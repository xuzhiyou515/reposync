package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"reposync/backend/internal/domain"
	"reposync/backend/internal/git"
	"reposync/backend/internal/scheduler"
	"reposync/backend/internal/scm"
	"reposync/backend/internal/security"
	"reposync/backend/internal/service"
	"reposync/backend/internal/store"
)

type Server struct {
	http            *http.Server
	mux             *http.ServeMux
	store           *store.Store
	service         *service.Service
	scheduler       *scheduler.Scheduler
	frontendDistDir string
}

var executionStreamUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func NewServer(cfg Config) (*Server, error) {
	box := security.NewSecretBox(cfg.SecretKey)
	dbStore, err := store.New(cfg.DBPath, box)
	if err != nil {
		return nil, err
	}
	svc := service.New(dbStore, cfg.CacheDir, git.NewClient(cfg.GitBin), scm.NewManager())
	sched := scheduler.New(svc)
	server := &Server{
		mux:             http.NewServeMux(),
		store:           dbStore,
		service:         svc,
		scheduler:       sched,
		frontendDistDir: cfg.FrontendDistDir,
	}
	if tasks, err := svc.ListTasks(context.Background()); err == nil {
		if err := sched.LoadTasks(tasks); err != nil {
			return nil, err
		}
	}
	server.routes()
	server.http = &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           withCORS(server.mux),
		ReadHeaderTimeout: 15 * time.Second,
	}
	return server, nil
}

func (s *Server) ListenAndServe(addr string) error {
	s.http.Addr = addr
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return err
	}
	if s.scheduler != nil {
		<-s.scheduler.Stop().Done()
	}
	return s.store.Close()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/tasks", s.handleTasks)
	s.mux.HandleFunc("/api/tasks/", s.handleTaskByID)
	s.mux.HandleFunc("/api/schedules", s.handleSchedules)
	s.mux.HandleFunc("/api/credentials", s.handleCredentials)
	s.mux.HandleFunc("/api/credentials/", s.handleCredentialByID)
	s.mux.HandleFunc("/api/caches", s.handleCaches)
	s.mux.HandleFunc("/api/caches/", s.handleCacheByID)
	s.mux.HandleFunc("/api/executions/", s.handleExecutionByID)
	s.mux.HandleFunc("/api/webhooks/github/", s.handleWebhook)
	s.mux.HandleFunc("/api/webhooks/gogs/", s.handleWebhook)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		distDir := s.frontendDistDir
		if strings.TrimSpace(distDir) == "" {
			distDir = filepath.Join("..", "frontend", "dist")
		}
		if _, err := os.Stat(filepath.Join(distDir, "index.html")); err == nil {
			http.FileServer(http.Dir(distDir)).ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "RepoSync API is running"})
	})
}

func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tasks, err := s.service.ListTasksForScheduling(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.scheduler.Statuses(tasks))
}

func parseIDAndTail(raw, prefix string) (int64, []string, error) {
	trimmed := strings.Trim(strings.TrimPrefix(raw, prefix), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 || parts[0] == "" {
		return 0, nil, sql.ErrNoRows
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, nil, err
	}
	return id, parts[1:], nil
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.service.ListTasks(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var task domain.SyncTask
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			writeError(w, http.StatusBadRequest, "invalid task payload")
			return
		}
		saved, err := s.service.SaveTask(r.Context(), task)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.scheduler.SyncTask(saved); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	id, tail, err := parseIDAndTail(r.URL.Path, "/api/tasks/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	if len(tail) == 1 && tail[0] == "run" && r.Method == http.MethodPost {
		execution, runErr := s.service.RunTask(r.Context(), id, domain.TriggerManual)
		if runErr != nil {
			writeError(w, http.StatusBadRequest, runErr.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, execution)
		return
	}
	if len(tail) == 1 && tail[0] == "executions" && r.Method == http.MethodGet {
		items, listErr := s.service.ListExecutions(r.Context(), id)
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, listErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	if len(tail) == 1 && tail[0] == "webhook-events" && r.Method == http.MethodGet {
		items, listErr := s.service.ListWebhookEvents(r.Context(), id)
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, listErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	if len(tail) == 3 && tail[0] == "webhook-events" && tail[2] == "replay" && r.Method == http.MethodPost {
		eventID, parseErr := strconv.ParseInt(tail[1], 10, 64)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid webhook event id")
			return
		}
		event, getErr := s.store.GetWebhookEvent(r.Context(), eventID)
		if getErr != nil {
			writeError(w, http.StatusNotFound, getErr.Error())
			return
		}
		if event.TaskID != id {
			writeError(w, http.StatusBadRequest, "webhook event does not belong to task")
			return
		}
		execution, runErr := s.service.RunTask(r.Context(), id, domain.TriggerWebhook)
		if runErr != nil {
			_, _ = s.store.CreateWebhookEvent(r.Context(), domain.WebhookEvent{
				TaskID:    id,
				Provider:  event.Provider,
				EventType: event.EventType,
				Ref:       event.Ref,
				Status:    "failed",
				Reason:    fmt.Sprintf("replay of event #%d failed: %v", eventID, runErr),
			})
			writeError(w, http.StatusBadRequest, runErr.Error())
			return
		}
		_, _ = s.store.CreateWebhookEvent(r.Context(), domain.WebhookEvent{
			TaskID:      id,
			Provider:    event.Provider,
			EventType:   event.EventType,
			Ref:         event.Ref,
			Status:      "accepted",
			Reason:      fmt.Sprintf("replayed from event #%d", eventID),
			ExecutionID: &execution.ID,
		})
		writeJSON(w, http.StatusAccepted, execution)
		return
	}
	if len(tail) == 1 && tail[0] == "schedule-status" && r.Method == http.MethodGet {
		task, getErr := s.service.GetTask(r.Context(), id)
		if getErr != nil {
			writeError(w, http.StatusNotFound, getErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.scheduler.Status(task))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var task domain.SyncTask
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			writeError(w, http.StatusBadRequest, "invalid task payload")
			return
		}
		task.ID = id
		saved, err := s.service.SaveTask(r.Context(), task)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.scheduler.SyncTask(saved); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		if err := s.service.DeleteTask(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.scheduler.RemoveTask(id)
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.service.ListCredentials(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var credential domain.Credential
		if err := json.NewDecoder(r.Body).Decode(&credential); err != nil {
			writeError(w, http.StatusBadRequest, "invalid credential payload")
			return
		}
		saved, err := s.service.SaveCredential(r.Context(), credential)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, saved)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleCredentialByID(w http.ResponseWriter, r *http.Request) {
	id, _, err := parseIDAndTail(r.URL.Path, "/api/credentials/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var credential domain.Credential
		if err := json.NewDecoder(r.Body).Decode(&credential); err != nil {
			writeError(w, http.StatusBadRequest, "invalid credential payload")
			return
		}
		credential.ID = id
		saved, err := s.service.SaveCredential(r.Context(), credential)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		if err := s.service.DeleteCredential(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleCaches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := s.service.ListCaches(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCacheByID(w http.ResponseWriter, r *http.Request) {
	id, tail, err := parseIDAndTail(r.URL.Path, "/api/caches/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cache id")
		return
	}
	if len(tail) == 1 && tail[0] == "cleanup" && r.Method == http.MethodPost {
		if err := s.service.CleanupCache(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"cleaned": true})
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *Server) handleExecutionByID(w http.ResponseWriter, r *http.Request) {
	id, tail, err := parseIDAndTail(r.URL.Path, "/api/executions/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid execution id")
		return
	}
	if len(tail) == 1 && tail[0] == "stream" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleExecutionStream(w, r, id)
		return
	}
	if len(tail) == 1 && tail[0] == "ws" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleExecutionWebSocket(w, r, id)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	detail, err := s.service.ExecutionDetail(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleExecutionStream(w http.ResponseWriter, r *http.Request, id int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastSignature := ""
	writeDetail := func(detail domain.ExecutionDetail) error {
		payload, signature, err := executionStreamPayload(detail)
		if err != nil {
			return err
		}
		if signature == lastSignature {
			return nil
		}
		lastSignature = signature
		if _, err := fmt.Fprintf(w, "event: execution\ndata: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	detail, err := s.service.ExecutionDetail(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := writeDetail(detail); err != nil {
		return
	}

	for {
		if detail.Execution.Status != domain.ExecutionStatusRunning {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			detail, err = s.service.ExecutionDetail(r.Context(), id)
			if err != nil {
				return
			}
			if err := writeDetail(detail); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleExecutionWebSocket(w http.ResponseWriter, r *http.Request, id int64) {
	conn, err := executionStreamUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, readErr := conn.ReadMessage(); readErr != nil {
				return
			}
		}
	}()

	lastSignature := ""
	writeDetail := func(detail domain.ExecutionDetail) error {
		payload, signature, err := executionStreamPayload(detail)
		if err != nil {
			return err
		}
		if signature == lastSignature {
			return nil
		}
		lastSignature = signature
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, payload)
	}

	detail, err := s.service.ExecutionDetail(r.Context(), id)
	if err != nil {
		return
	}
	if err := writeDetail(detail); err != nil {
		return
	}

	for {
		if detail.Execution.Status != domain.ExecutionStatusRunning {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case <-ticker.C:
			detail, err = s.service.ExecutionDetail(r.Context(), id)
			if err != nil {
				return
			}
			if err := writeDetail(detail); err != nil {
				return
			}
		}
	}
}

func executionStreamPayload(detail domain.ExecutionDetail) ([]byte, string, error) {
	payload, err := json.Marshal(map[string]any{
		"type":   "execution",
		"detail": detail,
	})
	if err != nil {
		return nil, "", err
	}
	return payload, string(payload), nil
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, _, err := parseIDAndTail(r.URL.Path, "/api/webhooks/github/")
	if err != nil {
		id, _, err = parseIDAndTail(r.URL.Path, "/api/webhooks/gogs/")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	task, taskErr := s.service.GetTask(r.Context(), id)
	if taskErr != nil {
		writeError(w, http.StatusNotFound, taskErr.Error())
		return
	}
	provider := string(detectWebhookProvider(r.URL.Path))
	eventType := webhookEventType(r)
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		writeError(w, http.StatusBadRequest, "failed to read webhook body")
		return
	}
	if !task.Enabled || !task.TriggerConfig.EnableWebhook {
		_, _ = s.store.CreateWebhookEvent(r.Context(), domain.WebhookEvent{
			TaskID:    id,
			Provider:  provider,
			EventType: eventType,
			Ref:       webhookRef(body),
			Status:    "blocked",
			Reason:    "webhook is disabled for this task",
		})
		writeError(w, http.StatusForbidden, "webhook is disabled for this task")
		return
	}
	if err := validateWebhookSignature(r, body, task.TriggerConfig.WebhookSecret); err != nil {
		_, _ = s.store.CreateWebhookEvent(r.Context(), domain.WebhookEvent{
			TaskID:    id,
			Provider:  provider,
			EventType: eventType,
			Ref:       webhookRef(body),
			Status:    "rejected",
			Reason:    err.Error(),
		})
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if shouldRun, reason := shouldProcessWebhook(r, body, task); !shouldRun {
		_, _ = s.store.CreateWebhookEvent(r.Context(), domain.WebhookEvent{
			TaskID:    id,
			Provider:  provider,
			EventType: eventType,
			Ref:       webhookRef(body),
			Status:    "ignored",
			Reason:    reason,
		})
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ignored": true,
			"reason":  reason,
		})
		return
	}
	execution, runErr := s.service.RunTask(r.Context(), id, domain.TriggerWebhook)
	if runErr != nil {
		_, _ = s.store.CreateWebhookEvent(r.Context(), domain.WebhookEvent{
			TaskID:    id,
			Provider:  provider,
			EventType: eventType,
			Ref:       webhookRef(body),
			Status:    "failed",
			Reason:    runErr.Error(),
		})
		writeError(w, http.StatusBadRequest, runErr.Error())
		return
	}
	_, _ = s.store.CreateWebhookEvent(r.Context(), domain.WebhookEvent{
		TaskID:      id,
		Provider:    provider,
		EventType:   eventType,
		Ref:         webhookRef(body),
		Status:      "accepted",
		Reason:      "execution started",
		ExecutionID: &execution.ID,
	})
	writeJSON(w, http.StatusAccepted, execution)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validateWebhookSignature(r *http.Request, body []byte, secret string) error {
	if secret == "" {
		return nil
	}
	if signature := r.Header.Get("X-Hub-Signature-256"); signature != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(expected), []byte(signature)) {
			return nil
		}
		return fmt.Errorf("invalid github webhook signature")
	}
	if signature := r.Header.Get("X-Gogs-Signature"); signature != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(expected), []byte(signature)) {
			return nil
		}
		return fmt.Errorf("invalid gogs webhook signature")
	}
	if signature := r.Header.Get("X-Hub-Signature"); signature != "" {
		mac := hmac.New(sha1.New, []byte(secret))
		_, _ = mac.Write(body)
		expected := "sha1=" + hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(expected), []byte(signature)) {
			return nil
		}
		return fmt.Errorf("invalid github webhook signature")
	}
	return fmt.Errorf("missing webhook signature")
}

func shouldProcessWebhook(r *http.Request, body []byte, task domain.SyncTask) (bool, string) {
	provider := detectWebhookProvider(r.URL.Path)
	switch provider {
	case domain.ProviderGitHub:
		event := r.Header.Get("X-GitHub-Event")
		if event != "" && event != "push" {
			return false, "unsupported github event"
		}
	case domain.ProviderGogs:
		event := r.Header.Get("X-Gogs-Event")
		if event != "" && event != "push" {
			return false, "unsupported gogs event"
		}
	}

	ref := webhookRef(body)
	if task.TriggerConfig.BranchReference != "" && ref != "" && task.TriggerConfig.BranchReference != ref {
		return false, "branch does not match trigger config"
	}
	return true, ""
}

func detectWebhookProvider(path string) domain.ProviderType {
	if strings.Contains(path, "/api/webhooks/gogs/") {
		return domain.ProviderGogs
	}
	return domain.ProviderGitHub
}

func webhookRef(body []byte) string {
	var payload struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return payload.Ref
}

func webhookEventType(r *http.Request) string {
	if event := r.Header.Get("X-GitHub-Event"); event != "" {
		return event
	}
	if event := r.Header.Get("X-Gogs-Event"); event != "" {
		return event
	}
	return "push"
}

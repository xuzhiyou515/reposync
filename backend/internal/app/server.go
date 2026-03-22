package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reposync/backend/internal/domain"
	"reposync/backend/internal/security"
	"reposync/backend/internal/service"
	"reposync/backend/internal/store"
)

type Server struct {
	http    *http.Server
	mux     *http.ServeMux
	store   *store.Store
	service *service.Service
}

func NewServer(cfg Config) (*Server, error) {
	box := security.NewSecretBox(cfg.SecretKey)
	dbStore, err := store.New(cfg.DBPath, box)
	if err != nil {
		return nil, err
	}
	svc := service.New(dbStore, cfg.CacheDir)
	server := &Server{
		mux:     http.NewServeMux(),
		store:   dbStore,
		service: svc,
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
	return s.store.Close()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/tasks", s.handleTasks)
	s.mux.HandleFunc("/api/tasks/", s.handleTaskByID)
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
		distDir := filepath.Join("..", "frontend", "dist")
		if _, err := os.Stat(filepath.Join(distDir, "index.html")); err == nil {
			http.FileServer(http.Dir(distDir)).ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "RepoSync API is running"})
	})
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
		writeJSON(w, http.StatusOK, saved)
	case http.MethodDelete:
		if err := s.service.DeleteTask(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
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
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, _, err := parseIDAndTail(r.URL.Path, "/api/executions/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid execution id")
		return
	}
	detail, err := s.service.ExecutionDetail(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
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
	execution, runErr := s.service.RunTask(r.Context(), id, domain.TriggerWebhook)
	if runErr != nil {
		writeError(w, http.StatusBadRequest, runErr.Error())
		return
	}
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

package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"reposync/backend/internal/domain"
	"reposync/backend/internal/git"
	"reposync/backend/internal/scheduler"
	"reposync/backend/internal/scm"
	"reposync/backend/internal/security"
	"reposync/backend/internal/service"
	"reposync/backend/internal/store"
)

func TestValidateWebhookSignatureGitHub(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest("POST", "/api/webhooks/github/1", nil)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	if err := validateWebhookSignature(req, body, "secret"); err != nil {
		t.Fatalf("expected valid signature, got %v", err)
	}
}

func TestValidateWebhookSignatureGogsRejectsInvalid(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest("POST", "/api/webhooks/gogs/1", nil)
	req.Header.Set("X-Gogs-Signature", "invalid")
	if err := validateWebhookSignature(req, body, "secret"); err == nil {
		t.Fatalf("expected invalid signature error")
	}
}

func TestShouldProcessWebhookRejectsNonPushEvent(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhooks/github/1", nil)
	req.Header.Set("X-GitHub-Event", "ping")
	ok, reason := shouldProcessWebhook(req, []byte(`{"ref":"refs/heads/main"}`), domain.SyncTask{})
	if ok {
		t.Fatalf("expected webhook to be ignored")
	}
	if reason == "" {
		t.Fatalf("expected ignore reason")
	}
}

func TestShouldProcessWebhookRejectsMismatchedBranch(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhooks/gogs/1", nil)
	req.Header.Set("X-Gogs-Event", "push")
	ok, reason := shouldProcessWebhook(req, []byte(`{"ref":"refs/heads/dev"}`), domain.SyncTask{
		TriggerConfig: domain.TriggerConfig{BranchReference: "refs/heads/main"},
	})
	if ok {
		t.Fatalf("expected webhook to be ignored for mismatched branch")
	}
	if reason == "" {
		t.Fatalf("expected ignore reason")
	}
}

func TestShouldProcessWebhookAcceptsMatchingPush(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhooks/github/1", nil)
	req.Header.Set("X-GitHub-Event", "push")
	ok, reason := shouldProcessWebhook(req, []byte(`{"ref":"refs/heads/main"}`), domain.SyncTask{
		TriggerConfig: domain.TriggerConfig{BranchReference: "refs/heads/main"},
	})
	if !ok {
		t.Fatalf("expected webhook to be processed, got reason %q", reason)
	}
}

func TestHandleExecutionStreamWritesEvent(t *testing.T) {
	db := filepathJoinTemp(t, "reposync.db")
	box := security.NewSecretBox("test-secret")
	dbStore, err := store.New(db, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer dbStore.Close()

	svc := service.New(dbStore, filepathJoinTemp(t, "cache"), git.NewClient("git"), scm.NewManager())
	ctx := context.Background()
	_, err = dbStore.SaveTask(ctx, domain.SyncTask{
		Name:           "demo",
		SourceRepoURL:  "src",
		TargetRepoURL:  "dst",
		Enabled:        true,
		SyncAllRefs:    true,
		ProviderConfig: domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
		TriggerConfig:  domain.TriggerConfig{},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	execution, err := dbStore.CreateExecution(ctx, domain.SyncExecution{
		TaskID:      1,
		TriggerType: domain.TriggerManual,
		Status:      domain.ExecutionStatusSuccess,
		SummaryLog:  "line 1",
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}

	server := &Server{mux: http.NewServeMux(), store: dbStore, service: svc}
	server.routes()
	req := httptest.NewRequest(http.MethodGet, "/api/executions/"+strconv.FormatInt(execution.ID, 10)+"/stream", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "event: execution") {
		t.Fatalf("expected sse execution event, got %q", body)
	}
	if !strings.Contains(body, `"summaryLog":"line 1"`) {
		t.Fatalf("expected execution payload in stream, got %q", body)
	}
}

func TestHandleExecutionWebSocketWritesEvent(t *testing.T) {
	db := filepathJoinTemp(t, "reposync.db")
	box := security.NewSecretBox("test-secret")
	dbStore, err := store.New(db, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer dbStore.Close()

	svc := service.New(dbStore, filepathJoinTemp(t, "cache"), git.NewClient("git"), scm.NewManager())
	ctx := context.Background()
	_, err = dbStore.SaveTask(ctx, domain.SyncTask{
		Name:           "demo",
		SourceRepoURL:  "src",
		TargetRepoURL:  "dst",
		Enabled:        true,
		SyncAllRefs:    true,
		ProviderConfig: domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
		TriggerConfig:  domain.TriggerConfig{},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	execution, err := dbStore.CreateExecution(ctx, domain.SyncExecution{
		TaskID:      1,
		TriggerType: domain.TriggerManual,
		Status:      domain.ExecutionStatusSuccess,
		SummaryLog:  "line 1",
	})
	if err != nil {
		t.Fatalf("create execution: %v", err)
	}

	server := &Server{mux: http.NewServeMux(), store: dbStore, service: svc}
	server.routes()
	testServer := httptest.NewServer(server.mux)
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/api/executions/" + strconv.FormatInt(execution.ID, 10) + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	_, message, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	var payload struct {
		Type   string                 `json:"type"`
		Detail domain.ExecutionDetail `json:"detail"`
	}
	if err := json.Unmarshal(message, &payload); err != nil {
		t.Fatalf("unmarshal websocket payload: %v", err)
	}
	if payload.Type != "execution" {
		t.Fatalf("expected execution payload type, got %q", payload.Type)
	}
	if payload.Detail.Execution.SummaryLog != "line 1" {
		t.Fatalf("expected summary log in websocket payload, got %q", payload.Detail.Execution.SummaryLog)
	}
}

func TestHandleSchedulesReturnsRegisteredStatus(t *testing.T) {
	db := filepathJoinTemp(t, "reposync.db")
	box := security.NewSecretBox("test-secret")
	dbStore, err := store.New(db, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer dbStore.Close()

	svc := service.New(dbStore, filepathJoinTemp(t, "cache"), git.NewClient("git"), scm.NewManager())
	server := &Server{
		mux:       http.NewServeMux(),
		store:     dbStore,
		service:   svc,
		scheduler: scheduler.New(svc),
	}
	defer server.scheduler.Stop()
	server.routes()

	ctx := context.Background()
	task, err := dbStore.SaveTask(ctx, domain.SyncTask{
		Name:          "scheduled-demo",
		SourceRepoURL: "src",
		TargetRepoURL: "dst",
		Enabled:       true,
		SyncAllRefs:   true,
		ProviderConfig: domain.ProviderConfig{
			Provider:   domain.ProviderGitHub,
			Visibility: domain.VisibilityPrivate,
		},
		TriggerConfig: domain.TriggerConfig{
			EnableSchedule: true,
			Cron:           "*/30 * * * * *",
		},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if err := server.scheduler.SyncTask(task); err != nil {
		t.Fatalf("sync task into scheduler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/schedules", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %q", rec.Code, body)
	}
	if !strings.Contains(body, `"taskId":`+strconv.FormatInt(task.ID, 10)) {
		t.Fatalf("expected schedule payload to include task id, got %q", body)
	}
	if !strings.Contains(body, `"registered":true`) {
		t.Fatalf("expected schedule payload to mark registered, got %q", body)
	}
	if !strings.Contains(body, `"cron":"*/30 * * * * *"`) {
		t.Fatalf("expected schedule payload to include cron, got %q", body)
	}
}

func TestReplayWebhookEventRunsTaskAgain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for replay integration test")
	}

	root := t.TempDir()
	sourceBare := filepath.Join(root, "source.git")
	targetBare := filepath.Join(root, "target.git")
	worktree := filepath.Join(root, "work")
	dbPath := filepath.Join(root, "reposync.db")
	cacheDir := filepath.Join(root, "cache")

	runGitForAppTest(t, "", "init", "--bare", sourceBare)
	runGitForAppTest(t, "", "init", "--bare", targetBare)
	runGitForAppTest(t, "", "clone", sourceBare, worktree)
	runGitForAppTest(t, worktree, "config", "user.name", "RepoSync Test")
	runGitForAppTest(t, worktree, "config", "user.email", "reposync@example.com")
	runGitForAppTest(t, worktree, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitForAppTest(t, worktree, "add", ".")
	runGitForAppTest(t, worktree, "commit", "-m", "init")
	runGitForAppTest(t, worktree, "push", "-u", "origin", "main")

	box := security.NewSecretBox("test-secret")
	dbStore, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer dbStore.Close()

	svc := service.New(dbStore, cacheDir, git.NewClient("git"), scm.NewManager())
	server := &Server{
		mux:       http.NewServeMux(),
		store:     dbStore,
		service:   svc,
		scheduler: scheduler.New(svc),
	}
	defer server.scheduler.Stop()
	server.routes()

	ctx := context.Background()
	task, err := dbStore.SaveTask(ctx, domain.SyncTask{
		Name:          "replay-demo",
		SourceRepoURL: sourceBare,
		TargetRepoURL: targetBare,
		Enabled:       true,
		SyncAllRefs:   true,
		ProviderConfig: domain.ProviderConfig{
			Provider:   domain.ProviderGitHub,
			Visibility: domain.VisibilityPrivate,
		},
		TriggerConfig: domain.TriggerConfig{
			EnableWebhook: true,
		},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	event, err := dbStore.CreateWebhookEvent(ctx, domain.WebhookEvent{
		TaskID:    task.ID,
		Provider:  "github",
		EventType: "push",
		Ref:       "refs/heads/main",
		Status:    "ignored",
		Reason:    "branch does not match trigger config",
	})
	if err != nil {
		t.Fatalf("create webhook event: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/webhook-events/"+strconv.FormatInt(event.ID, 10)+"/replay", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d with body %q", rec.Code, rec.Body.String())
	}
	events, err := dbStore.ListWebhookEventsForTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list webhook events: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected replay to append webhook event, got %d", len(events))
	}
	if !strings.Contains(events[0].Reason, "replayed from event") {
		t.Fatalf("expected latest webhook event to record replay reason, got %q", events[0].Reason)
	}
}

func filepathJoinTemp(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

func runGitForAppTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

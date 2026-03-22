package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

func filepathJoinTemp(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

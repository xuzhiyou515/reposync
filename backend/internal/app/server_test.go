package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"reposync/backend/internal/domain"
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

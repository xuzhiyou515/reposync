package store

import (
	"context"
	"path/filepath"
	"testing"

	"reposync/backend/internal/domain"
	"reposync/backend/internal/security"
)

func TestWebhookEventsRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	first, err := db.CreateWebhookEvent(ctx, domain.WebhookEvent{
		TaskID:    42,
		Provider:  "github",
		EventType: "push",
		Ref:       "refs/heads/main",
		Status:    "ignored",
		Reason:    "branch does not match trigger config",
	})
	if err != nil {
		t.Fatalf("create first webhook event: %v", err)
	}
	second, err := db.CreateWebhookEvent(ctx, domain.WebhookEvent{
		TaskID:    42,
		Provider:  "github",
		EventType: "push",
		Ref:       "refs/heads/main",
		Status:    "accepted",
		Reason:    "execution started",
	})
	if err != nil {
		t.Fatalf("create second webhook event: %v", err)
	}

	events, err := db.ListWebhookEventsForTask(ctx, 42)
	if err != nil {
		t.Fatalf("list webhook events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != second.ID {
		t.Fatalf("expected latest event first, got id %d", events[0].ID)
	}
	if events[1].ID != first.ID {
		t.Fatalf("expected oldest event second, got id %d", events[1].ID)
	}
}

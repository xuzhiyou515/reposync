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

func TestTaskRoundTripWithSeparateTargetAPICredential(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	sourceCredentialID := int64(1)
	targetCredentialID := int64(2)
	targetAPICredentialID := int64(3)

	task, err := db.SaveTask(ctx, domain.SyncTask{
		Name:                  "credential-split",
		SourceRepoURL:         "src",
		TargetRepoURL:         "dst",
		SourceCredentialID:    &sourceCredentialID,
		TargetCredentialID:    &targetCredentialID,
		TargetAPICredentialID: &targetAPICredentialID,
		Enabled:               true,
		SyncAllRefs:           true,
		ProviderConfig:        domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.TargetAPICredentialID == nil || *task.TargetAPICredentialID != targetAPICredentialID {
		t.Fatalf("expected separate target api credential id to round trip, got %+v", task.TargetAPICredentialID)
	}
}

func TestTaskRoundTripWithSubmoduleCredentials(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	sourceCredentialID := int64(1)
	submoduleSourceCredentialID := int64(2)
	targetCredentialID := int64(3)
	submoduleTargetCredentialID := int64(4)
	targetAPICredentialID := int64(5)
	submoduleTargetAPICredentialID := int64(6)

	task, err := db.SaveTask(ctx, domain.SyncTask{
		Name:                           "submodule-credential-split",
		SourceRepoURL:                  "src",
		TargetRepoURL:                  "dst",
		SourceCredentialID:             &sourceCredentialID,
		SubmoduleSourceCredentialID:    &submoduleSourceCredentialID,
		TargetCredentialID:             &targetCredentialID,
		SubmoduleTargetCredentialID:    &submoduleTargetCredentialID,
		TargetAPICredentialID:          &targetAPICredentialID,
		SubmoduleTargetAPICredentialID: &submoduleTargetAPICredentialID,
		SubmoduleRewriteProtocol:       domain.SubmoduleRewriteProtocolSSH,
		Enabled:                        true,
		SyncAllRefs:                    true,
		ProviderConfig:                 domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.SubmoduleSourceCredentialID == nil || *task.SubmoduleSourceCredentialID != submoduleSourceCredentialID {
		t.Fatalf("expected submodule source credential id to round trip, got %+v", task.SubmoduleSourceCredentialID)
	}
	if task.SubmoduleTargetCredentialID == nil || *task.SubmoduleTargetCredentialID != submoduleTargetCredentialID {
		t.Fatalf("expected submodule target credential id to round trip, got %+v", task.SubmoduleTargetCredentialID)
	}
	if task.SubmoduleTargetAPICredentialID == nil || *task.SubmoduleTargetAPICredentialID != submoduleTargetAPICredentialID {
		t.Fatalf("expected submodule target api credential id to round trip, got %+v", task.SubmoduleTargetAPICredentialID)
	}
	if task.SubmoduleRewriteProtocol != domain.SubmoduleRewriteProtocolSSH {
		t.Fatalf("expected submodule rewrite protocol to round trip, got %q", task.SubmoduleRewriteProtocol)
	}
}

func TestTaskRoundTripWithSVNImportConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	task, err := db.SaveTask(ctx, domain.SyncTask{
		TaskType:      domain.TaskTypeSVNImport,
		Name:          "svn-import",
		SourceRepoURL: "https://svn.example.com/repos/demo",
		TargetRepoURL: "https://git.example.com/mirror/demo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		ProviderConfig: domain.ProviderConfig{
			Provider:   domain.ProviderGitHub,
			Visibility: domain.VisibilityPrivate,
		},
		SVNConfig: domain.SVNConfig{
			AuthorsFilePath: "/tmp/authors.txt",
			AuthorDomain:    "svn.example.com",
		},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.TaskType != domain.TaskTypeSVNImport {
		t.Fatalf("expected task type svn_import, got %s", task.TaskType)
	}
	if task.SVNConfig.TrunkPath != "trunk" || task.SVNConfig.BranchesPath != "branches" || task.SVNConfig.TagsPath != "tags" {
		t.Fatalf("expected default svn layout, got %+v", task.SVNConfig)
	}
	if task.SVNConfig.AuthorsFilePath != "/tmp/authors.txt" {
		t.Fatalf("expected authors file path to round trip, got %q", task.SVNConfig.AuthorsFilePath)
	}
	if task.SVNConfig.AuthorDomain != "svn.example.com" {
		t.Fatalf("expected author domain to round trip, got %q", task.SVNConfig.AuthorDomain)
	}
}

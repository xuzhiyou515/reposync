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
			StartRevision:   "120000",
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
	if task.SVNConfig.StartRevision != "120000" {
		t.Fatalf("expected start revision to round trip, got %q", task.SVNConfig.StartRevision)
	}
	if task.SVNConfig.AuthorDomain != "svn.example.com" {
		t.Fatalf("expected author domain to round trip, got %q", task.SVNConfig.AuthorDomain)
	}
}

func TestTaskRoundTripWithSVNSingleDirectoryLayout(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	task, err := db.SaveTask(ctx, domain.SyncTask{
		TaskType:      domain.TaskTypeSVNImport,
		Name:          "svn-root-layout",
		SourceRepoURL: "svn://svn.example.com/repos/demo",
		TargetRepoURL: "https://git.example.com/mirror/demo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		ProviderConfig: domain.ProviderConfig{
			Provider:   domain.ProviderGitHub,
			Visibility: domain.VisibilityPrivate,
		},
		SVNConfig: domain.SVNConfig{
			TrunkPath:     ".",
			BranchesPath:  "",
			TagsPath:      "",
			StartRevision: "4500",
			AuthorDomain:  "svn.example.com",
		},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.SVNConfig.TrunkPath != "." || task.SVNConfig.BranchesPath != "" || task.SVNConfig.TagsPath != "" {
		t.Fatalf("expected single-directory layout to round trip, got %+v", task.SVNConfig)
	}
	if task.SVNConfig.StartRevision != "4500" {
		t.Fatalf("expected start revision to round trip, got %q", task.SVNConfig.StartRevision)
	}
}

func TestCacheRoundTripWithTaskLink(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	taskID := int64(7)
	anotherTaskID := int64(8)
	if err := db.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      "cache-key",
		SourceRepoURL: "src",
		AuthContext:   "managed",
		CachePath:     "/tmp/cache-key.git",
		HitCount:      3,
		SizeBytes:     42,
		HealthStatus:  "ready",
	}); err != nil {
		t.Fatalf("upsert cache: %v", err)
	}
	if err := db.LinkCacheToTask(ctx, "cache-key", taskID); err != nil {
		t.Fatalf("link cache to task: %v", err)
	}
	if err := db.LinkCacheToTask(ctx, "cache-key", anotherTaskID); err != nil {
		t.Fatalf("link cache to another task: %v", err)
	}

	item, err := db.GetCacheByKey(ctx, "cache-key")
	if err != nil {
		t.Fatalf("get cache by key: %v", err)
	}
	links, err := db.ListCacheTaskIDs(ctx, item.CacheKey)
	if err != nil {
		t.Fatalf("list cache task ids: %v", err)
	}
	if len(links) != 2 || links[0] != taskID || links[1] != anotherTaskID {
		t.Fatalf("expected task link to round trip, got %+v", links)
	}
}

func TestListCachesForTask(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      "cache-key",
		SourceRepoURL: "src",
		AuthContext:   "managed",
		CachePath:     "/tmp/cache-key.git",
		HealthStatus:  "ready",
	}); err != nil {
		t.Fatalf("upsert cache: %v", err)
	}
	if err := db.LinkCacheToTask(ctx, "cache-key", 7); err != nil {
		t.Fatalf("link first task: %v", err)
	}
	if err := db.LinkCacheToTask(ctx, "cache-key", 8); err != nil {
		t.Fatalf("link second task: %v", err)
	}

	caches, err := db.ListCachesForTask(ctx, 7)
	if err != nil {
		t.Fatalf("list caches for task: %v", err)
	}
	if len(caches) != 1 || caches[0].CacheKey != "cache-key" {
		t.Fatalf("expected task 7 to list cache-key, got %+v", caches)
	}
}

func TestListCacheKeysForTask(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	for _, key := range []string{"cache-a", "cache-b"} {
		if err := db.UpsertCache(ctx, domain.RepoCache{
			CacheKey:      key,
			SourceRepoURL: "src",
			AuthContext:   "managed",
			CachePath:     "/tmp/" + key + ".git",
			HealthStatus:  "ready",
		}); err != nil {
			t.Fatalf("upsert cache %s: %v", key, err)
		}
		if err := db.LinkCacheToTask(ctx, key, 7); err != nil {
			t.Fatalf("link cache %s: %v", key, err)
		}
	}

	keys, err := db.ListCacheKeysForTask(ctx, 7)
	if err != nil {
		t.Fatalf("list cache keys for task: %v", err)
	}
	if len(keys) != 2 || keys[0] != "cache-a" || keys[1] != "cache-b" {
		t.Fatalf("expected both cache keys, got %+v", keys)
	}
}

func TestRenameCacheKeyUpdatesCacheAndLinks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reposync.db")
	db, err := New(dbPath, security.NewSecretBox("test-secret"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      "cache-old",
		SourceRepoURL: "src",
		AuthContext:   "managed",
		CachePath:     "/tmp/cache-old.git",
		HealthStatus:  "ready",
	}); err != nil {
		t.Fatalf("upsert old cache: %v", err)
	}
	if err := db.LinkCacheToTask(ctx, "cache-old", 7); err != nil {
		t.Fatalf("link old cache: %v", err)
	}

	if err := db.RenameCacheKey(ctx, "cache-old", "cache-new"); err != nil {
		t.Fatalf("rename cache key: %v", err)
	}

	keys, err := db.ListCacheKeysForTask(ctx, 7)
	if err != nil {
		t.Fatalf("list cache keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "cache-new" {
		t.Fatalf("expected renamed cache key, got %+v", keys)
	}
	cache, err := db.GetCacheByKey(ctx, "cache-new")
	if err != nil {
		t.Fatalf("get renamed cache: %v", err)
	}
	if cache.CachePath != "/tmp/cache-old.git" {
		t.Fatalf("expected cache path to stay unchanged, got %q", cache.CachePath)
	}
}

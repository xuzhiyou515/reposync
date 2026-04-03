package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reposync/backend/internal/domain"
	gitclient "reposync/backend/internal/git"
	"reposync/backend/internal/scm"
	"reposync/backend/internal/security"
	"reposync/backend/internal/store"
)

func TestBuildCacheKeyStable(t *testing.T) {
	left := buildCacheKey(domain.TaskTypeGitMirror, "source", "target")
	right := buildCacheKey(domain.TaskTypeGitMirror, "source", "target")
	if left != right {
		t.Fatalf("expected cache key to be stable")
	}
}

func TestBuildCacheKeySeparatesTaskTypes(t *testing.T) {
	gitMirrorKey := buildCacheKey(domain.TaskTypeGitMirror, "source", "target")
	svnImportKey := buildCacheKey(domain.TaskTypeSVNImport, "source", "target")
	if gitMirrorKey == svnImportKey {
		t.Fatalf("expected cache keys to differ across task types")
	}
}

func TestBuildCacheKeyIgnoresGitMirrorTargetRepoURL(t *testing.T) {
	left := buildCacheKey(domain.TaskTypeGitMirror, "https://git.example.com/org/repo.git", "https://target-a.example.com/org/repo.git")
	right := buildCacheKey(domain.TaskTypeGitMirror, "https://git.example.com/org/repo.git", "ssh://git@target-b.example.com:2222/org/repo.git")
	if left != right {
		t.Fatalf("expected git_mirror cache key to ignore target repo url")
	}
}

func TestBuildCacheKeyIgnoresGitMirrorSourceProtocolDifferences(t *testing.T) {
	httpsKey := buildCacheKey(domain.TaskTypeGitMirror, "https://git.example.com/org/repo.git", "target-a")
	sshKey := buildCacheKey(domain.TaskTypeGitMirror, "ssh://git@git.example.com:2222/org/repo.git", "target-b")
	scpKey := buildCacheKey(domain.TaskTypeGitMirror, "git@git.example.com:org/repo.git", "target-c")
	if httpsKey != sshKey || sshKey != scpKey {
		t.Fatalf("expected git_mirror cache key to ignore source protocol differences")
	}
}

func TestBuildCacheKeySeparatesSVNLayouts(t *testing.T) {
	rootLayoutKey := buildCacheKey(domain.TaskTypeSVNImport, "source", "target", domain.SVNConfig{
		TrunkPath:    ".",
		BranchesPath: "",
		TagsPath:     "",
	})
	standardLayoutKey := buildCacheKey(domain.TaskTypeSVNImport, "source", "target", domain.SVNConfig{
		TrunkPath:    "trunk",
		BranchesPath: "branches",
		TagsPath:     "tags",
	})
	if rootLayoutKey == standardLayoutKey {
		t.Fatalf("expected cache keys to differ across svn layouts")
	}
}

func TestBuildCacheKeySeparatesSVNStartRevision(t *testing.T) {
	firstKey := buildCacheKey(domain.TaskTypeSVNImport, "source", "target", domain.SVNConfig{
		TrunkPath:     ".",
		StartRevision: "120000",
	})
	secondKey := buildCacheKey(domain.TaskTypeSVNImport, "source", "target", domain.SVNConfig{
		TrunkPath:     ".",
		StartRevision: "130000",
	})
	if firstKey == secondKey {
		t.Fatalf("expected cache keys to differ across svn start revisions")
	}
}

func TestBuildCacheKeyIgnoresSVNSourceProtocolDifferences(t *testing.T) {
	httpKey := buildCacheKey(domain.TaskTypeSVNImport, "http://svn.example.com/repos/project", "target-a", domain.SVNConfig{
		TrunkPath:     ".",
		StartRevision: "120000",
	})
	svnKey := buildCacheKey(domain.TaskTypeSVNImport, "svn://svn.example.com/repos/project", "target-b", domain.SVNConfig{
		TrunkPath:     ".",
		StartRevision: "120000",
	})
	if httpKey != svnKey {
		t.Fatalf("expected svn cache key to ignore source protocol differences")
	}
}

func TestNormalizeSVNSourceIdentity(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "svn://svn.example.com/repos/project", expected: "svn.example.com/repos/project"},
		{input: "https://user:pass@svn.example.com/repos/project?x=1#frag", expected: "svn.example.com/repos/project"},
		{input: "https://svn.example.com:8443/repos/project", expected: "svn.example.com/repos/project"},
		{input: " svn://SVN.EXAMPLE.COM/repos/project/ ", expected: "svn.example.com/repos/project"},
	}

	for _, tc := range cases {
		if got := normalizeSVNSourceIdentity(tc.input); got != tc.expected {
			t.Fatalf("normalize source identity %q: expected %q, got %q", tc.input, tc.expected, got)
		}
	}
}

func TestNormalizeGitSourceIdentity(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "https://user:pass@git.example.com/org/repo.git?x=1#frag", expected: "git.example.com/org/repo"},
		{input: "ssh://git@git.example.com:2222/org/repo.git", expected: "git.example.com/org/repo"},
		{input: "git@git.example.com:org/repo.git", expected: "git.example.com/org/repo"},
	}

	for _, tc := range cases {
		if got := normalizeGitSourceIdentity(tc.input); got != tc.expected {
			t.Fatalf("normalize git source identity %q: expected %q, got %q", tc.input, tc.expected, got)
		}
	}
}

func TestMapSubmoduleTargetUsesRepoNameFromGitmodulesURL(t *testing.T) {
	cases := []struct {
		name          string
		parentTarget  string
		submoduleURL  string
		submodulePath string
		protocol      domain.SubmoduleRewriteProtocol
		expected      string
	}{
		{
			name:          "local bare path",
			parentTarget:  filepath.ToSlash(filepath.Join("D:/repos/targets", "main.git")),
			submoduleURL:  "https://github.com/example/libs-core.git",
			submodulePath: "libs/core",
			protocol:      domain.SubmoduleRewriteProtocolInherit,
			expected:      filepath.ToSlash(filepath.Join("D:/repos/targets", "libs-core.git")),
		},
		{
			name:          "http target",
			parentTarget:  "https://git.example.com/mirror/main.git",
			submoduleURL:  "ssh://git@github.com/example/core-lib.git",
			submodulePath: "vendor/core",
			protocol:      domain.SubmoduleRewriteProtocolInherit,
			expected:      "https://git.example.com/mirror/core-lib.git",
		},
		{
			name:          "scp target",
			parentTarget:  "git@gogs.example.com:mirror/main.git",
			submoduleURL:  "git@github.com:example/child.git",
			submodulePath: "child",
			protocol:      domain.SubmoduleRewriteProtocolInherit,
			expected:      "git@gogs.example.com:mirror/child.git",
		},
		{
			name:          "force ssh rewrite from https target",
			parentTarget:  "https://git.example.com/mirror/main.git",
			submoduleURL:  "https://github.com/example/child.git",
			submodulePath: "child",
			protocol:      domain.SubmoduleRewriteProtocolSSH,
			expected:      "ssh://git@git.example.com/mirror/child.git",
		},
		{
			name:          "force http rewrite from ssh target",
			parentTarget:  "git@gogs.example.com:mirror/main.git",
			submoduleURL:  "git@github.com:example/child.git",
			submodulePath: "child",
			protocol:      domain.SubmoduleRewriteProtocolHTTP,
			expected:      "https://gogs.example.com/mirror/child.git",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapSubmoduleTarget(tc.parentTarget, tc.submoduleURL, tc.submodulePath, tc.protocol)
			if got != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, got)
			}
		})
	}
}

func TestSaveTaskRejectsUnsupportedSubmoduleRewriteProtocol(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	_, err = svc.SaveTask(context.Background(), domain.SyncTask{
		Name:                     "bad-submodule-protocol",
		SourceRepoURL:            "git@example.com:org/repo.git",
		TargetRepoURL:            "https://target.example.com/org/repo.git",
		Enabled:                  true,
		SyncAllRefs:              true,
		SubmoduleRewriteProtocol: domain.SubmoduleRewriteProtocol("ftp"),
		ProviderConfig:           domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err == nil {
		t.Fatal("expected save task to fail")
	}
	if !strings.Contains(err.Error(), "unsupported submoduleRewriteProtocol") {
		t.Fatalf("expected protocol validation error, got %v", err)
	}
}

func TestSaveTaskRejectsSSHGitTargetWithoutBaseAPIURL(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	_, err = svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:      domain.TaskTypeSVNImport,
		Name:          "svn-ssh-target-without-api",
		SourceRepoURL: "svn://svn.example.com/repos/project",
		TargetRepoURL: "ssh://git.example.com:2222/mirror/demo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		SVNConfig: domain.SVNConfig{
			TrunkPath: ".",
		},
		ProviderConfig: domain.ProviderConfig{
			Provider:   domain.ProviderGogs,
			Visibility: domain.VisibilityPrivate,
		},
	})
	if err == nil {
		t.Fatal("expected save task to fail")
	}
	if !strings.Contains(err.Error(), "baseApiUrl is required") {
		t.Fatalf("expected explicit base api url error, got %v", err)
	}
}

func TestAdjustSubmoduleSourceURLWithSSHCredential(t *testing.T) {
	credential := &domain.Credential{Type: domain.CredentialTypeSSHKey, Username: "wangli"}
	got := adjustSubmoduleSourceURL(
		"ssh://wh@example.com:3003/org/main.git",
		"https://example.com:3002/org/sub.git",
		credential,
	)
	want := "ssh://wh@example.com:3003/org/sub.git"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestAdjustSubmoduleSourceURLWithHTTPTokenCredential(t *testing.T) {
	credential := &domain.Credential{Type: domain.CredentialTypeHTTPSToken}
	got := adjustSubmoduleSourceURL(
		"https://example.com/org/main.git",
		"git@example.com:org/sub.git",
		credential,
	)
	want := "https://example.com/org/sub.git"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestAdjustSubmoduleSourceURLResolvesRelativePath(t *testing.T) {
	credential := &domain.Credential{Type: domain.CredentialTypeHTTPSToken}
	got := adjustSubmoduleSourceURL(
		"https://example.com/group/main.git",
		"../libs/sub.git",
		credential,
	)
	want := "https://example.com/libs/sub.git"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestAdjustSubmoduleSourceURLKeepsURLWithoutCredential(t *testing.T) {
	got := adjustSubmoduleSourceURL(
		"https://example.com/group/main.git",
		"git@example.com:group/sub.git",
		nil,
	)
	want := "git@example.com:group/sub.git"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestSaveTaskRejectsSSHCredentialForHTTPSource(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	credential, err := svc.SaveCredential(context.Background(), domain.Credential{
		Name:     "ssh",
		Type:     domain.CredentialTypeSSHKey,
		Secret:   "dummy-private-key",
		Username: "git",
	})
	if err != nil {
		t.Fatalf("save credential: %v", err)
	}

	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		Name:                "http-with-ssh",
		SourceRepoURL:       "http://example.com/org/repo.git",
		TargetRepoURL:       "http://target.example.com/org/repo.git",
		SourceCredentialID:  &credential.ID,
		Enabled:             true,
		RecursiveSubmodules: false,
		SyncAllRefs:         true,
		ProviderConfig:      domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err == nil {
		t.Fatalf("expected save task to fail, got task id %d", task.ID)
	}
	if !strings.Contains(err.Error(), "sourceRepoUrl uses http/https") {
		t.Fatalf("expected compatibility error, got %v", err)
	}
}

func TestSaveTaskAllowsSSHCredentialForSSHSource(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	credential, err := svc.SaveCredential(context.Background(), domain.Credential{
		Name:     "ssh",
		Type:     domain.CredentialTypeSSHKey,
		Secret:   "dummy-private-key",
		Username: "git",
	})
	if err != nil {
		t.Fatalf("save credential: %v", err)
	}

	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		Name:                "ssh-with-ssh",
		SourceRepoURL:       "git@example.com:org/repo.git",
		TargetRepoURL:       "http://target.example.com/org/repo.git",
		SourceCredentialID:  &credential.ID,
		Enabled:             true,
		RecursiveSubmodules: false,
		SyncAllRefs:         true,
		ProviderConfig:      domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.ID == 0 {
		t.Fatalf("expected saved task id, got %d", task.ID)
	}
}

func TestSaveTaskRejectsWebhookForSVNImport(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	_, err = svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:      domain.TaskTypeSVNImport,
		Name:          "svn-with-webhook",
		SourceRepoURL: "https://svn.example.com/repos/project",
		TargetRepoURL: "https://target.example.com/org/repo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		TriggerConfig: domain.TriggerConfig{
			EnableWebhook: true,
		},
		ProviderConfig: domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err == nil {
		t.Fatal("expected save task to fail for svn webhook")
	}
	if !strings.Contains(err.Error(), "does not support webhook") {
		t.Fatalf("expected webhook validation error, got %v", err)
	}
}

func TestSaveTaskRejectsSVNImportCredentialWithoutUsernameAndPassword(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	credential, err := svc.SaveCredential(context.Background(), domain.Credential{
		Name:   "svn-http",
		Type:   domain.CredentialTypeHTTPSToken,
		Secret: "",
	})
	if err != nil {
		t.Fatalf("save credential: %v", err)
	}

	_, err = svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:           domain.TaskTypeSVNImport,
		Name:               "svn-http-auth",
		SourceRepoURL:      "https://svn.example.com/repos/project",
		TargetRepoURL:      "https://target.example.com/org/repo.git",
		SourceCredentialID: &credential.ID,
		Enabled:            true,
		SyncAllRefs:        true,
		ProviderConfig:     domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err == nil {
		t.Fatal("expected save task to fail for incomplete svn credentials")
	}
	if !strings.Contains(err.Error(), "username and password") {
		t.Fatalf("expected explicit svn auth validation error, got %v", err)
	}
}

func TestSaveTaskDefaultsSVNLayoutPaths(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:       domain.TaskTypeSVNImport,
		Name:           "svn-import",
		SourceRepoURL:  "https://svn.example.com/repos/project",
		TargetRepoURL:  "https://target.example.com/org/repo.git",
		Enabled:        true,
		SyncAllRefs:    true,
		ProviderConfig: domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.SVNConfig.TrunkPath != "trunk" || task.SVNConfig.BranchesPath != "branches" || task.SVNConfig.TagsPath != "tags" {
		t.Fatalf("expected default svn layout to be applied, got %+v", task.SVNConfig)
	}
	if task.SVNConfig.AuthorDomain != "svn.example.com" {
		t.Fatalf("expected default svn author domain from source host, got %q", task.SVNConfig.AuthorDomain)
	}
}

func TestSaveTaskAllowsSVNProtocolSourceURL(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:       domain.TaskTypeSVNImport,
		Name:           "svn-protocol-import",
		SourceRepoURL:  "svn://svn.example.com/repos/project",
		TargetRepoURL:  "https://target.example.com/org/repo.git",
		Enabled:        true,
		SyncAllRefs:    true,
		ProviderConfig: domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.ID == 0 {
		t.Fatalf("expected saved task id, got %d", task.ID)
	}
	if task.SVNConfig.AuthorDomain != "svn.example.com" {
		t.Fatalf("expected default svn author domain from source host, got %q", task.SVNConfig.AuthorDomain)
	}
}

func TestSaveTaskPreservesSingleDirectorySVNLayout(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:      domain.TaskTypeSVNImport,
		Name:          "svn-root-layout",
		SourceRepoURL: "svn://svn.example.com/repos/project",
		TargetRepoURL: "https://target.example.com/org/repo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		SVNConfig: domain.SVNConfig{
			TrunkPath:    ".",
			BranchesPath: "",
			TagsPath:     "",
		},
		ProviderConfig: domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.SVNConfig.TrunkPath != "." || task.SVNConfig.BranchesPath != "" || task.SVNConfig.TagsPath != "" {
		t.Fatalf("expected single-directory svn layout to be preserved, got %+v", task.SVNConfig)
	}
}

func TestSaveTaskRejectsInvalidSVNStartRevision(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	_, err = svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:      domain.TaskTypeSVNImport,
		Name:          "svn-invalid-revision",
		SourceRepoURL: "svn://svn.example.com/repos/project",
		TargetRepoURL: "https://target.example.com/org/repo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		SVNConfig: domain.SVNConfig{
			TrunkPath:     ".",
			StartRevision: "abc",
		},
		ProviderConfig: domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err == nil {
		t.Fatal("expected save task to fail for invalid start revision")
	}
	if !strings.Contains(err.Error(), "startRevision must be a positive integer") {
		t.Fatalf("expected start revision validation error, got %v", err)
	}
}

func TestSaveTaskPreservesSVNStartRevision(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:      domain.TaskTypeSVNImport,
		Name:          "svn-start-revision",
		SourceRepoURL: "svn://svn.example.com/repos/project",
		TargetRepoURL: "https://target.example.com/org/repo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		SVNConfig: domain.SVNConfig{
			TrunkPath:     ".",
			StartRevision: "120000",
		},
		ProviderConfig: domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}
	if task.SVNConfig.StartRevision != "120000" {
		t.Fatalf("expected start revision to round trip, got %q", task.SVNConfig.StartRevision)
	}
}

func TestBuildCacheKeyIgnoresSVNTargetRepoURL(t *testing.T) {
	root := t.TempDir()
	_ = root
	left := buildCacheKey(domain.TaskTypeSVNImport, "svn://svn.example.com/repos/project", "https://target.example.com/org/repo.git", domain.SVNConfig{
		TrunkPath:     ".",
		StartRevision: "120000",
		AuthorDomain:  "@example.com",
	})
	right := buildCacheKey(domain.TaskTypeSVNImport, "svn://svn.example.com/repos/project", "ssh://git@example.com:2222/org/repo.git", domain.SVNConfig{
		TrunkPath:     ".",
		StartRevision: "120000",
		AuthorDomain:  "@example.com",
	})
	if left != right {
		t.Fatalf("expected svn cache key to ignore target repo url")
	}
}

func TestSaveTaskReconcilesSVNRootCacheKeyWhenIdentityStaysSame(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	ctx := context.Background()
	task, err := svc.SaveTask(ctx, domain.SyncTask{
		TaskType:      domain.TaskTypeSVNImport,
		Name:          "svn-root-cache-reconcile",
		SourceRepoURL: "svn://svn.example.com/repos/project",
		TargetRepoURL: "https://git.example.com/mirror/demo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		SVNConfig: domain.SVNConfig{
			TrunkPath:     ".",
			StartRevision: "158000",
		},
		ProviderConfig: domain.ProviderConfig{
			Provider:   domain.ProviderGogs,
			Visibility: domain.VisibilityPrivate,
			BaseAPIURL: "http://git.example.com:3000/api/v1",
		},
	})
	if err != nil {
		t.Fatalf("save initial task: %v", err)
	}

	legacyCacheKey := "legacy-cache-key"
	if err := db.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      legacyCacheKey,
		SourceRepoURL: task.SourceRepoURL,
		AuthContext:   "managed",
		CachePath:     filepath.Join(root, "cache", legacyCacheKey),
		HealthStatus:  "ready",
	}); err != nil {
		t.Fatalf("upsert cache: %v", err)
	}
	if err := db.LinkCacheToTask(ctx, legacyCacheKey, task.ID); err != nil {
		t.Fatalf("link cache: %v", err)
	}

	task.TargetRepoURL = "ssh://git.example.com:2222/mirror/demo.git"
	if _, err := svc.SaveTask(ctx, task); err != nil {
		t.Fatalf("save task again: %v", err)
	}

	linkedCacheKeys, err := db.ListCacheKeysForTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list cache keys: %v", err)
	}
	currentCacheKey := buildCacheKey(task.TaskType, task.SourceRepoURL, task.TargetRepoURL, task.SVNConfig)
	foundCurrent := false
	for _, key := range linkedCacheKeys {
		if key == currentCacheKey {
			foundCurrent = true
			break
		}
	}
	if !foundCurrent {
		t.Fatalf("expected current cache key %q to be linked, got %+v", currentCacheKey, linkedCacheKeys)
	}
	cache, err := db.GetCacheByKey(ctx, currentCacheKey)
	if err != nil {
		t.Fatalf("get reconciled cache: %v", err)
	}
	if !strings.Contains(cache.CachePath, legacyCacheKey) {
		t.Fatalf("expected reconciled cache to reuse existing path, got %q", cache.CachePath)
	}
}

func TestSaveTaskReconcilesGitMirrorRootCacheKeyWhenIdentityStaysSame(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "reposync.db")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, filepath.Join(root, "cache"), gitclient.NewClient("git"), scm.NewManager())
	ctx := context.Background()
	task, err := svc.SaveTask(ctx, domain.SyncTask{
		TaskType:      domain.TaskTypeGitMirror,
		Name:          "git-root-cache-reconcile",
		SourceRepoURL: "https://git.example.com/org/repo.git",
		TargetRepoURL: "https://target.example.com/mirror/repo.git",
		Enabled:       true,
		SyncAllRefs:   true,
		ProviderConfig: domain.ProviderConfig{
			Provider:   domain.ProviderGogs,
			Visibility: domain.VisibilityPrivate,
			BaseAPIURL: "http://target.example.com:3000/api/v1",
		},
	})
	if err != nil {
		t.Fatalf("save initial task: %v", err)
	}

	legacyCacheKey := "legacy-git-cache-key"
	if err := db.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      legacyCacheKey,
		SourceRepoURL: task.SourceRepoURL,
		AuthContext:   "managed",
		CachePath:     filepath.Join(root, "cache", legacyCacheKey),
		HealthStatus:  "ready",
	}); err != nil {
		t.Fatalf("upsert cache: %v", err)
	}
	if err := db.LinkCacheToTask(ctx, legacyCacheKey, task.ID); err != nil {
		t.Fatalf("link cache: %v", err)
	}

	task.SourceRepoURL = "git@git.example.com:org/repo.git"
	task.TargetRepoURL = "ssh://git@target.example.com:2222/mirror/repo.git"
	if _, err := svc.SaveTask(ctx, task); err != nil {
		t.Fatalf("save task again: %v", err)
	}

	currentCacheKey := buildCacheKey(task.TaskType, task.SourceRepoURL, task.TargetRepoURL, task.SVNConfig)
	cache, err := db.GetCacheByKey(ctx, currentCacheKey)
	if err != nil {
		t.Fatalf("get reconciled cache: %v", err)
	}
	if !strings.Contains(cache.CachePath, legacyCacheKey) {
		t.Fatalf("expected reconciled cache to reuse existing path, got %q", cache.CachePath)
	}
}

func TestRunTaskMirrorsAllRefsAndReusesCache(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for integration test")
	}

	root := t.TempDir()
	sourceBare := filepath.Join(root, "source.git")
	targetBare := filepath.Join(root, "target.git")
	worktree := filepath.Join(root, "work")
	dbPath := filepath.Join(root, "reposync.db")
	cacheDir := filepath.Join(root, "cache")
	customCacheDir := filepath.Join(root, "custom-cache")

	runGit(t, "", "init", "--bare", sourceBare)
	runGit(t, "", "init", "--bare", targetBare)
	runGit(t, "", "clone", sourceBare, worktree)
	runGit(t, worktree, "config", "user.name", "RepoSync Test")
	runGit(t, worktree, "config", "user.email", "reposync@example.com")
	runGit(t, worktree, "checkout", "-b", "main")

	writeFile(t, filepath.Join(worktree, "README.md"), "main\n")
	runGit(t, worktree, "add", ".")
	runGit(t, worktree, "commit", "-m", "init")
	runGit(t, worktree, "push", "-u", "origin", "main")
	runGit(t, worktree, "tag", "v1.0.0")
	runGit(t, worktree, "push", "origin", "v1.0.0")

	runGit(t, worktree, "checkout", "-b", "feature/mirror")
	writeFile(t, filepath.Join(worktree, "feature.txt"), "feature\n")
	runGit(t, worktree, "add", ".")
	runGit(t, worktree, "commit", "-m", "feature")
	runGit(t, worktree, "push", "-u", "origin", "feature/mirror")
	runGit(t, worktree, "checkout", "main")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, cacheDir, gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		Name:                "mirror-all-refs",
		SourceRepoURL:       sourceBare,
		TargetRepoURL:       targetBare,
		CacheBasePath:       customCacheDir,
		Enabled:             true,
		RecursiveSubmodules: false,
		SyncAllRefs:         true,
		ProviderConfig:      domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}

	first, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	first = waitForExecutionCompletion(t, svc, first.ID)
	if first.Status != domain.ExecutionStatusSuccess {
		t.Fatalf("expected first execution success, got %s", first.Status)
	}

	assertGitRef(t, targetBare, "refs/heads/main")
	assertGitRef(t, targetBare, "refs/heads/feature/mirror")
	assertGitRef(t, targetBare, "refs/tags/v1.0.0")

	second, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	second = waitForExecutionCompletion(t, svc, second.ID)
	if second.Status != domain.ExecutionStatusSuccess {
		t.Fatalf("expected second execution success, got %s", second.Status)
	}

	caches, err := svc.ListCaches(context.Background())
	if err != nil {
		t.Fatalf("list caches: %v", err)
	}
	if len(caches) != 1 {
		t.Fatalf("expected 1 cache, got %d", len(caches))
	}
	links, err := db.ListCacheTaskIDs(context.Background(), caches[0].CacheKey)
	if err != nil {
		t.Fatalf("list cache links: %v", err)
	}
	if len(links) != 1 || links[0] != task.ID {
		t.Fatalf("expected cache to link to task %d, got %+v", task.ID, links)
	}
	if caches[0].HitCount < 2 {
		t.Fatalf("expected cache hit count >= 2, got %d", caches[0].HitCount)
	}
	if caches[0].SizeBytes <= 0 {
		t.Fatalf("expected cache size bytes to be greater than 0, got %d", caches[0].SizeBytes)
	}
	if !strings.HasPrefix(caches[0].CachePath, customCacheDir) {
		t.Fatalf("expected cache path under custom cache dir %s, got %s", customCacheDir, caches[0].CachePath)
	}

	detail, err := svc.ExecutionDetail(context.Background(), second.ID)
	if err != nil {
		t.Fatalf("execution detail: %v", err)
	}
	if len(detail.Nodes) != 1 {
		t.Fatalf("expected 1 execution node, got %d", len(detail.Nodes))
	}
	if !detail.Nodes[0].CacheHit {
		t.Fatalf("expected second run to hit cache")
	}
	logText := executionLogText(t, svc, second.ID)
	if !strings.Contains(logText, "Refreshing mirror cache") {
		t.Fatalf("expected execution logs to include progress output, got:\n%s", logText)
	}
	if !strings.Contains(logText, "git exec: fetch --progress --prune origin +refs/*:refs/*") {
		t.Fatalf("expected execution logs to include raw git command output, got:\n%s", logText)
	}
}

func TestMoveCacheReusesMigratedPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for integration test")
	}

	root := t.TempDir()
	sourceBare := filepath.Join(root, "source.git")
	targetBare := filepath.Join(root, "target.git")
	worktree := filepath.Join(root, "work")
	dbPath := filepath.Join(root, "reposync.db")
	cacheDir := filepath.Join(root, "cache")

	runGit(t, "", "init", "--bare", sourceBare)
	runGit(t, "", "init", "--bare", targetBare)
	runGit(t, "", "clone", sourceBare, worktree)
	runGit(t, worktree, "config", "user.name", "RepoSync Test")
	runGit(t, worktree, "config", "user.email", "reposync@example.com")
	runGit(t, worktree, "checkout", "-b", "main")
	writeFile(t, filepath.Join(worktree, "README.md"), "main\n")
	runGit(t, worktree, "add", ".")
	runGit(t, worktree, "commit", "-m", "init")
	runGit(t, worktree, "push", "-u", "origin", "main")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, cacheDir, gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		Name:                "move-cache",
		SourceRepoURL:       sourceBare,
		TargetRepoURL:       targetBare,
		Enabled:             true,
		RecursiveSubmodules: false,
		SyncAllRefs:         true,
		ProviderConfig:      domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}

	first, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	first = waitForExecutionCompletion(t, svc, first.ID)
	if first.Status != domain.ExecutionStatusSuccess {
		t.Fatalf("expected first execution success, got %s", first.Status)
	}

	caches, err := svc.ListCaches(context.Background())
	if err != nil {
		t.Fatalf("list caches: %v", err)
	}
	if len(caches) != 1 {
		t.Fatalf("expected 1 cache, got %d", len(caches))
	}
	oldPath := caches[0].CachePath
	newPath := filepath.Join(root, "migrated-cache", filepath.Base(oldPath))

	moved, err := svc.MoveCache(context.Background(), caches[0].ID, newPath)
	if err != nil {
		t.Fatalf("move cache: %v", err)
	}
	if moved.CachePath != newPath {
		t.Fatalf("expected moved cache path %s, got %s", newPath, moved.CachePath)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected migrated cache at %s: %v", newPath, err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old cache path to be gone, got err=%v", err)
	}

	second, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	second = waitForExecutionCompletion(t, svc, second.ID)
	if second.Status != domain.ExecutionStatusSuccess {
		t.Fatalf("expected second execution success, got %s", second.Status)
	}

	caches, err = svc.ListCaches(context.Background())
	if err != nil {
		t.Fatalf("list caches after move: %v", err)
	}
	if caches[0].CachePath != newPath {
		t.Fatalf("expected cache path to remain migrated path %s, got %s", newPath, caches[0].CachePath)
	}
	if caches[0].HitCount < 2 {
		t.Fatalf("expected migrated cache hit count >= 2, got %d", caches[0].HitCount)
	}
}

func TestRunTaskSubscriptionReceivesRealtimeLogUpdates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for integration test")
	}

	root := t.TempDir()
	sourceBare := filepath.Join(root, "source.git")
	targetBare := filepath.Join(root, "target.git")
	worktree := filepath.Join(root, "work")
	dbPath := filepath.Join(root, "reposync.db")
	cacheDir := filepath.Join(root, "cache")

	runGit(t, "", "init", "--bare", sourceBare)
	runGit(t, "", "init", "--bare", targetBare)
	runGit(t, "", "clone", sourceBare, worktree)
	runGit(t, worktree, "config", "user.name", "RepoSync Test")
	runGit(t, worktree, "config", "user.email", "reposync@example.com")
	runGit(t, worktree, "checkout", "-b", "main")
	writeFile(t, filepath.Join(worktree, "README.md"), "main\n")
	runGit(t, worktree, "add", ".")
	runGit(t, worktree, "commit", "-m", "init")
	runGit(t, worktree, "push", "-u", "origin", "main")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, cacheDir, gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		Name:                "subscription-updates",
		SourceRepoURL:       sourceBare,
		TargetRepoURL:       targetBare,
		Enabled:             true,
		RecursiveSubmodules: false,
		SyncAllRefs:         true,
		ProviderConfig:      domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}

	execution, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("run task: %v", err)
	}

	initial, updates, cancel, err := svc.SubscribeExecution(context.Background(), execution.ID)
	if err != nil {
		t.Fatalf("subscribe execution: %v", err)
	}
	defer cancel()

	if initial.Execution.LogCount == 0 || initial.Execution.LastLogID == 0 {
		t.Fatalf("expected initial execution metadata to expose logs, got count=%d last=%d", initial.Execution.LogCount, initial.Execution.LastLogID)
	}

	deadline := time.Now().Add(10 * time.Second)
	sawRealtimeUpdate := false
	for time.Now().Before(deadline) && !sawRealtimeUpdate {
		select {
		case detail, ok := <-updates:
			if !ok {
				t.Fatal("execution updates channel closed before realtime update arrived")
			}
			if detail.Execution.LastLogID > initial.Execution.LastLogID || detail.Execution.LogCount > initial.Execution.LogCount {
				sawRealtimeUpdate = true
			}
		case <-time.After(200 * time.Millisecond):
		}
	}

	if !sawRealtimeUpdate {
		t.Fatal("expected subscription to receive realtime execution log updates")
	}

	waitForExecutionCompletion(t, svc, execution.ID)
}

func TestRunTaskRecursivelyMirrorsSubmodules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for integration test")
	}

	root := t.TempDir()
	sourceReposDir := filepath.Join(root, "sources")
	targetReposDir := filepath.Join(root, "targets")
	subSourceBare := filepath.Join(sourceReposDir, "sub-source.git")
	subTargetBare := filepath.Join(targetReposDir, "sub-source.git")
	mainSourceBare := filepath.Join(root, "main-source.git")
	mainTargetBare := filepath.Join(targetReposDir, "target-main.git")
	subWorktree := filepath.Join(root, "sub-work")
	mainWorktree := filepath.Join(root, "main-work")
	dbPath := filepath.Join(root, "reposync.db")
	cacheDir := filepath.Join(root, "cache")

	if err := os.MkdirAll(sourceReposDir, 0o755); err != nil {
		t.Fatalf("mkdir source repos dir: %v", err)
	}
	if err := os.MkdirAll(targetReposDir, 0o755); err != nil {
		t.Fatalf("mkdir target repos dir: %v", err)
	}

	runGit(t, "", "init", "--bare", subSourceBare)
	runGit(t, "", "clone", subSourceBare, subWorktree)
	runGit(t, subWorktree, "config", "user.name", "RepoSync Test")
	runGit(t, subWorktree, "config", "user.email", "reposync@example.com")
	writeFile(t, filepath.Join(subWorktree, "sub.txt"), "submodule\n")
	runGit(t, subWorktree, "add", ".")
	runGit(t, subWorktree, "commit", "-m", "submodule init")
	runGit(t, subWorktree, "push", "-u", "origin", "master")

	runGit(t, "", "init", "--bare", mainSourceBare)
	runGit(t, "", "clone", mainSourceBare, mainWorktree)
	runGit(t, mainWorktree, "config", "user.name", "RepoSync Test")
	runGit(t, mainWorktree, "config", "user.email", "reposync@example.com")
	writeFile(t, filepath.Join(mainWorktree, "README.md"), "main\n")
	runGit(t, mainWorktree, "add", ".")
	runGit(t, mainWorktree, "commit", "-m", "main init")
	runGit(t, mainWorktree, "push", "-u", "origin", "master")
	runGitWithEnv(t, mainWorktree, []string{"GIT_ALLOW_PROTOCOL=file"}, "submodule", "add", subSourceBare, "libs/core")
	runGit(t, mainWorktree, "commit", "-am", "add submodule")
	runGit(t, mainWorktree, "push", "origin", "master")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, cacheDir, gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		Name:                "recursive-submodules",
		SourceRepoURL:       mainSourceBare,
		TargetRepoURL:       mainTargetBare,
		Enabled:             true,
		RecursiveSubmodules: true,
		SyncAllRefs:         true,
		ProviderConfig:      domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}

	execution, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("recursive run failed: %v", err)
	}
	execution = waitForExecutionCompletion(t, svc, execution.ID)
	if execution.Status != domain.ExecutionStatusSuccess {
		t.Fatalf("expected success, got %s", execution.Status)
	}
	if execution.RepoCount != 2 {
		t.Fatalf("expected 2 mirrored repositories, got %d", execution.RepoCount)
	}
	if execution.CreatedRepoCount != 2 {
		t.Fatalf("expected 2 auto-created target repositories, got %d", execution.CreatedRepoCount)
	}

	assertGitRef(t, mainTargetBare, "refs/heads/master")
	assertGitRef(t, subTargetBare, "refs/heads/master")
	firstTargetHead := gitRevParse(t, mainTargetBare, "refs/heads/master")

	cloneDir := filepath.Join(root, "clone-target")
	runGitWithEnv(t, "", []string{"GIT_ALLOW_PROTOCOL=file"}, "clone", "--recurse-submodules", mainTargetBare, cloneDir)
	gitmodulesContent, err := os.ReadFile(filepath.Join(cloneDir, ".gitmodules"))
	if err != nil {
		t.Fatalf("read cloned .gitmodules: %v", err)
	}
	if !strings.Contains(string(gitmodulesContent), filepath.ToSlash(subTargetBare)) {
		t.Fatalf("expected cloned .gitmodules to point to mirrored submodule target, got:\n%s", string(gitmodulesContent))
	}
	if _, err := os.Stat(filepath.Join(cloneDir, "libs", "core", "sub.txt")); err != nil {
		t.Fatalf("expected submodule content to be available after recursive clone: %v", err)
	}

	detail, err := svc.ExecutionDetail(context.Background(), execution.ID)
	if err != nil {
		t.Fatalf("execution detail: %v", err)
	}
	if len(detail.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(detail.Nodes))
	}

	secondExecution, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("second recursive run failed: %v", err)
	}
	secondExecution = waitForExecutionCompletion(t, svc, secondExecution.ID)
	if secondExecution.Status != domain.ExecutionStatusSuccess {
		t.Fatalf("expected second recursive run success, got %s", secondExecution.Status)
	}
	secondTargetHead := gitRevParse(t, mainTargetBare, "refs/heads/master")
	if secondTargetHead != firstTargetHead {
		t.Fatalf("expected rewritten target branch head to remain stable, got %s then %s", firstTargetHead, secondTargetHead)
	}
}

func TestRunTaskRewritesGitlinkToMirroredSubmoduleCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for integration test")
	}

	root := t.TempDir()
	sourceReposDir := filepath.Join(root, "sources")
	targetReposDir := filepath.Join(root, "targets")
	leafSourceBare := filepath.Join(sourceReposDir, "leaf-source.git")
	leafTargetBare := filepath.Join(targetReposDir, "leaf-source.git")
	childSourceBare := filepath.Join(sourceReposDir, "child-source.git")
	childTargetBare := filepath.Join(targetReposDir, "child-source.git")
	mainSourceBare := filepath.Join(root, "main-source.git")
	mainTargetBare := filepath.Join(targetReposDir, "target-main.git")
	leafWorktree := filepath.Join(root, "leaf-work")
	childWorktree := filepath.Join(root, "child-work")
	mainWorktree := filepath.Join(root, "main-work")
	dbPath := filepath.Join(root, "reposync.db")
	cacheDir := filepath.Join(root, "cache")

	if err := os.MkdirAll(sourceReposDir, 0o755); err != nil {
		t.Fatalf("mkdir source repos dir: %v", err)
	}
	if err := os.MkdirAll(targetReposDir, 0o755); err != nil {
		t.Fatalf("mkdir target repos dir: %v", err)
	}

	runGit(t, "", "init", "--bare", leafSourceBare)
	runGit(t, "", "clone", leafSourceBare, leafWorktree)
	runGit(t, leafWorktree, "config", "user.name", "RepoSync Test")
	runGit(t, leafWorktree, "config", "user.email", "reposync@example.com")
	writeFile(t, filepath.Join(leafWorktree, "leaf.txt"), "leaf\n")
	runGit(t, leafWorktree, "add", ".")
	runGit(t, leafWorktree, "commit", "-m", "leaf init")
	runGit(t, leafWorktree, "push", "-u", "origin", "master")

	runGit(t, "", "init", "--bare", childSourceBare)
	runGit(t, "", "clone", childSourceBare, childWorktree)
	runGit(t, childWorktree, "config", "user.name", "RepoSync Test")
	runGit(t, childWorktree, "config", "user.email", "reposync@example.com")
	writeFile(t, filepath.Join(childWorktree, "child.txt"), "child\n")
	runGit(t, childWorktree, "add", ".")
	runGit(t, childWorktree, "commit", "-m", "child init")
	runGit(t, childWorktree, "push", "-u", "origin", "master")
	runGitWithEnv(t, childWorktree, []string{"GIT_ALLOW_PROTOCOL=file"}, "submodule", "add", leafSourceBare, "deps/leaf")
	runGit(t, childWorktree, "commit", "-am", "add leaf submodule")
	runGit(t, childWorktree, "push", "origin", "master")
	sourceChildCommit := gitRevParse(t, childSourceBare, "refs/heads/master")

	runGit(t, "", "init", "--bare", mainSourceBare)
	runGit(t, "", "clone", mainSourceBare, mainWorktree)
	runGit(t, mainWorktree, "config", "user.name", "RepoSync Test")
	runGit(t, mainWorktree, "config", "user.email", "reposync@example.com")
	writeFile(t, filepath.Join(mainWorktree, "README.md"), "main\n")
	runGit(t, mainWorktree, "add", ".")
	runGit(t, mainWorktree, "commit", "-m", "main init")
	runGit(t, mainWorktree, "push", "-u", "origin", "master")
	runGitWithEnv(t, mainWorktree, []string{"GIT_ALLOW_PROTOCOL=file"}, "submodule", "add", childSourceBare, "child")
	runGit(t, mainWorktree, "commit", "-am", "add child submodule")
	runGit(t, mainWorktree, "push", "origin", "master")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, cacheDir, gitclient.NewClient("git"), scm.NewManager())
	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		Name:                "rewrite-gitlink",
		SourceRepoURL:       mainSourceBare,
		TargetRepoURL:       mainTargetBare,
		Enabled:             true,
		RecursiveSubmodules: true,
		SyncAllRefs:         true,
		ProviderConfig:      domain.ProviderConfig{Provider: domain.ProviderGitHub, Visibility: domain.VisibilityPrivate},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}

	execution, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	execution = waitForExecutionCompletion(t, svc, execution.ID)
	if execution.Status != domain.ExecutionStatusSuccess {
		t.Fatalf("expected success, got %s", execution.Status)
	}

	mainTargetCommit := gitRevParse(t, mainTargetBare, "refs/heads/master")
	childTargetCommit := gitRevParse(t, childTargetBare, "refs/heads/master")
	if childTargetCommit == sourceChildCommit {
		t.Fatalf("expected child target commit to differ from source commit after nested rewrite")
	}

	mainTreeEntry := runGit(t, "", "--git-dir", mainTargetBare, "ls-tree", mainTargetCommit, "child")
	if !strings.Contains(mainTreeEntry, childTargetCommit) {
		t.Fatalf("expected main target gitlink to point to mirrored child commit %s, got %s", childTargetCommit, mainTreeEntry)
	}

	cloneDir := filepath.Join(root, "clone-main-target")
	runGitWithEnv(t, "", []string{"GIT_ALLOW_PROTOCOL=file"}, "clone", "--recurse-submodules", mainTargetBare, cloneDir)

	childGitmodulesContent, err := os.ReadFile(filepath.Join(cloneDir, "child", ".gitmodules"))
	if err != nil {
		t.Fatalf("read cloned child .gitmodules: %v", err)
	}
	if !strings.Contains(string(childGitmodulesContent), filepath.ToSlash(leafTargetBare)) {
		t.Fatalf("expected nested child .gitmodules to point to mirrored leaf target, got:\n%s", string(childGitmodulesContent))
	}
	if _, err := os.Stat(filepath.Join(cloneDir, "child", "deps", "leaf", "leaf.txt")); err != nil {
		t.Fatalf("expected nested submodule content to be available after recursive clone: %v", err)
	}
}

func TestRunTaskImportsSVNRepositoryEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for integration test")
	}

	sourceURL := strings.TrimSpace(os.Getenv("REPOSYNC_E2E_SVN_URL"))
	if sourceURL == "" {
		t.Skip("set REPOSYNC_E2E_SVN_URL to run the real svn_import end-to-end regression")
	}

	root := t.TempDir()
	targetBare := filepath.Join(root, "target.git")
	dbPath := filepath.Join(root, "reposync.db")
	cacheDir := filepath.Join(root, "cache")

	box := security.NewSecretBox("test-secret")
	db, err := store.New(dbPath, box)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer db.Close()

	svc := New(db, cacheDir, gitclient.NewClient("git"), scm.NewManager())

	var sourceCredentialID *int64
	sourceUsername := strings.TrimSpace(os.Getenv("REPOSYNC_E2E_SVN_USERNAME"))
	sourcePassword := strings.TrimSpace(os.Getenv("REPOSYNC_E2E_SVN_PASSWORD"))
	if sourceUsername != "" || sourcePassword != "" {
		credential, err := svc.SaveCredential(context.Background(), domain.Credential{
			Name:     "svn-e2e-source",
			Type:     domain.CredentialTypeHTTPSToken,
			Username: sourceUsername,
			Secret:   sourcePassword,
		})
		if err != nil {
			t.Fatalf("save source credential: %v", err)
		}
		sourceCredentialID = &credential.ID
	}

	task, err := svc.SaveTask(context.Background(), domain.SyncTask{
		TaskType:           domain.TaskTypeSVNImport,
		Name:               "svn-e2e",
		SourceRepoURL:      sourceURL,
		TargetRepoURL:      targetBare,
		SourceCredentialID: sourceCredentialID,
		Enabled:            true,
		SyncAllRefs:        true,
		ProviderConfig: domain.ProviderConfig{
			Provider:   domain.ProviderGitHub,
			Visibility: domain.VisibilityPrivate,
		},
		SVNConfig: domain.SVNConfig{
			AuthorDomain: strings.TrimSpace(os.Getenv("REPOSYNC_E2E_SVN_AUTHOR_DOMAIN")),
		},
	})
	if err != nil {
		t.Fatalf("save task: %v", err)
	}

	execution, err := svc.RunTask(context.Background(), task.ID, domain.TriggerManual)
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	execution = waitForExecutionCompletion(t, svc, execution.ID)
	if execution.Status != domain.ExecutionStatusSuccess {
		_, detailErr := svc.ExecutionDetail(context.Background(), execution.ID)
		if detailErr == nil {
			t.Fatalf("expected success, got %s\nexecution detail loaded", execution.Status)
		}
		t.Fatalf("expected success, got %s", execution.Status)
	}

	expectedBranch := strings.TrimSpace(os.Getenv("REPOSYNC_E2E_SVN_EXPECT_BRANCH"))
	if expectedBranch == "" {
		expectedBranch = "trunk"
	}
	assertGitRef(t, targetBare, "refs/heads/"+expectedBranch)

	if expectedTag := strings.TrimSpace(os.Getenv("REPOSYNC_E2E_SVN_EXPECT_TAG")); expectedTag != "" {
		assertGitRef(t, targetBare, "refs/tags/"+expectedTag)
	}

	detail, err := svc.ExecutionDetail(context.Background(), execution.ID)
	if err != nil {
		t.Fatalf("execution detail: %v", err)
	}
	if detail.Execution.RepoCount != 1 {
		t.Fatalf("expected 1 imported repository, got %d", detail.Execution.RepoCount)
	}
	if len(detail.Nodes) != 1 {
		t.Fatalf("expected 1 execution node, got %d", len(detail.Nodes))
	}
	logText := executionLogText(t, svc, execution.ID)
	if !strings.Contains(logText, "Promoting SVN refs") {
		t.Fatalf("expected execution logs to mention svn ref promotion, got:\n%s", logText)
	}
}

func executionLogText(t *testing.T, svc *Service, executionID int64) string {
	t.Helper()
	logs, err := svc.ListExecutionLogs(context.Background(), executionID, 0, 0)
	if err != nil {
		t.Fatalf("list execution logs: %v", err)
	}
	lines := make([]string, 0, len(logs))
	for _, item := range logs {
		lines = append(lines, item.Message)
	}
	return strings.Join(lines, "\n")
}

func runGit(t *testing.T, dir string, args ...string) string {
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

func runGitWithEnv(t *testing.T, dir string, extraEnv []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func waitForExecutionCompletion(t *testing.T, svc *Service, executionID int64) domain.SyncExecution {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		detail, err := svc.ExecutionDetail(context.Background(), executionID)
		if err != nil {
			t.Fatalf("execution detail %d: %v", executionID, err)
		}
		if detail.Execution.Status != domain.ExecutionStatusRunning {
			return detail.Execution
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("execution %d did not finish before timeout", executionID)
	return domain.SyncExecution{}
}

func assertGitRef(t *testing.T, repo string, ref string) {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", repo, "show-ref", "--verify", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected ref %s in %s: %v\n%s", ref, repo, err, string(out))
	}
}

func gitRevParse(t *testing.T, repo string, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", repo, "rev-parse", rev)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse %s in %s failed: %v\n%s", rev, repo, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

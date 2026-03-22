package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reposync/backend/internal/domain"
	gitclient "reposync/backend/internal/git"
	"reposync/backend/internal/scm"
	"reposync/backend/internal/security"
	"reposync/backend/internal/store"
)

func TestBuildCacheKeyStable(t *testing.T) {
	left := buildCacheKey("source", "target")
	right := buildCacheKey("source", "target")
	if left != right {
		t.Fatalf("expected cache key to be stable")
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
	if caches[0].HitCount < 2 {
		t.Fatalf("expected cache hit count >= 2, got %d", caches[0].HitCount)
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

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func assertGitRef(t *testing.T, repo string, ref string) {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", repo, "show-ref", "--verify", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected ref %s in %s: %v\n%s", ref, repo, err, string(out))
	}
}

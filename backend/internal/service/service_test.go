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

func TestRunTaskRecursivelyMirrorsSubmodules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for integration test")
	}

	root := t.TempDir()
	subSourceBare := filepath.Join(root, "sub-source.git")
	subTargetBare := filepath.Join(root, "target-main-libs-core.git")
	mainSourceBare := filepath.Join(root, "main-source.git")
	mainTargetBare := filepath.Join(root, "target-main.git")
	subWorktree := filepath.Join(root, "sub-work")
	mainWorktree := filepath.Join(root, "main-work")
	dbPath := filepath.Join(root, "reposync.db")
	cacheDir := filepath.Join(root, "cache")

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
	if secondExecution.Status != domain.ExecutionStatusSuccess {
		t.Fatalf("expected second recursive run success, got %s", secondExecution.Status)
	}
	secondTargetHead := gitRevParse(t, mainTargetBare, "refs/heads/master")
	if secondTargetHead != firstTargetHead {
		t.Fatalf("expected rewritten target branch head to remain stable, got %s then %s", firstTargetHead, secondTargetHead)
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

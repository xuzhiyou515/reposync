package git

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reposync/backend/internal/domain"
)

func TestPrepareGitAuthInjectsHTTPSCredentials(t *testing.T) {
	authURL, env, cleanup, err := prepareGitAuth("https://example.com/org/repo.git", &domain.Credential{
		Type:     domain.CredentialTypeHTTPSToken,
		Username: "mirror-bot",
		Secret:   "super-secret",
	})
	if err != nil {
		t.Fatalf("prepare auth: %v", err)
	}
	defer cleanup()

	if len(env) != 0 {
		t.Fatalf("expected https auth to avoid env injection, got %v", env)
	}
	if !strings.Contains(authURL, "mirror-bot:super-secret@") {
		t.Fatalf("expected credentials in auth url, got %s", authURL)
	}
}

func TestPrepareGitAuthInjectsSSHCommand(t *testing.T) {
	authURL, env, cleanup, err := prepareGitAuth("git@example.com:org/repo.git", &domain.Credential{
		Type:   domain.CredentialTypeSSHKey,
		Secret: "-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----",
	})
	if err != nil {
		t.Fatalf("prepare auth: %v", err)
	}
	defer cleanup()

	if authURL != "git@example.com:org/repo.git" {
		t.Fatalf("expected ssh auth to keep original url, got %s", authURL)
	}
	if len(env) != 1 || !strings.HasPrefix(env[0], "GIT_SSH_COMMAND=") {
		t.Fatalf("expected ssh auth env, got %v", env)
	}
}

func TestSanitizeArgsMasksEmbeddedSecrets(t *testing.T) {
	safe := sanitizeArgs([]string{"clone", "https://mirror-bot:super-secret@example.com/org/repo.git"})
	if strings.Contains(safe, "super-secret") {
		t.Fatalf("expected secret to be masked, got %s", safe)
	}
	if !strings.Contains(safe, "%2A%2A%2A") {
		t.Fatalf("expected masked placeholder, got %s", safe)
	}
}

func TestBuildSVNCloneArgsIncludesLayoutAndAuthorsFile(t *testing.T) {
	args := buildSVNCloneArgs("https://svn.example.com/repos/demo", "D:/cache/demo", domain.SVNConfig{
		TrunkPath:       "proj/trunk",
		BranchesPath:    "proj/branches",
		TagsPath:        "proj/tags",
		AuthorsFilePath: "D:/authors.txt",
	}, []string{"--username=svn-user"}, []string{"--authors-file=D:/authors.txt"})
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"svn clone",
		"--prefix=svn/",
		"--trunk=proj/trunk",
		"--branches=proj/branches",
		"--tags=proj/tags",
		"--username=svn-user",
		"--authors-file=D:/authors.txt",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected args to contain %q, got %s", expected, joined)
		}
	}
}

func TestPrepareSVNAuthInjectsUsernameAndPassword(t *testing.T) {
	authURL, env, args, bootstrap, cleanup, err := prepareSVNAuth("https://svn.example.com/repos/demo", &domain.Credential{
		Type:     domain.CredentialTypeHTTPSToken,
		Username: "svn-user",
		Secret:   "svn-pass",
	})
	if err != nil {
		t.Fatalf("prepare svn auth: %v", err)
	}
	defer cleanup()
	if err := bootstrap(context.Background()); err != nil {
		t.Fatalf("expected no bootstrap for https auth, got %v", err)
	}

	if len(env) != 0 {
		t.Fatalf("expected no env overrides, got %v", env)
	}
	if len(args) != 1 || args[0] != "--username=svn-user" {
		t.Fatalf("expected svn username arg, got %v", args)
	}
	if !strings.Contains(authURL, "svn-user:svn-pass@") {
		t.Fatalf("expected password embedded in auth url, got %s", authURL)
	}
}

func TestPrepareSVNAuthSupportsSVNProtocol(t *testing.T) {
	authURL, env, args, bootstrap, cleanup, err := prepareSVNAuth("svn://svn.example.com/repos/demo", &domain.Credential{
		Type:     domain.CredentialTypeHTTPSToken,
		Username: "svn-user",
		Secret:   "svn-pass",
	})
	if err != nil {
		t.Fatalf("prepare svn auth: %v", err)
	}
	defer cleanup()
	if bootstrap == nil {
		t.Fatal("expected bootstrap func for svn protocol auth")
	}

	if len(env) != 0 {
		t.Fatalf("expected no env overrides, got %v", env)
	}
	if len(args) != 2 || args[0] != "--username=svn-user" || !strings.HasPrefix(args[1], "--config-dir=") {
		t.Fatalf("expected svn username arg, got %v", args)
	}
	if authURL != "svn://svn.example.com/repos/demo" {
		t.Fatalf("expected svn url to stay unchanged, got %s", authURL)
	}
}

func TestStreamPipeSplitsCarriageReturnProgressLines(t *testing.T) {
	client := &Client{}
	input := io.NopCloser(strings.NewReader("progress 1\rprogress 2\rfinal line\n"))
	var wg sync.WaitGroup
	var out bytes.Buffer

	wg.Add(1)
	client.streamPipe(&wg, "stderr", input, &out)
	wg.Wait()

	got := strings.TrimSpace(out.String())
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), got)
	}
	if lines[0] != "progress 1" || lines[1] != "progress 2" || lines[2] != "final line" {
		t.Fatalf("unexpected split lines: %#v", lines)
	}
}

func TestIsSVNMetadataLockError(t *testing.T) {
	err := errors.New("error: could not lock config file .git/svn/.metadata: File exists")
	if !isSVNMetadataLockError(err) {
		t.Fatal("expected svn metadata lock error to be detected")
	}
}

func TestIsSVNLastRevConflictError(t *testing.T) {
	err := errors.New("last_rev is higher!: 192800 >= 192800 at C:/Program Files/Git/mingw64/share/perl5/Git/SVN/Ra.pm line 493.")
	if !isSVNLastRevConflictError(err) {
		t.Fatal("expected svn last_rev conflict to be detected")
	}
}

func TestClearSVNMetadataLockRemovesLockFile(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, ".git", "svn", ".metadata.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("lock"), 0o644); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	if err := clearSVNMetadataLock(root); err != nil {
		t.Fatalf("clear lock: %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("expected lock file to be removed, got %v", err)
	}
}

func TestPrepareSVNAuthRejectsMissingUsernameOrPassword(t *testing.T) {
	_, _, _, _, cleanup, err := prepareSVNAuth("https://svn.example.com/repos/demo", &domain.Credential{
		Type:     domain.CredentialTypeHTTPSToken,
		Username: "svn-user",
		Secret:   "",
	})
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil {
		t.Fatal("expected svn auth preparation to fail")
	}
	if !strings.Contains(err.Error(), "username and password") {
		t.Fatalf("expected explicit auth error, got %v", err)
	}
}

func TestPrepareSVNAuthorArgsUsesAuthorsFileWhenProvided(t *testing.T) {
	args, cleanup, err := prepareSVNAuthorArgs(domain.SVNConfig{
		AuthorsFilePath: "D:/authors.txt",
		AuthorDomain:    "svn.example.com",
	})
	if err != nil {
		t.Fatalf("prepare authors args: %v", err)
	}
	defer cleanup()

	if len(args) != 1 || args[0] != "--authors-file=D:/authors.txt" {
		t.Fatalf("expected authors-file arg, got %v", args)
	}
}

func TestPrepareSVNAuthorArgsFallsBackToAuthorsProg(t *testing.T) {
	args, cleanup, err := prepareSVNAuthorArgs(domain.SVNConfig{
		AuthorDomain: "svn.example.com",
	})
	if err != nil {
		t.Fatalf("prepare authors args: %v", err)
	}
	if len(args) != 1 || !strings.HasPrefix(args[0], "--authors-prog=") {
		t.Fatalf("expected authors-prog arg, got %v", args)
	}
	progPath := strings.TrimPrefix(args[0], "--authors-prog=")
	if _, statErr := os.Stat(progPath); statErr != nil {
		t.Fatalf("expected authors prog to exist: %v", statErr)
	}
	cleanup()
	if _, statErr := os.Stat(progPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected authors prog to be removed, got %v", statErr)
	}
}

func TestWrapSVNPushErrorMarksDrift(t *testing.T) {
	err := wrapSVNPushError(errors.New("git push: [rejected] main -> main (non-fast-forward)"))
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	if !strings.Contains(err.Error(), "drift detected") {
		t.Fatalf("expected drift message, got %v", err)
	}
}

func TestWrapSVNPushErrorLeavesOtherErrorsUntouched(t *testing.T) {
	original := errors.New("git push: remote auth failed")
	err := wrapSVNPushError(original)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != original.Error() {
		t.Fatalf("expected original error to pass through, got %v", err)
	}
}

func TestClassifySVNRemoteRef(t *testing.T) {
	config := domain.SVNConfig{
		TrunkPath:    "trunk",
		BranchesPath: "branches",
		TagsPath:     "tags",
	}
	cases := []struct {
		ref  string
		kind string
		name string
	}{
		{ref: "svn/trunk", kind: "branch", name: "trunk"},
		{ref: "svn/branches/release/1.0", kind: "branch", name: "release/1.0"},
		{ref: "svn/tags/v1.0.0", kind: "tag", name: "v1.0.0"},
		{ref: "tags/v2.0.0", kind: "tag", name: "v2.0.0"},
		{ref: "svn/feature-x", kind: "branch", name: "feature-x"},
	}

	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			kind, name := classifySVNRemoteRef(tc.ref, config)
			if kind != tc.kind || name != tc.name {
				t.Fatalf("expected (%s, %s), got (%s, %s)", tc.kind, tc.name, kind, name)
			}
		})
	}
}

func TestPromoteSVNRefsFailsWhenNoRefsMatchConfiguredLayout(t *testing.T) {
	client := NewClient("git")
	root := t.TempDir()

	if _, err := client.run(context.Background(), "", "init", root); err != nil {
		t.Fatalf("init repo: %v", err)
	}
	samplePath := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(samplePath, []byte("sample\n"), 0o644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}
	objectID, err := client.run(context.Background(), root, "hash-object", "-w", samplePath)
	if err != nil {
		t.Fatalf("hash object: %v", err)
	}
	if _, err := client.run(context.Background(), root, "update-ref", "refs/remotes/origin/unknown", strings.TrimSpace(objectID)); err != nil {
		t.Fatalf("update ref: %v", err)
	}

	_, err = client.PromoteSVNRefs(context.Background(), root, domain.SVNConfig{
		TrunkPath:    "trunk",
		BranchesPath: "branches",
		TagsPath:     "tags",
	})
	if err == nil {
		t.Fatal("expected promote refs to fail when nothing matches")
	}
	if !strings.Contains(err.Error(), "produced no Git branches or tags") {
		t.Fatalf("expected explicit no-refs error, got %v", err)
	}
}

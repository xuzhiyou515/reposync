package git

import (
	"errors"
	"os"
	"strings"
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
	}, []string{"--authors-file=D:/authors.txt"})
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"svn clone",
		"--prefix=svn/",
		"--trunk=proj/trunk",
		"--branches=proj/branches",
		"--tags=proj/tags",
		"--authors-file=D:/authors.txt",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected args to contain %q, got %s", expected, joined)
		}
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

package git

import (
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

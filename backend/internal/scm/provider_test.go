package scm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"reposync/backend/internal/domain"
)

func TestGitHubEnsureRepositoryCreatesMissingRepo(t *testing.T) {
	var created bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/org/demo":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/orgs/org/repos":
			created = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	manager := NewManager()
	autoCreated, _, err := manager.EnsureRepository(context.Background(), "https://example.com/org/demo.git", domain.ProviderConfig{
		Provider:   domain.ProviderGitHub,
		Namespace:  "org",
		Visibility: domain.VisibilityPrivate,
		BaseAPIURL: server.URL,
	}, &domain.Credential{Type: domain.CredentialTypeAPIToken, Secret: "token"})
	if err != nil {
		t.Fatalf("ensure repository: %v", err)
	}
	if !autoCreated || !created {
		t.Fatalf("expected repository to be auto-created")
	}
}

func TestGogsEnsureRepositorySkipsExistingRepo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/mirror/demo" {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	manager := NewManager()
	autoCreated, _, err := manager.EnsureRepository(context.Background(), "https://gogs.example.com/mirror/demo.git", domain.ProviderConfig{
		Provider:   domain.ProviderGogs,
		Namespace:  "mirror",
		Visibility: domain.VisibilityPrivate,
		BaseAPIURL: server.URL,
	}, &domain.Credential{Type: domain.CredentialTypeAPIToken, Secret: "token"})
	if err != nil {
		t.Fatalf("ensure repository: %v", err)
	}
	if autoCreated {
		t.Fatalf("expected existing repository to skip auto-creation")
	}
}

func TestEnsureRepositoryCreatesLocalBareRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for local repository initialization")
	}

	target := filepath.Join(t.TempDir(), "mirror.git")
	manager := NewManager()
	autoCreated, _, err := manager.EnsureRepository(context.Background(), target, domain.ProviderConfig{
		Provider:   domain.ProviderGitHub,
		Visibility: domain.VisibilityPrivate,
	}, nil)
	if err != nil {
		t.Fatalf("ensure local repository: %v", err)
	}
	if !autoCreated {
		t.Fatalf("expected local repository to be auto-created")
	}
	if !existsTargetRepo(target) {
		t.Fatalf("expected initialized local bare repository at %s", target)
	}
}

package scm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reposync/backend/internal/domain"
)

type Provider interface {
	EnsureRepository(ctx context.Context, targetRepoURL string, config domain.ProviderConfig, credential *domain.Credential) (bool, time.Duration, error)
}

type Manager struct {
	client *http.Client
}

func NewManager() *Manager {
	return &Manager{
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (m *Manager) EnsureRepository(ctx context.Context, targetRepoURL string, config domain.ProviderConfig, credential *domain.Credential) (bool, time.Duration, error) {
	if isLocalGitTarget(targetRepoURL) {
		if existsTargetRepo(targetRepoURL) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("target repository does not exist: %s", targetRepoURL)
	}

	switch config.Provider {
	case domain.ProviderGogs:
		return (&gogsProvider{client: m.client}).EnsureRepository(ctx, targetRepoURL, config, credential)
	default:
		return (&githubProvider{client: m.client}).EnsureRepository(ctx, targetRepoURL, config, credential)
	}
}

type githubProvider struct {
	client *http.Client
}

type gogsProvider struct {
	client *http.Client
}

func (p *githubProvider) EnsureRepository(ctx context.Context, targetRepoURL string, config domain.ProviderConfig, credential *domain.Credential) (bool, time.Duration, error) {
	apiBase := strings.TrimRight(config.BaseAPIURL, "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	owner, repo, err := parseOwnerRepo(targetRepoURL, config.Namespace)
	if err != nil {
		return false, 0, err
	}
	started := time.Now()
	exists, err := repositoryExists(ctx, p.client, fmt.Sprintf("%s/repos/%s/%s", apiBase, owner, repo), credential)
	if err == nil && exists {
		return false, time.Since(started), nil
	}
	if err != nil && !isNotFound(err) {
		return false, 0, err
	}
	payload := map[string]any{
		"name":        repo,
		"private":     config.Visibility != domain.VisibilityPublic,
		"description": renderDescription(config.DescriptionTemplate, repo),
	}
	createURL := fmt.Sprintf("%s/orgs/%s/repos", apiBase, owner)
	if owner == "" {
		createURL = fmt.Sprintf("%s/user/repos", apiBase)
	}
	if err := createRepository(ctx, p.client, createURL, payload, credential); err != nil {
		return false, 0, err
	}
	return true, time.Since(started), nil
}

func (p *gogsProvider) EnsureRepository(ctx context.Context, targetRepoURL string, config domain.ProviderConfig, credential *domain.Credential) (bool, time.Duration, error) {
	apiBase := strings.TrimRight(config.BaseAPIURL, "/")
	if apiBase == "" {
		parsed, err := url.Parse(targetRepoURL)
		if err != nil {
			return false, 0, err
		}
		apiBase = parsed.Scheme + "://" + parsed.Host + "/api/v1"
	}
	owner, repo, err := parseOwnerRepo(targetRepoURL, config.Namespace)
	if err != nil {
		return false, 0, err
	}
	started := time.Now()
	exists, err := repositoryExists(ctx, p.client, fmt.Sprintf("%s/repos/%s/%s", apiBase, owner, repo), credential)
	if err == nil && exists {
		return false, time.Since(started), nil
	}
	if err != nil && !isNotFound(err) {
		return false, 0, err
	}
	payload := map[string]any{
		"name":        repo,
		"private":     config.Visibility != domain.VisibilityPublic,
		"description": renderDescription(config.DescriptionTemplate, repo),
	}
	createURL := fmt.Sprintf("%s/admin/users/%s/repos", apiBase, owner)
	if err := createRepository(ctx, p.client, createURL, payload, credential); err != nil {
		return false, 0, err
	}
	return true, time.Since(started), nil
}

func repositoryExists(ctx context.Context, client *http.Client, target string, credential *domain.Credential) (bool, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	applyAuth(req, credential)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	body, _ := io.ReadAll(resp.Body)
	return false, httpError{statusCode: resp.StatusCode, message: string(body)}
}

func createRepository(ctx context.Context, client *http.Client, target string, payload map[string]any, credential *domain.Credential) error {
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	applyAuth(req, credential)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return httpError{statusCode: resp.StatusCode, message: string(body)}
}

type httpError struct {
	statusCode int
	message    string
}

func (e httpError) Error() string {
	return fmt.Sprintf("http %d: %s", e.statusCode, strings.TrimSpace(e.message))
}

func isNotFound(err error) bool {
	httpErr, ok := err.(httpError)
	return ok && httpErr.statusCode == http.StatusNotFound
}

func applyAuth(req *http.Request, credential *domain.Credential) {
	if credential == nil {
		return
	}
	switch credential.Type {
	case domain.CredentialTypeAPIToken, domain.CredentialTypeHTTPSToken:
		req.Header.Set("Authorization", "token "+credential.Secret)
	}
}

func parseOwnerRepo(targetRepoURL string, fallbackOwner string) (string, string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(targetRepoURL), ".git")
	if strings.HasPrefix(trimmed, "git@") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid target repository url: %s", targetRepoURL)
		}
		segments := strings.Split(strings.Trim(parts[1], "/"), "/")
		if len(segments) < 2 {
			return "", "", fmt.Errorf("invalid target repository url: %s", targetRepoURL)
		}
		return strings.Join(segments[:len(segments)-1], "/"), segments[len(segments)-1], nil
	}
	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(segments) >= 2 {
			return strings.Join(segments[:len(segments)-1], "/"), segments[len(segments)-1], nil
		}
	}
	base := filepath.Base(trimmed)
	if fallbackOwner == "" {
		return "", strings.TrimSuffix(base, ".git"), fmt.Errorf("missing namespace for target repository: %s", targetRepoURL)
	}
	return fallbackOwner, strings.TrimSuffix(base, ".git"), nil
}

func renderDescription(template string, repo string) string {
	if strings.TrimSpace(template) == "" {
		return "mirror for " + repo
	}
	return strings.ReplaceAll(template, "{{repo}}", repo)
}

func isLocalGitTarget(target string) bool {
	if strings.HasPrefix(target, "git@") {
		return false
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "ssh://") {
		return false
	}
	return true
}

func existsTargetRepo(target string) bool {
	info, err := os.Stat(target)
	if err == nil && info.IsDir() {
		_, headErr := os.Stat(filepath.Join(target, "HEAD"))
		return headErr == nil
	}
	return false
}

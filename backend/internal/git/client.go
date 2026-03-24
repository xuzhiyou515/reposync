package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reposync/backend/internal/domain"
)

type Client struct {
	bin  string
	logf func(format string, args ...any)
}

type Submodule struct {
	Path   string
	URL    string
	Commit string
}

type SubmoduleRewrite struct {
	URL    string
	Commit string
}

type RewriteResult struct {
	SourceToTarget map[string]string
}

type SVNRefPromotionResult struct {
	BranchCount   int
	TagCount      int
	DefaultBranch string
	DefaultCommit string
}

func NewClient(bin string) *Client {
	if strings.TrimSpace(bin) == "" {
		bin = "git"
	}
	return &Client{bin: bin}
}

func (c *Client) WithLogger(logf func(format string, args ...any)) *Client {
	if c == nil {
		return nil
	}
	return &Client{
		bin:  c.bin,
		logf: logf,
	}
}

func (c *Client) EnsureMirror(ctx context.Context, sourceURL string, cachePath string, credential *domain.Credential) (bool, time.Duration, error) {
	started := time.Now()
	cacheHit := isGitMirror(cachePath)
	authURL, env, cleanup, err := prepareGitAuth(sourceURL, credential)
	if err != nil {
		return cacheHit, 0, err
	}
	defer cleanup()

	if !cacheHit {
		if info, statErr := os.Stat(cachePath); statErr == nil && info.IsDir() {
			// Recover from half-created/broken cache dirs left by interrupted runs.
			if c.logf != nil {
				c.logf("cache recovery: removing non-mirror cache dir %s", cachePath)
			}
			if err := os.RemoveAll(cachePath); err != nil {
				return false, 0, err
			}
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return false, 0, err
		}
		if _, err := c.runWithEnv(ctx, "", env, "clone", "--mirror", "--progress", authURL, cachePath); err != nil {
			return false, 0, err
		}
		if authURL != sourceURL {
			if _, err := c.run(ctx, cachePath, "remote", "set-url", "origin", sourceURL); err != nil {
				return false, 0, err
			}
		}
		return false, time.Since(started), nil
	}

	if _, err := c.run(ctx, cachePath, "remote", "set-url", "origin", authURL); err != nil {
		return true, 0, err
	}
	if _, err := c.runWithEnv(ctx, cachePath, env, "fetch", "--progress", "--prune", "origin", "+refs/*:refs/*"); err != nil {
		return true, 0, err
	}
	if authURL != sourceURL {
		if _, err := c.run(ctx, cachePath, "remote", "set-url", "origin", sourceURL); err != nil {
			return true, 0, err
		}
	}
	return true, time.Since(started), nil
}

func (c *Client) MirrorPush(ctx context.Context, cachePath string, targetURL string, credential *domain.Credential) (time.Duration, error) {
	started := time.Now()
	authURL, env, cleanup, err := prepareGitAuth(targetURL, credential)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	if _, err := c.run(ctx, cachePath, "remote", "remove", "reposync-target"); err != nil && !strings.Contains(err.Error(), "No such remote") {
		return 0, err
	}
	if _, err := c.run(ctx, cachePath, "remote", "add", "reposync-target", authURL); err != nil {
		return 0, err
	}
	if _, err := c.runWithEnv(ctx, cachePath, env, pushArgs("reposync-target")...); err != nil {
		return 0, err
	}
	if _, err := c.run(ctx, cachePath, "remote", "remove", "reposync-target"); err != nil && !strings.Contains(err.Error(), "No such remote") {
		return 0, err
	}
	return time.Since(started), nil
}

func (c *Client) EnsureSVNCheckout(ctx context.Context, sourceURL string, cachePath string, svnConfig domain.SVNConfig, credential *domain.Credential) (bool, time.Duration, error) {
	started := time.Now()
	cacheHit := isGitWorktree(cachePath)
	authURL, env, cleanup, err := prepareGitAuth(sourceURL, credential)
	if err != nil {
		return cacheHit, 0, err
	}
	defer cleanup()

	if !cacheHit {
		if info, statErr := os.Stat(cachePath); statErr == nil && info.IsDir() {
			if c.logf != nil {
				c.logf("cache recovery: removing non-repository svn cache dir %s", cachePath)
			}
			if err := os.RemoveAll(cachePath); err != nil {
				return false, 0, err
			}
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return false, 0, err
		}
		args := buildSVNCloneArgs(authURL, cachePath, svnConfig)
		if _, err := c.runWithEnv(ctx, "", env, args...); err != nil {
			return false, 0, err
		}
		if authURL != sourceURL {
			if _, err := c.run(ctx, cachePath, "config", "svn-remote.svn.url", sourceURL); err != nil {
				return false, 0, err
			}
		}
		return false, time.Since(started), nil
	}

	restoreURL := sourceURL
	if authURL != sourceURL {
		if _, err := c.run(ctx, cachePath, "config", "svn-remote.svn.url", authURL); err != nil {
			return true, 0, err
		}
		defer func() {
			_, _ = c.run(context.Background(), cachePath, "config", "svn-remote.svn.url", restoreURL)
		}()
	}
	if _, err := c.runWithEnv(ctx, cachePath, env, buildSVNFetchArgs(svnConfig)...); err != nil {
		return true, 0, err
	}
	return true, time.Since(started), nil
}

func (c *Client) PromoteSVNRefs(ctx context.Context, repoPath string, svnConfig domain.SVNConfig) (SVNRefPromotionResult, error) {
	refs, err := c.listRefs(ctx, repoPath, "refs/remotes")
	if err != nil {
		return SVNRefPromotionResult{}, err
	}
	result := SVNRefPromotionResult{
		DefaultBranch: trunkBranchName(svnConfig.TrunkPath),
	}
	for _, ref := range refs {
		kind, name := classifySVNRemoteRef(ref.Name, svnConfig)
		if kind == "" || strings.TrimSpace(name) == "" {
			continue
		}
		switch kind {
		case "branch":
			if _, err := c.run(ctx, repoPath, "branch", "-f", name, ref.ObjectName); err != nil {
				return SVNRefPromotionResult{}, err
			}
			result.BranchCount++
			if name == result.DefaultBranch {
				result.DefaultCommit = ref.ObjectName
			}
		case "tag":
			if _, err := c.run(ctx, repoPath, "tag", "-f", name, ref.ObjectName); err != nil {
				return SVNRefPromotionResult{}, err
			}
			result.TagCount++
		}
	}
	if result.DefaultCommit == "" && result.DefaultBranch != "" {
		result.DefaultCommit = c.ResolveRef(ctx, repoPath, "refs/heads/"+result.DefaultBranch)
	}
	return result, nil
}

func (c *Client) PushBranchesAndTags(ctx context.Context, repoPath string, targetURL string, credential *domain.Credential) (time.Duration, error) {
	started := time.Now()
	authURL, env, cleanup, err := prepareGitAuth(targetURL, credential)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	if _, err := c.run(ctx, repoPath, "remote", "remove", "reposync-target"); err != nil && !strings.Contains(err.Error(), "No such remote") {
		return 0, err
	}
	if _, err := c.run(ctx, repoPath, "remote", "add", "reposync-target", authURL); err != nil {
		return 0, err
	}
	args := []string{
		"-c", "http.postBuffer=524288000",
		"-c", "credential.interactive=false",
		"-c", "http.version=HTTP/1.1",
		"push", "--progress", "--prune", "reposync-target",
		"+refs/heads/*:refs/heads/*",
		"+refs/tags/*:refs/tags/*",
	}
	if _, err := c.runWithEnv(ctx, repoPath, env, args...); err != nil {
		return 0, err
	}
	if _, err := c.run(ctx, repoPath, "remote", "remove", "reposync-target"); err != nil && !strings.Contains(err.Error(), "No such remote") {
		return 0, err
	}
	return time.Since(started), nil
}

func (c *Client) ResolveHEAD(ctx context.Context, repoPath string) string {
	out, err := c.run(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (c *Client) ResolveRef(ctx context.Context, repoPath string, ref string) string {
	out, err := c.run(ctx, repoPath, "rev-parse", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (c *Client) ReadSubmodules(ctx context.Context, repoPath string) ([]Submodule, error) {
	content, err := c.run(ctx, repoPath, "show", "HEAD:.gitmodules")
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil, nil
		}
		return nil, err
	}

	configured := parseGitmodules(content)
	var result []Submodule
	for _, item := range configured {
		tree, treeErr := c.run(ctx, repoPath, "ls-tree", "HEAD", item.Path)
		if treeErr != nil {
			return nil, treeErr
		}
		fields := strings.Fields(strings.TrimSpace(tree))
		if len(fields) < 3 {
			continue
		}
		result = append(result, Submodule{
			Path:   item.Path,
			URL:    item.URL,
			Commit: fields[2],
		})
	}
	return result, nil
}

func (c *Client) RewriteSubmodulesAndPushBranches(ctx context.Context, cachePath string, targetURL string, mapping map[string]SubmoduleRewrite, credential *domain.Credential) (RewriteResult, time.Duration, error) {
	started := time.Now()
	tempDir, err := os.MkdirTemp("", "reposync-rewrite-*")
	if err != nil {
		return RewriteResult{}, 0, err
	}
	defer os.RemoveAll(tempDir)
	authURL, env, cleanup, err := prepareGitAuth(targetURL, credential)
	if err != nil {
		return RewriteResult{}, 0, err
	}
	defer cleanup()

	if _, err := c.run(ctx, "", "clone", cachePath, tempDir); err != nil {
		return RewriteResult{}, 0, err
	}
	if _, err := c.run(ctx, tempDir, "config", "user.name", "RepoSync"); err != nil {
		return RewriteResult{}, 0, err
	}
	if _, err := c.run(ctx, tempDir, "config", "user.email", "reposync@example.com"); err != nil {
		return RewriteResult{}, 0, err
	}
	if _, err := c.run(ctx, tempDir, "remote", "add", "reposync-target", authURL); err != nil && !strings.Contains(err.Error(), "already exists") {
		return RewriteResult{}, 0, err
	}

	branches, err := c.listRemoteBranches(ctx, tempDir)
	if err != nil {
		return RewriteResult{}, 0, err
	}
	result := RewriteResult{SourceToTarget: map[string]string{}}

	for _, branch := range branches {
		if _, err := c.run(ctx, tempDir, "checkout", "-B", branch, "origin/"+branch); err != nil {
			return RewriteResult{}, 0, err
		}
		sourceHeadOut, err := c.run(ctx, tempDir, "rev-parse", "HEAD")
		if err != nil {
			return RewriteResult{}, 0, err
		}
		sourceHead := strings.TrimSpace(sourceHeadOut)
		sourceCommitDate, err := c.commitDate(ctx, tempDir, "HEAD")
		if err != nil {
			return RewriteResult{}, 0, err
		}
		changed, err := rewriteGitmodulesFile(filepath.Join(tempDir, ".gitmodules"), mapping)
		if err != nil {
			return RewriteResult{}, 0, err
		}
		gitlinkChanged, err := c.rewriteGitlinks(ctx, tempDir, mapping)
		if err != nil {
			return RewriteResult{}, 0, err
		}
		if changed || gitlinkChanged {
			if _, err := c.run(ctx, tempDir, "add", ".gitmodules"); err != nil {
				if !os.IsNotExist(err) && !strings.Contains(err.Error(), "pathspec '.gitmodules' did not match") {
					return RewriteResult{}, 0, err
				}
			}
			env := []string{
				"GIT_AUTHOR_DATE=" + sourceCommitDate,
				"GIT_COMMITTER_DATE=" + sourceCommitDate,
			}
			if _, err := c.runWithEnv(ctx, tempDir, env, "commit", "-m", "Rewrite submodule URLs for mirror target"); err != nil {
				return RewriteResult{}, 0, err
			}
		}
		targetHeadOut, err := c.run(ctx, tempDir, "rev-parse", "HEAD")
		if err != nil {
			return RewriteResult{}, 0, err
		}
		targetHead := strings.TrimSpace(targetHeadOut)
		result.SourceToTarget[sourceHead] = targetHead
		if _, err := c.runWithEnv(ctx, tempDir, env, forcePushBranchArgs("reposync-target", branch)...); err != nil {
			return RewriteResult{}, 0, err
		}
	}

	return result, time.Since(started), nil
}

func (c *Client) run(ctx context.Context, dir string, args ...string) (string, error) {
	return c.runWithEnv(ctx, dir, nil, args...)
}

func (c *Client) runWithEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	baseEnv := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	}
	cmd.Env = append(os.Environ(), baseEnv...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Env, env...)
	}
	if c.logf != nil {
		c.logf("git exec: %s", sanitizeArgs(args))
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var heartbeatDone chan struct{}
	if c.logf != nil {
		heartbeatDone = make(chan struct{})
		started := time.Now()
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatDone:
					return
				case <-ticker.C:
					c.logf("git running: %s (elapsed %s)", sanitizeArgs(args), time.Since(started).Round(time.Second))
				}
			}
		}()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go c.streamPipe(&wg, "stdout", stdoutPipe, &stdout)
	go c.streamPipe(&wg, "stderr", stderrPipe, &stderr)
	waitErr := cmd.Wait()
	if heartbeatDone != nil {
		close(heartbeatDone)
	}
	wg.Wait()
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		if c.logf != nil {
			c.logf("git error: %s", sanitizeMessage(msg))
		}
		return "", fmt.Errorf("git %s: %w", sanitizeArgs(args), errors.New(sanitizeMessage(msg)))
	}
	if c.logf != nil {
		c.logf("git done: %s", sanitizeArgs(args))
	}
	return stdout.String(), nil
}

func pushArgs(remote string) []string {
	return []string{
		"-c", "http.postBuffer=524288000",
		"-c", "credential.interactive=false",
		"-c", "http.version=HTTP/1.1",
		"push", "--progress", "--mirror", remote,
	}
}

func forcePushBranchArgs(remote string, branch string) []string {
	return []string{
		"-c", "http.postBuffer=524288000",
		"-c", "credential.interactive=false",
		"-c", "http.version=HTTP/1.1",
		"push", "--progress", "--force", remote, "HEAD:refs/heads/" + branch,
	}
}

func (c *Client) streamPipe(wg *sync.WaitGroup, stream string, pipe io.ReadCloser, out *bytes.Buffer) {
	defer wg.Done()
	defer pipe.Close()
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		out.WriteString(line)
		out.WriteByte('\n')
		if c.logf != nil && strings.TrimSpace(line) != "" {
			c.logf("git %s: %s", stream, line)
		}
	}
}

func (c *Client) commitDate(ctx context.Context, repoPath string, rev string) (string, error) {
	out, err := c.run(ctx, repoPath, "show", "-s", "--format=%cI", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func isGitMirror(path string) bool {
	info, err := os.Stat(filepath.Join(path, "HEAD"))
	return err == nil && !info.IsDir()
}

func isGitWorktree(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git", "HEAD"))
	return err == nil && !info.IsDir()
}

func (c *Client) listRemoteBranches(ctx context.Context, repoPath string) ([]string, error) {
	out, err := c.run(ctx, repoPath, "for-each-ref", "--format=%(refname:strip=3)", "refs/remotes/origin")
	if err != nil {
		return nil, err
	}
	var result []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" || branch == "HEAD" {
			continue
		}
		if !seen[branch] {
			seen[branch] = true
			result = append(result, branch)
		}
	}
	sort.Strings(result)
	return result, nil
}

type gitRef struct {
	Name       string
	ObjectName string
}

func (c *Client) listRefs(ctx context.Context, repoPath string, refPrefix string) ([]gitRef, error) {
	out, err := c.run(ctx, repoPath, "for-each-ref", "--format=%(refname:short) %(objectname)", refPrefix)
	if err != nil {
		return nil, err
	}
	var refs []gitRef
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		refs = append(refs, gitRef{Name: fields[0], ObjectName: fields[1]})
	}
	return refs, nil
}

func buildSVNCloneArgs(sourceURL string, cachePath string, svnConfig domain.SVNConfig) []string {
	args := []string{
		"svn", "clone", sourceURL, cachePath,
		"--prefix=svn/",
		"--trunk=" + defaultSVNLayoutPath(svnConfig.TrunkPath, "trunk"),
		"--branches=" + defaultSVNLayoutPath(svnConfig.BranchesPath, "branches"),
		"--tags=" + defaultSVNLayoutPath(svnConfig.TagsPath, "tags"),
	}
	if strings.TrimSpace(svnConfig.AuthorsFilePath) != "" {
		args = append(args, "--authors-file="+svnConfig.AuthorsFilePath)
	}
	return args
}

func buildSVNFetchArgs(svnConfig domain.SVNConfig) []string {
	args := []string{"svn", "fetch"}
	if strings.TrimSpace(svnConfig.AuthorsFilePath) != "" {
		args = append(args, "--authors-file="+svnConfig.AuthorsFilePath)
	}
	return args
}

func defaultSVNLayoutPath(value string, fallback string) string {
	trimmed := strings.Trim(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func trunkBranchName(trunkPath string) string {
	normalized := strings.ReplaceAll(defaultSVNLayoutPath(trunkPath, "trunk"), "\\", "/")
	parts := strings.Split(normalized, "/")
	return parts[len(parts)-1]
}

func classifySVNRemoteRef(refName string, svnConfig domain.SVNConfig) (string, string) {
	name := strings.TrimSpace(refName)
	if name == "" {
		return "", ""
	}
	trimmed := strings.TrimPrefix(name, "svn/")
	normalizedTrunk := defaultSVNLayoutPath(svnConfig.TrunkPath, "trunk")
	normalizedBranches := defaultSVNLayoutPath(svnConfig.BranchesPath, "branches")
	normalizedTags := defaultSVNLayoutPath(svnConfig.TagsPath, "tags")

	if trimmed == normalizedTrunk || name == normalizedTrunk {
		return "branch", trunkBranchName(svnConfig.TrunkPath)
	}
	if strings.HasPrefix(trimmed, normalizedBranches+"/") {
		return "branch", strings.TrimPrefix(trimmed, normalizedBranches+"/")
	}
	if strings.HasPrefix(trimmed, normalizedTags+"/") {
		return "tag", strings.TrimPrefix(trimmed, normalizedTags+"/")
	}
	if strings.HasPrefix(name, "tags/") {
		return "tag", strings.TrimPrefix(name, "tags/")
	}
	if strings.HasPrefix(trimmed, "tags/") {
		return "tag", strings.TrimPrefix(trimmed, "tags/")
	}
	if strings.HasPrefix(trimmed, "branches/") {
		return "branch", strings.TrimPrefix(trimmed, "branches/")
	}
	if strings.HasPrefix(trimmed, "remote/") || trimmed == "HEAD" {
		return "", ""
	}
	if strings.HasPrefix(name, "svn/") && !strings.Contains(trimmed, "/") {
		return "branch", trimmed
	}
	return "", ""
}

type submoduleConfig struct {
	Path string
	URL  string
}

func parseGitmodules(content string) []submoduleConfig {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var current submoduleConfig
	var result []submoduleConfig
	flush := func() {
		if current.Path != "" && current.URL != "" {
			result = append(result, current)
		}
		current = submoduleConfig{}
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "[submodule"):
			flush()
		case strings.HasPrefix(line, "path ="):
			current.Path = strings.TrimSpace(strings.TrimPrefix(line, "path ="))
		case strings.HasPrefix(line, "url ="):
			current.URL = strings.TrimSpace(strings.TrimPrefix(line, "url ="))
		}
	}
	flush()
	return result
}

func (c *Client) rewriteGitlinks(ctx context.Context, repoPath string, mapping map[string]SubmoduleRewrite) (bool, error) {
	changed := false
	for path, item := range mapping {
		if strings.TrimSpace(item.Commit) == "" {
			continue
		}
		currentCommit, err := c.submoduleCommit(ctx, repoPath, path)
		if err != nil {
			return false, err
		}
		if currentCommit == item.Commit {
			continue
		}
		if _, err := c.run(ctx, repoPath, "update-index", "--cacheinfo", "160000", item.Commit, path); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func (c *Client) submoduleCommit(ctx context.Context, repoPath string, submodulePath string) (string, error) {
	tree, err := c.run(ctx, repoPath, "ls-tree", "HEAD", submodulePath)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(strings.TrimSpace(tree))
	if len(fields) < 3 {
		return "", nil
	}
	return fields[2], nil
}

func rewriteGitmodulesFile(path string, mapping map[string]SubmoduleRewrite) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	lines := strings.Split(string(content), "\n")
	currentPath := ""
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "[submodule"):
			currentPath = ""
		case strings.HasPrefix(trimmed, "path ="):
			currentPath = strings.TrimSpace(strings.TrimPrefix(trimmed, "path ="))
		case strings.HasPrefix(trimmed, "url ="):
			target, ok := mapping[currentPath]
			if !ok {
				continue
			}
			if strings.TrimSpace(target.URL) == "" {
				continue
			}
			prefixIndex := strings.Index(line, "url =")
			if prefixIndex < 0 {
				prefixIndex = 0
			}
			lines[i] = line[:prefixIndex] + "url = " + target.URL
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

func prepareGitAuth(rawURL string, credential *domain.Credential) (string, []string, func(), error) {
	if credential == nil {
		return rawURL, nil, func() {}, nil
	}
	switch credential.Type {
	case domain.CredentialTypeHTTPSToken, domain.CredentialTypeAPIToken:
		parsed, err := url.Parse(rawURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return rawURL, nil, func() {}, nil
		}
		username := strings.TrimSpace(credential.Username)
		if username == "" {
			username = "git"
		}
		parsed.User = url.UserPassword(username, credential.Secret)
		return parsed.String(), nil, func() {}, nil
	case domain.CredentialTypeSSHKey:
		keyFile, err := os.CreateTemp("", "reposync-key-*")
		if err != nil {
			return rawURL, nil, nil, err
		}
		if err := keyFile.Close(); err != nil {
			_ = os.Remove(keyFile.Name())
			return rawURL, nil, nil, err
		}
		if err := os.WriteFile(keyFile.Name(), []byte(credential.Secret), 0o600); err != nil {
			_ = os.Remove(keyFile.Name())
			return rawURL, nil, nil, err
		}
		// Keep SSH behavior explicit and compatible with legacy Git servers that
		// still only offer ssh-rsa host keys.
		sshCommand := fmt.Sprintf(
			`ssh -i "%s" -o IdentitiesOnly=yes -o PreferredAuthentications=publickey -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o BatchMode=yes -o ConnectTimeout=15 -o ConnectionAttempts=1 -o StrictHostKeyChecking=no -o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa`,
			keyFile.Name(),
		)
		return rawURL, []string{"GIT_SSH_COMMAND=" + sshCommand}, func() { _ = os.Remove(keyFile.Name()) }, nil
	default:
		return rawURL, nil, func() {}, nil
	}
}

func sanitizeArgs(args []string) string {
	safe := make([]string, len(args))
	for i, arg := range args {
		safe[i] = sanitizeArg(arg)
	}
	return strings.Join(safe, " ")
}

func sanitizeMessage(message string) string {
	fields := strings.Fields(message)
	if len(fields) == 0 {
		return message
	}
	for i, field := range fields {
		fields[i] = sanitizeArg(field)
	}
	return strings.Join(fields, " ")
}

func sanitizeArg(arg string) string {
	parsed, err := url.Parse(arg)
	if err != nil || parsed.User == nil {
		return arg
	}
	username := parsed.User.Username()
	if username == "" {
		username = "git"
	}
	parsed.User = url.UserPassword(username, "***")
	return parsed.String()
}

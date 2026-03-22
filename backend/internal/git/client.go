package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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

func (c *Client) EnsureMirror(ctx context.Context, sourceURL string, cachePath string) (bool, time.Duration, error) {
	started := time.Now()
	cacheHit := isGitMirror(cachePath)

	if !cacheHit {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return false, 0, err
		}
		if _, err := c.run(ctx, "", "clone", "--mirror", sourceURL, cachePath); err != nil {
			return false, 0, err
		}
		return false, time.Since(started), nil
	}

	if _, err := c.run(ctx, cachePath, "remote", "set-url", "origin", sourceURL); err != nil {
		return true, 0, err
	}
	if _, err := c.run(ctx, cachePath, "fetch", "--prune", "origin", "+refs/*:refs/*"); err != nil {
		return true, 0, err
	}
	return true, time.Since(started), nil
}

func (c *Client) MirrorPush(ctx context.Context, cachePath string, targetURL string) (time.Duration, error) {
	started := time.Now()
	if _, err := c.run(ctx, cachePath, "remote", "remove", "reposync-target"); err != nil && !strings.Contains(err.Error(), "No such remote") {
		return 0, err
	}
	if _, err := c.run(ctx, cachePath, "remote", "add", "reposync-target", targetURL); err != nil {
		return 0, err
	}
	if _, err := c.run(ctx, cachePath, "push", "--mirror", "reposync-target"); err != nil {
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

func (c *Client) RewriteSubmoduleURLsAndPushBranches(ctx context.Context, cachePath string, targetURL string, mapping map[string]string) (time.Duration, error) {
	started := time.Now()
	tempDir, err := os.MkdirTemp("", "reposync-rewrite-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tempDir)

	if _, err := c.run(ctx, "", "clone", cachePath, tempDir); err != nil {
		return 0, err
	}
	if _, err := c.run(ctx, tempDir, "config", "user.name", "RepoSync"); err != nil {
		return 0, err
	}
	if _, err := c.run(ctx, tempDir, "config", "user.email", "reposync@example.com"); err != nil {
		return 0, err
	}
	if _, err := c.run(ctx, tempDir, "remote", "add", "reposync-target", targetURL); err != nil && !strings.Contains(err.Error(), "already exists") {
		return 0, err
	}

	branches, err := c.listRemoteBranches(ctx, tempDir)
	if err != nil {
		return 0, err
	}

	for _, branch := range branches {
		if _, err := c.run(ctx, tempDir, "checkout", "-B", branch, "origin/"+branch); err != nil {
			return 0, err
		}
		sourceCommitDate, err := c.commitDate(ctx, tempDir, "HEAD")
		if err != nil {
			return 0, err
		}
		changed, err := rewriteGitmodulesFile(filepath.Join(tempDir, ".gitmodules"), mapping)
		if err != nil {
			return 0, err
		}
		if changed {
			if _, err := c.run(ctx, tempDir, "add", ".gitmodules"); err != nil {
				return 0, err
			}
			env := []string{
				"GIT_AUTHOR_DATE=" + sourceCommitDate,
				"GIT_COMMITTER_DATE=" + sourceCommitDate,
			}
			if _, err := c.runWithEnv(ctx, tempDir, env, "commit", "-m", "Rewrite submodule URLs for mirror target"); err != nil {
				return 0, err
			}
		}
		if _, err := c.run(ctx, tempDir, "push", "--force", "reposync-target", "HEAD:refs/heads/"+branch); err != nil {
			return 0, err
		}
	}

	return time.Since(started), nil
}

func (c *Client) run(ctx context.Context, dir string, args ...string) (string, error) {
	return c.runWithEnv(ctx, dir, nil, args...)
}

func (c *Client) runWithEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if c.logf != nil {
		c.logf("git exec: %s", strings.Join(args, " "))
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
	var wg sync.WaitGroup
	wg.Add(2)
	go c.streamPipe(&wg, "stdout", stdoutPipe, &stdout)
	go c.streamPipe(&wg, "stderr", stderrPipe, &stderr)
	waitErr := cmd.Wait()
	wg.Wait()
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		if c.logf != nil {
			c.logf("git error: %s", msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), errors.New(msg))
	}
	if c.logf != nil {
		c.logf("git done: %s", strings.Join(args, " "))
	}
	return stdout.String(), nil
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

func rewriteGitmodulesFile(path string, mapping map[string]string) (bool, error) {
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
			prefixIndex := strings.Index(line, "url =")
			if prefixIndex < 0 {
				prefixIndex = 0
			}
			lines[i] = line[:prefixIndex] + "url = " + target
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	bin string
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

func (c *Client) run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), errors.New(msg))
	}
	return stdout.String(), nil
}

func isGitMirror(path string) bool {
	info, err := os.Stat(filepath.Join(path, "HEAD"))
	return err == nil && !info.IsDir()
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

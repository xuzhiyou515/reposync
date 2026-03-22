package service

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reposync/backend/internal/domain"
	"reposync/backend/internal/git"
	"reposync/backend/internal/scm"
	"reposync/backend/internal/store"
)

type Service struct {
	store    *store.Store
	cacheDir string
	locks    sync.Map
	git      *git.Client
	scm      *scm.Manager
}

func New(db *store.Store, cacheDir string, gitClient *git.Client, scmManager *scm.Manager) *Service {
	return &Service{store: db, cacheDir: cacheDir, git: gitClient, scm: scmManager}
}

func (s *Service) SaveTask(ctx context.Context, task domain.SyncTask) (domain.SyncTask, error) {
	return s.store.SaveTask(ctx, task)
}

func (s *Service) ListTasks(ctx context.Context) ([]domain.SyncTask, error) {
	return s.store.ListTasks(ctx)
}

func (s *Service) GetTask(ctx context.Context, id int64) (domain.SyncTask, error) {
	return s.store.GetTask(ctx, id)
}

func (s *Service) DeleteTask(ctx context.Context, id int64) error {
	return s.store.DeleteTask(ctx, id)
}

func (s *Service) SaveCredential(ctx context.Context, credential domain.Credential) (domain.Credential, error) {
	return s.store.SaveCredential(ctx, credential)
}

func (s *Service) ListCredentials(ctx context.Context) ([]domain.Credential, error) {
	return s.store.ListCredentials(ctx)
}

func (s *Service) DeleteCredential(ctx context.Context, id int64) error {
	return s.store.DeleteCredential(ctx, id)
}

func (s *Service) ListCaches(ctx context.Context) ([]domain.RepoCache, error) {
	return s.store.ListCaches(ctx)
}

func (s *Service) CleanupCache(ctx context.Context, id int64) error {
	caches, err := s.store.ListCaches(ctx)
	if err != nil {
		return err
	}
	for _, cache := range caches {
		if cache.ID == id {
			if cache.CachePath != "" {
				_ = os.RemoveAll(cache.CachePath)
			}
			return s.store.DeleteCache(ctx, id)
		}
	}
	return fmt.Errorf("cache not found")
}

func (s *Service) ListExecutions(ctx context.Context, taskID int64) ([]domain.SyncExecution, error) {
	return s.store.ListExecutionsForTask(ctx, taskID)
}

func (s *Service) ExecutionDetail(ctx context.Context, id int64) (domain.ExecutionDetail, error) {
	return s.store.GetExecutionDetail(ctx, id)
}

func (s *Service) ListWebhookEvents(ctx context.Context, taskID int64) ([]domain.WebhookEvent, error) {
	return s.store.ListWebhookEventsForTask(ctx, taskID)
}

func (s *Service) ListTasksForScheduling(ctx context.Context) ([]domain.SyncTask, error) {
	return s.store.ListTasks(ctx)
}

func taskKey(id int64) string {
	return fmt.Sprintf("task-%d", id)
}

func (s *Service) taskLock(id int64) *sync.Mutex {
	value, _ := s.locks.LoadOrStore(taskKey(id), &sync.Mutex{})
	return value.(*sync.Mutex)
}

func buildCacheKey(source, target string) string {
	sum := sha1.Sum([]byte(source + "|" + target))
	return hex.EncodeToString(sum[:])
}

func resolveCacheBase(defaultBase string, taskBase string) string {
	base := strings.TrimSpace(taskBase)
	if base == "" {
		return defaultBase
	}
	if filepath.IsAbs(base) {
		return filepath.Clean(base)
	}
	return filepath.Join(defaultBase, base)
}

type executionCounters struct {
	repoCount        int
	createdRepoCount int
	failedNodeCount  int
}

type executionLogger struct {
	store     *store.Store
	execution *domain.SyncExecution
}

func (l *executionLogger) log(ctx context.Context, format string, args ...any) {
	if l == nil || l.execution == nil {
		return
	}
	line := fmt.Sprintf("%s  %s", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	if strings.TrimSpace(l.execution.SummaryLog) == "" {
		l.execution.SummaryLog = line
	} else {
		l.execution.SummaryLog += "\n" + line
	}
	_ = l.store.UpdateExecution(ctx, *l.execution)
}

func (s *Service) RunTask(ctx context.Context, taskID int64, trigger domain.TriggerType) (domain.SyncExecution, error) {
	lock := s.taskLock(taskID)
	lock.Lock()
	defer lock.Unlock()

	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return domain.SyncExecution{}, err
	}
	if !task.Enabled {
		return domain.SyncExecution{}, fmt.Errorf("task is disabled")
	}
	targetCredential, err := s.store.CredentialByOptionalID(ctx, task.TargetCredentialID)
	if err != nil && err != sql.ErrNoRows {
		return domain.SyncExecution{}, err
	}

	execution, err := s.store.CreateExecution(ctx, domain.SyncExecution{
		TaskID:      taskID,
		TriggerType: trigger,
		Status:      domain.ExecutionStatusRunning,
		SummaryLog:  "",
	})
	if err != nil {
		return domain.SyncExecution{}, err
	}
	logger := &executionLogger{store: s.store, execution: &execution}
	logger.log(ctx, "Execution started for task %q with trigger %s", task.Name, trigger)

	counters := &executionCounters{}
	visited := map[string]bool{}
	gitClient := s.git.WithLogger(func(format string, args ...any) {
		logger.log(ctx, format, args...)
	})
	_, runErr := s.syncRepository(ctx, gitClient, execution.ID, task, "", task.SourceRepoURL, task.TargetRepoURL, 0, nil, targetCredential, visited, counters, logger)

	finished := time.Now().UTC()
	execution.FinishedAt = &finished
	execution.RepoCount = counters.repoCount
	execution.CreatedRepoCount = counters.createdRepoCount
	execution.FailedNodeCount = counters.failedNodeCount
	if runErr != nil {
		execution.Status = domain.ExecutionStatusFailed
		logger.log(ctx, "Execution failed: %v", runErr)
	} else {
		execution.Status = domain.ExecutionStatusSuccess
		logger.log(ctx, "Execution completed: mirrored %d repositories and auto-created %d targets", counters.repoCount, counters.createdRepoCount)
	}
	if err := s.store.UpdateExecution(ctx, execution); err != nil {
		return domain.SyncExecution{}, err
	}
	if runErr != nil {
		return execution, runErr
	}
	return execution, nil
}

func (s *Service) syncRepository(ctx context.Context, gitClient *git.Client, executionID int64, task domain.SyncTask, repoPath string, sourceRepoURL string, targetRepoURL string, depth int, parentNodeID *int64, targetCredential *domain.Credential, visited map[string]bool, counters *executionCounters, logger *executionLogger) (domain.SyncExecutionNode, error) {
	nodeLabel := repoPath
	if nodeLabel == "" {
		nodeLabel = "(root)"
	}
	if visited[sourceRepoURL] {
		counters.failedNodeCount++
		logger.log(ctx, "Cycle detected while visiting %s", nodeLabel)
		node, err := s.store.CreateExecutionNode(ctx, domain.SyncExecutionNode{
			ExecutionID:   executionID,
			ParentNodeID:  parentNodeID,
			RepoPath:      repoPath,
			SourceRepoURL: sourceRepoURL,
			TargetRepoURL: targetRepoURL,
			Depth:         depth,
			Status:        domain.ExecutionStatusFailed,
			ErrorMessage:  "detected recursive submodule cycle",
		})
		return node, err
	}
	visited[sourceRepoURL] = true
	defer delete(visited, sourceRepoURL)

	node, err := s.store.CreateExecutionNode(ctx, domain.SyncExecutionNode{
		ExecutionID:   executionID,
		ParentNodeID:  parentNodeID,
		RepoPath:      repoPath,
		SourceRepoURL: sourceRepoURL,
		TargetRepoURL: targetRepoURL,
		Depth:         depth,
		Status:        domain.ExecutionStatusRunning,
	})
	if err != nil {
		return domain.SyncExecutionNode{}, err
	}
	logger.log(ctx, "Syncing %s from %s to %s", nodeLabel, sourceRepoURL, targetRepoURL)

	now := time.Now().UTC()
	cacheKey := buildCacheKey(sourceRepoURL, targetRepoURL)
	cacheRoot := resolveCacheBase(s.cacheDir, task.CacheBasePath)
	cachePath := filepath.Join(cacheRoot, cacheKey+".git")
	_ = os.MkdirAll(cacheRoot, 0o755)

	cacheHit := false
	hitCount := 1
	if existing, getErr := s.store.GetCacheByKey(ctx, cacheKey); getErr == nil {
		cacheHit = true
		hitCount = existing.HitCount + 1
	} else if getErr != nil && getErr != sql.ErrNoRows {
		return node, getErr
	}

	autoCreated, createDuration, createErr := s.scm.EnsureRepository(ctx, targetRepoURL, task.ProviderConfig, targetCredential)
	node.CacheKey = cacheKey
	node.CacheHit = cacheHit
	node.AutoCreated = autoCreated
	node.CreateDuration = createDuration.Milliseconds()
	if createErr != nil {
		logger.log(ctx, "Failed to ensure target repository for %s: %v", nodeLabel, createErr)
		node.Status = domain.ExecutionStatusFailed
		node.ErrorMessage = createErr.Error()
		counters.failedNodeCount++
		_ = s.store.UpdateExecutionNode(ctx, node)
		return node, createErr
	}
	if autoCreated {
		counters.createdRepoCount++
		logger.log(ctx, "Created target repository for %s in %dms", nodeLabel, createDuration.Milliseconds())
	} else {
		logger.log(ctx, "Verified target repository for %s in %dms", nodeLabel, createDuration.Milliseconds())
	}

	logger.log(ctx, "Refreshing mirror cache for %s", nodeLabel)
	cacheHitFromGit, fetchDuration, fetchErr := gitClient.EnsureMirror(ctx, sourceRepoURL, cachePath)
	node.CacheHit = node.CacheHit || cacheHitFromGit
	node.FetchDuration = fetchDuration.Milliseconds()
	if fetchErr != nil {
		logger.log(ctx, "Mirror refresh failed for %s: %v", nodeLabel, fetchErr)
		counters.failedNodeCount++
		node.Status = domain.ExecutionStatusFailed
		node.ErrorMessage = fetchErr.Error()
		_ = s.store.UpsertCache(ctx, domain.RepoCache{
			CacheKey:         cacheKey,
			SourceRepoURL:    sourceRepoURL,
			AuthContext:      "managed",
			CachePath:        cachePath,
			LastFetchAt:      &now,
			LastUsedAt:       &now,
			HitCount:         hitCount,
			HealthStatus:     "broken",
			LastErrorMessage: fetchErr.Error(),
		})
		_ = s.store.UpdateExecutionNode(ctx, node)
		return node, fetchErr
	}

	_ = s.store.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      cacheKey,
		SourceRepoURL: sourceRepoURL,
		AuthContext:   "managed",
		CachePath:     cachePath,
		LastFetchAt:   &now,
		LastUsedAt:    &now,
		HitCount:      hitCount,
		HealthStatus:  "ready",
	})
	cacheState := "cache miss"
	if node.CacheHit {
		cacheState = "cache hit"
	}
	logger.log(ctx, "Mirror cache ready for %s in %dms (%s)", nodeLabel, fetchDuration.Milliseconds(), cacheState)

	if task.RecursiveSubmodules {
		logger.log(ctx, "Scanning submodules for %s", nodeLabel)
		submodules, subErr := gitClient.ReadSubmodules(ctx, cachePath)
		if subErr != nil {
			logger.log(ctx, "Failed to read submodules for %s: %v", nodeLabel, subErr)
			counters.failedNodeCount++
			node.Status = domain.ExecutionStatusFailed
			node.ErrorMessage = subErr.Error()
			_ = s.store.UpdateExecutionNode(ctx, node)
			return node, subErr
		}
		urlMapping := map[string]string{}
		for _, submodule := range submodules {
			childTarget := mapSubmoduleTarget(targetRepoURL, submodule.Path)
			urlMapping[submodule.Path] = childTarget
			logger.log(ctx, "Discovered submodule %s -> %s", submodule.Path, childTarget)
			childNode, childErr := s.syncRepository(ctx, gitClient, executionID, task, submodule.Path, submodule.URL, childTarget, depth+1, &node.ID, targetCredential, visited, counters, logger)
			if childErr != nil {
				logger.log(ctx, "Submodule sync failed for %s: %v", submodule.Path, childErr)
				node.Status = domain.ExecutionStatusFailed
				node.ErrorMessage = childErr.Error()
				node.ReferenceCommit = submodule.Commit
				_ = s.store.UpdateExecutionNode(ctx, node)
				return childNode, childErr
			}
			_ = childNode
		}
		logger.log(ctx, "Pushing mirrored refs for %s", nodeLabel)
		pushDuration, pushErr := gitClient.MirrorPush(ctx, cachePath, targetRepoURL)
		if pushErr != nil {
			logger.log(ctx, "Mirror push failed for %s: %v", nodeLabel, pushErr)
			node.PushDuration = pushDuration.Milliseconds()
			node.Status = domain.ExecutionStatusFailed
			node.ErrorMessage = pushErr.Error()
			counters.failedNodeCount++
			_ = s.store.UpdateExecutionNode(ctx, node)
			return node, pushErr
		}
		logger.log(ctx, "Rewriting .gitmodules URLs for %s", nodeLabel)
		rewriteDuration, rewriteErr := gitClient.RewriteSubmoduleURLsAndPushBranches(ctx, cachePath, targetRepoURL, urlMapping)
		node.PushDuration = (pushDuration + rewriteDuration).Milliseconds()
		node.ReferenceCommit = gitClient.ResolveHEAD(ctx, cachePath)
		node.Status = executionStatus(rewriteErr)
		node.ErrorMessage = errorString(rewriteErr)
		if rewriteErr != nil {
			logger.log(ctx, "Submodule URL rewrite failed for %s: %v", nodeLabel, rewriteErr)
			counters.failedNodeCount++
			_ = s.store.UpdateExecutionNode(ctx, node)
			return node, rewriteErr
		}
		logger.log(ctx, "Completed recursive sync for %s in %dms", nodeLabel, node.PushDuration)
		counters.repoCount++
		err = s.store.UpdateExecutionNode(ctx, node)
		return node, err
	}

	node.ReferenceCommit = gitClient.ResolveHEAD(ctx, cachePath)
	logger.log(ctx, "Pushing mirrored refs for %s", nodeLabel)
	pushDuration, pushErr := gitClient.MirrorPush(ctx, cachePath, targetRepoURL)
	node.PushDuration = pushDuration.Milliseconds()
	node.Status = executionStatus(pushErr)
	node.ErrorMessage = errorString(pushErr)
	if pushErr != nil {
		logger.log(ctx, "Mirror push failed for %s: %v", nodeLabel, pushErr)
		counters.failedNodeCount++
		_ = s.store.UpdateExecutionNode(ctx, node)
		return node, pushErr
	}

	logger.log(ctx, "Completed sync for %s in %dms", nodeLabel, node.PushDuration)
	counters.repoCount++
	err = s.store.UpdateExecutionNode(ctx, node)
	return node, err
}

func mapSubmoduleTarget(parentTarget string, submodulePath string) string {
	if strings.HasSuffix(parentTarget, ".git") {
		base := strings.TrimSuffix(parentTarget, ".git")
		flattened := strings.ReplaceAll(strings.Trim(submodulePath, "/"), "/", "-")
		if flattened == "" {
			flattened = "submodule"
		}
		return normalizeGitTarget(base + "-" + flattened + ".git")
	}
	flattened := strings.ReplaceAll(strings.Trim(submodulePath, "/"), "/", "-")
	if flattened == "" {
		flattened = "submodule"
	}
	return normalizeGitTarget(parentTarget + "-" + flattened + ".git")
}

func normalizeGitTarget(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "ssh://") || strings.HasPrefix(target, "git@") {
		return target
	}
	return filepath.ToSlash(target)
}

func executionStatus(err error) domain.ExecutionStatus {
	if err != nil {
		return domain.ExecutionStatusFailed
	}
	return domain.ExecutionStatusSuccess
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

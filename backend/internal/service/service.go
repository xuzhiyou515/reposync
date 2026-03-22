package service

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
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
	running  sync.Map
	states   sync.Map
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
	if detail, ok := s.snapshotExecutionDetail(id); ok {
		return detail, nil
	}
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

func (s *Service) markTaskRunning(id int64) bool {
	_, loaded := s.running.LoadOrStore(taskKey(id), struct{}{})
	return !loaded
}

func (s *Service) clearTaskRunning(id int64) {
	s.running.Delete(taskKey(id))
}

func cloneExecutionDetail(detail domain.ExecutionDetail) domain.ExecutionDetail {
	cloned := detail
	cloned.Nodes = append([]domain.SyncExecutionNode(nil), detail.Nodes...)
	return cloned
}

func (s *Service) registerExecutionState(task domain.SyncTask, execution domain.SyncExecution) {
	state := &executionState{
		detail: domain.ExecutionDetail{
			Execution: execution,
			Task:      task,
			Nodes:     []domain.SyncExecutionNode{},
		},
		subscribers: map[int]chan domain.ExecutionDetail{},
	}
	s.states.Store(execution.ID, state)
}

func (s *Service) snapshotExecutionDetail(executionID int64) (domain.ExecutionDetail, bool) {
	value, ok := s.states.Load(executionID)
	if !ok {
		return domain.ExecutionDetail{}, false
	}
	state := value.(*executionState)
	state.mu.RLock()
	defer state.mu.RUnlock()
	return cloneExecutionDetail(state.detail), true
}

func (s *Service) updateExecutionState(executionID int64, mutate func(detail *domain.ExecutionDetail)) {
	value, ok := s.states.Load(executionID)
	if !ok {
		return
	}
	state := value.(*executionState)
	state.mu.Lock()
	mutate(&state.detail)
	snapshot := cloneExecutionDetail(state.detail)
	subscribers := make([]chan domain.ExecutionDetail, 0, len(state.subscribers))
	for _, subscriber := range state.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	state.mu.Unlock()
	for _, subscriber := range subscribers {
		select {
		case subscriber <- snapshot:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- snapshot:
			default:
			}
		}
	}
}

func (s *Service) dropExecutionState(executionID int64) {
	s.states.Delete(executionID)
}

func (s *Service) SubscribeExecution(ctx context.Context, executionID int64) (domain.ExecutionDetail, <-chan domain.ExecutionDetail, func(), error) {
	if value, ok := s.states.Load(executionID); ok {
		state := value.(*executionState)
		state.mu.Lock()
		state.nextID++
		subscriptionID := state.nextID
		ch := make(chan domain.ExecutionDetail, 1)
		snapshot := cloneExecutionDetail(state.detail)
		state.subscribers[subscriptionID] = ch
		state.mu.Unlock()
		ch <- snapshot
		cancel := func() {
			state.mu.Lock()
			subscriber, exists := state.subscribers[subscriptionID]
			if exists {
				delete(state.subscribers, subscriptionID)
				close(subscriber)
			}
			state.mu.Unlock()
		}
		return snapshot, ch, cancel, nil
	}
	detail, err := s.store.GetExecutionDetail(ctx, executionID)
	if err != nil {
		return domain.ExecutionDetail{}, nil, func() {}, err
	}
	return detail, nil, func() {}, nil
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

func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

type executionCounters struct {
	repoCount        int
	createdRepoCount int
	failedNodeCount  int
}

type repositoryCredentials struct {
	Source    *domain.Credential
	TargetGit *domain.Credential
	TargetAPI *domain.Credential
}

type syncResult struct {
	node            domain.SyncExecutionNode
	effectiveCommit string
}

type executionState struct {
	mu          sync.RWMutex
	detail      domain.ExecutionDetail
	subscribers map[int]chan domain.ExecutionDetail
	nextID      int
}

var east8Location = time.FixedZone("CST", 8*60*60)

func east8Now() time.Time {
	return time.Now().In(east8Location)
}

type executionLogger struct {
	store     *store.Store
	execution *domain.SyncExecution
	lastFlush time.Time
	onUpdate  func(domain.SyncExecution)
}

func (l *executionLogger) log(ctx context.Context, format string, args ...any) {
	if l == nil || l.execution == nil {
		return
	}
	now := east8Now()
	line := fmt.Sprintf("%s  %s", now.Format(time.RFC3339), fmt.Sprintf(format, args...))
	if strings.TrimSpace(l.execution.SummaryLog) == "" {
		l.execution.SummaryLog = line
	} else {
		l.execution.SummaryLog += "\n" + line
	}
	if l.onUpdate != nil {
		l.onUpdate(*l.execution)
	}
	if l.lastFlush.IsZero() || now.Sub(l.lastFlush) >= 400*time.Millisecond ||
		strings.Contains(line, "Execution started") ||
		strings.Contains(line, "Execution failed") ||
		strings.Contains(line, "Execution completed") {
		_ = l.store.UpdateExecution(ctx, *l.execution)
		l.lastFlush = now
	}
}

func (l *executionLogger) flush(ctx context.Context) {
	if l == nil || l.execution == nil {
		return
	}
	if l.onUpdate != nil {
		l.onUpdate(*l.execution)
	}
	_ = l.store.UpdateExecution(ctx, *l.execution)
	l.lastFlush = east8Now()
}

func (s *Service) RunTask(ctx context.Context, taskID int64, trigger domain.TriggerType) (domain.SyncExecution, error) {
	if !s.markTaskRunning(taskID) {
		return domain.SyncExecution{}, fmt.Errorf("task is already running")
	}

	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, err
	}
	if !task.Enabled {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, fmt.Errorf("task is disabled")
	}
	sourceCredential, err := s.store.CredentialByOptionalID(ctx, task.SourceCredentialID)
	if err != nil && err != sql.ErrNoRows {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, err
	}
	targetCredential, err := s.store.CredentialByOptionalID(ctx, task.TargetCredentialID)
	if err != nil && err != sql.ErrNoRows {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, err
	}
	targetAPICredentialID := task.TargetAPICredentialID
	if targetAPICredentialID == nil {
		targetAPICredentialID = task.TargetCredentialID
	}
	targetAPICredential, err := s.store.CredentialByOptionalID(ctx, targetAPICredentialID)
	if err != nil && err != sql.ErrNoRows {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, err
	}
	submoduleSourceCredentialID := task.SubmoduleSourceCredentialID
	if submoduleSourceCredentialID == nil {
		submoduleSourceCredentialID = task.SourceCredentialID
	}
	submoduleSourceCredential, err := s.store.CredentialByOptionalID(ctx, submoduleSourceCredentialID)
	if err != nil && err != sql.ErrNoRows {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, err
	}
	submoduleTargetCredentialID := task.SubmoduleTargetCredentialID
	if submoduleTargetCredentialID == nil {
		submoduleTargetCredentialID = task.TargetCredentialID
	}
	submoduleTargetCredential, err := s.store.CredentialByOptionalID(ctx, submoduleTargetCredentialID)
	if err != nil && err != sql.ErrNoRows {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, err
	}
	submoduleTargetAPICredentialID := task.SubmoduleTargetAPICredentialID
	if submoduleTargetAPICredentialID == nil {
		submoduleTargetAPICredentialID = targetAPICredentialID
	}
	submoduleTargetAPICredential, err := s.store.CredentialByOptionalID(ctx, submoduleTargetAPICredentialID)
	if err != nil && err != sql.ErrNoRows {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, err
	}

	execution, err := s.store.CreateExecution(ctx, domain.SyncExecution{
		TaskID:      taskID,
		TriggerType: trigger,
		Status:      domain.ExecutionStatusRunning,
		SummaryLog:  "",
	})
	if err != nil {
		s.clearTaskRunning(taskID)
		return domain.SyncExecution{}, err
	}
	logger := &executionLogger{store: s.store, execution: &execution}
	s.registerExecutionState(task, execution)
	logger.onUpdate = func(updated domain.SyncExecution) {
		s.updateExecutionState(execution.ID, func(detail *domain.ExecutionDetail) {
			detail.Execution = updated
		})
	}
	logger.log(ctx, "Execution started for task %q with trigger %s", task.Name, trigger)
	rootCredentials := repositoryCredentials{
		Source:    sourceCredential,
		TargetGit: targetCredential,
		TargetAPI: targetAPICredential,
	}
	submoduleCredentials := repositoryCredentials{
		Source:    submoduleSourceCredential,
		TargetGit: submoduleTargetCredential,
		TargetAPI: submoduleTargetAPICredential,
	}

	go s.executeTask(context.Background(), task, trigger, execution, rootCredentials, submoduleCredentials)
	return execution, nil
}

func (s *Service) executeTask(ctx context.Context, task domain.SyncTask, trigger domain.TriggerType, execution domain.SyncExecution, rootCredentials repositoryCredentials, submoduleCredentials repositoryCredentials) {
	defer s.clearTaskRunning(task.ID)
	defer s.dropExecutionState(execution.ID)

	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()

	logger := &executionLogger{store: s.store, execution: &execution}
	counters := &executionCounters{}
	visited := map[string]bool{}
	logger.onUpdate = func(updated domain.SyncExecution) {
		s.updateExecutionState(execution.ID, func(detail *domain.ExecutionDetail) {
			detail.Execution = updated
		})
	}
	gitClient := s.git.WithLogger(func(format string, args ...any) {
		logger.log(ctx, format, args...)
	})
	_, runErr := s.syncRepository(ctx, gitClient, execution.ID, task, "", task.SourceRepoURL, task.TargetRepoURL, "", 0, nil, rootCredentials, submoduleCredentials, visited, counters, logger)

	finished := east8Now()
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
	logger.flush(ctx)
}

func (s *Service) syncRepository(ctx context.Context, gitClient *git.Client, executionID int64, task domain.SyncTask, repoPath string, sourceRepoURL string, targetRepoURL string, referenceCommit string, depth int, parentNodeID *int64, currentCredentials repositoryCredentials, submoduleCredentials repositoryCredentials, visited map[string]bool, counters *executionCounters, logger *executionLogger) (syncResult, error) {
	nodeLabel := repoPath
	if nodeLabel == "" {
		nodeLabel = "(root)"
	}
	if visited[sourceRepoURL] {
		counters.failedNodeCount++
		logger.log(ctx, "Cycle detected while visiting %s", nodeLabel)
		node, err := s.createExecutionNode(ctx, domain.SyncExecutionNode{
			ExecutionID:     executionID,
			ParentNodeID:    parentNodeID,
			RepoPath:        repoPath,
			SourceRepoURL:   sourceRepoURL,
			TargetRepoURL:   targetRepoURL,
			ReferenceCommit: referenceCommit,
			Depth:           depth,
			Status:          domain.ExecutionStatusFailed,
			ErrorMessage:    "detected recursive submodule cycle",
		})
		return syncResult{node: node, effectiveCommit: referenceCommit}, err
	}
	visited[sourceRepoURL] = true
	defer delete(visited, sourceRepoURL)

	node, err := s.createExecutionNode(ctx, domain.SyncExecutionNode{
		ExecutionID:     executionID,
		ParentNodeID:    parentNodeID,
		RepoPath:        repoPath,
		SourceRepoURL:   sourceRepoURL,
		TargetRepoURL:   targetRepoURL,
		ReferenceCommit: referenceCommit,
		Depth:           depth,
		Status:          domain.ExecutionStatusRunning,
	})
	if err != nil {
		return syncResult{}, err
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
		return syncResult{node: node, effectiveCommit: referenceCommit}, getErr
	}

	autoCreated, createDuration, createErr := s.scm.EnsureRepository(ctx, targetRepoURL, task.ProviderConfig, currentCredentials.TargetAPI)
	node.CacheKey = cacheKey
	node.CacheHit = cacheHit
	node.AutoCreated = autoCreated
	node.CreateDuration = createDuration.Milliseconds()
	if createErr != nil {
		logger.log(ctx, "Failed to ensure target repository for %s: %v", nodeLabel, createErr)
		node.Status = domain.ExecutionStatusFailed
		node.ErrorMessage = createErr.Error()
		counters.failedNodeCount++
		_ = s.updateExecutionNode(ctx, node)
		return syncResult{node: node, effectiveCommit: referenceCommit}, createErr
	}
	if autoCreated {
		counters.createdRepoCount++
		logger.log(ctx, "Created target repository for %s in %dms", nodeLabel, createDuration.Milliseconds())
	} else {
		logger.log(ctx, "Verified target repository for %s in %dms", nodeLabel, createDuration.Milliseconds())
	}

	logger.log(ctx, "Refreshing mirror cache for %s", nodeLabel)
	cacheHitFromGit, fetchDuration, fetchErr := gitClient.EnsureMirror(ctx, sourceRepoURL, cachePath, currentCredentials.Source)
	cacheSize := dirSize(cachePath)
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
			SizeBytes:        cacheSize,
			HealthStatus:     "broken",
			LastErrorMessage: fetchErr.Error(),
		})
		_ = s.updateExecutionNode(ctx, node)
		return syncResult{node: node, effectiveCommit: referenceCommit}, fetchErr
	}

	_ = s.store.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      cacheKey,
		SourceRepoURL: sourceRepoURL,
		AuthContext:   "managed",
		CachePath:     cachePath,
		LastFetchAt:   &now,
		LastUsedAt:    &now,
		HitCount:      hitCount,
		SizeBytes:     cacheSize,
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
			_ = s.updateExecutionNode(ctx, node)
			return syncResult{node: node, effectiveCommit: referenceCommit}, subErr
		}
		submoduleMapping := map[string]git.SubmoduleRewrite{}
		for _, submodule := range submodules {
			childTarget := mapSubmoduleTarget(targetRepoURL, submodule.Path)
			logger.log(ctx, "Discovered submodule %s -> %s", submodule.Path, childTarget)
			childResult, childErr := s.syncRepository(ctx, gitClient, executionID, task, submodule.Path, submodule.URL, childTarget, submodule.Commit, depth+1, &node.ID, submoduleCredentials, submoduleCredentials, visited, counters, logger)
			if childErr != nil {
				logger.log(ctx, "Submodule sync failed for %s: %v", submodule.Path, childErr)
				node.Status = domain.ExecutionStatusFailed
				node.ErrorMessage = childErr.Error()
				node.ReferenceCommit = submodule.Commit
				_ = s.updateExecutionNode(ctx, node)
				return childResult, childErr
			}
			submoduleMapping[submodule.Path] = git.SubmoduleRewrite{
				URL:    childTarget,
				Commit: childResult.effectiveCommit,
			}
		}
		logger.log(ctx, "Pushing mirrored refs for %s", nodeLabel)
		pushDuration, pushErr := gitClient.MirrorPush(ctx, cachePath, targetRepoURL, currentCredentials.TargetGit)
		if pushErr != nil {
			logger.log(ctx, "Mirror push failed for %s: %v", nodeLabel, pushErr)
			node.PushDuration = pushDuration.Milliseconds()
			node.Status = domain.ExecutionStatusFailed
			node.ErrorMessage = pushErr.Error()
			counters.failedNodeCount++
			_ = s.updateExecutionNode(ctx, node)
			return syncResult{node: node, effectiveCommit: referenceCommit}, pushErr
		}
		logger.log(ctx, "Rewriting submodule pointers for %s", nodeLabel)
		rewriteResult, rewriteDuration, rewriteErr := gitClient.RewriteSubmodulesAndPushBranches(ctx, cachePath, targetRepoURL, submoduleMapping, currentCredentials.TargetGit)
		node.PushDuration = (pushDuration + rewriteDuration).Milliseconds()
		sourceHead := gitClient.ResolveHEAD(ctx, cachePath)
		node.ReferenceCommit = resolveEffectiveCommit(referenceCommit, sourceHead, rewriteResult.SourceToTarget)
		node.Status = executionStatus(rewriteErr)
		node.ErrorMessage = errorString(rewriteErr)
		if rewriteErr != nil {
			logger.log(ctx, "Submodule pointer rewrite failed for %s: %v", nodeLabel, rewriteErr)
			counters.failedNodeCount++
			_ = s.updateExecutionNode(ctx, node)
			return syncResult{node: node, effectiveCommit: node.ReferenceCommit}, rewriteErr
		}
		logger.log(ctx, "Completed recursive sync for %s in %dms", nodeLabel, node.PushDuration)
		counters.repoCount++
		err = s.updateExecutionNode(ctx, node)
		return syncResult{node: node, effectiveCommit: node.ReferenceCommit}, err
	}

	sourceHead := gitClient.ResolveHEAD(ctx, cachePath)
	node.ReferenceCommit = resolveEffectiveCommit(referenceCommit, sourceHead, nil)
	logger.log(ctx, "Pushing mirrored refs for %s", nodeLabel)
	pushDuration, pushErr := gitClient.MirrorPush(ctx, cachePath, targetRepoURL, currentCredentials.TargetGit)
	node.PushDuration = pushDuration.Milliseconds()
	node.Status = executionStatus(pushErr)
	node.ErrorMessage = errorString(pushErr)
	if pushErr != nil {
		logger.log(ctx, "Mirror push failed for %s: %v", nodeLabel, pushErr)
		counters.failedNodeCount++
		_ = s.updateExecutionNode(ctx, node)
		return syncResult{node: node, effectiveCommit: node.ReferenceCommit}, pushErr
	}

	logger.log(ctx, "Completed sync for %s in %dms", nodeLabel, node.PushDuration)
	counters.repoCount++
	err = s.updateExecutionNode(ctx, node)
	return syncResult{node: node, effectiveCommit: node.ReferenceCommit}, err
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

func (s *Service) createExecutionNode(ctx context.Context, node domain.SyncExecutionNode) (domain.SyncExecutionNode, error) {
	created, err := s.store.CreateExecutionNode(ctx, node)
	if err != nil {
		return domain.SyncExecutionNode{}, err
	}
	s.updateExecutionState(created.ExecutionID, func(detail *domain.ExecutionDetail) {
		detail.Nodes = append(detail.Nodes, created)
	})
	return created, nil
}

func (s *Service) updateExecutionNode(ctx context.Context, node domain.SyncExecutionNode) error {
	if err := s.store.UpdateExecutionNode(ctx, node); err != nil {
		return err
	}
	s.updateExecutionState(node.ExecutionID, func(detail *domain.ExecutionDetail) {
		for index := range detail.Nodes {
			if detail.Nodes[index].ID == node.ID {
				detail.Nodes[index] = node
				return
			}
		}
		detail.Nodes = append(detail.Nodes, node)
	})
	return nil
}

func resolveEffectiveCommit(referenceCommit string, fallback string, rewritten map[string]string) string {
	if strings.TrimSpace(referenceCommit) != "" {
		if rewritten != nil {
			if mapped, ok := rewritten[referenceCommit]; ok && strings.TrimSpace(mapped) != "" {
				return mapped
			}
		}
		return referenceCommit
	}
	if rewritten != nil {
		if mapped, ok := rewritten[fallback]; ok && strings.TrimSpace(mapped) != "" {
			return mapped
		}
	}
	return fallback
}

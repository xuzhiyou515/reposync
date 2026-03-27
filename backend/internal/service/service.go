package service

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
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
	task = normalizeTask(task)
	if err := s.validateTask(ctx, task); err != nil {
		return domain.SyncTask{}, err
	}
	return s.store.SaveTask(ctx, task)
}

func normalizeTask(task domain.SyncTask) domain.SyncTask {
	if task.TaskType == "" {
		task.TaskType = domain.TaskTypeGitMirror
	}
	if task.SubmoduleRewriteProtocol == "" {
		task.SubmoduleRewriteProtocol = domain.SubmoduleRewriteProtocolInherit
	}
	if task.TaskType == domain.TaskTypeSVNImport {
		if strings.TrimSpace(task.SVNConfig.TrunkPath) == "" {
			task.SVNConfig.TrunkPath = "trunk"
		}
		if strings.TrimSpace(task.SVNConfig.BranchesPath) == "" {
			task.SVNConfig.BranchesPath = "branches"
		}
		if strings.TrimSpace(task.SVNConfig.TagsPath) == "" {
			task.SVNConfig.TagsPath = "tags"
		}
		if strings.TrimSpace(task.SVNConfig.AuthorDomain) == "" {
			task.SVNConfig.AuthorDomain = defaultSVNAuthorDomain(task.SourceRepoURL)
		}
	}
	return task
}

func (s *Service) validateTask(ctx context.Context, task domain.SyncTask) error {
	if task.TaskType != domain.TaskTypeGitMirror && task.TaskType != domain.TaskTypeSVNImport {
		return fmt.Errorf("unsupported taskType %q", task.TaskType)
	}
	switch task.SubmoduleRewriteProtocol {
	case "", domain.SubmoduleRewriteProtocolInherit, domain.SubmoduleRewriteProtocolHTTP, domain.SubmoduleRewriteProtocolSSH:
	default:
		return fmt.Errorf("unsupported submoduleRewriteProtocol %q", task.SubmoduleRewriteProtocol)
	}
	if task.TaskType == domain.TaskTypeSVNImport {
		if !isHTTPRepoURL(task.SourceRepoURL) {
			return fmt.Errorf("svn_import sourceRepoUrl must use http/https")
		}
		if task.RecursiveSubmodules {
			return fmt.Errorf("svn_import does not support recursiveSubmodules")
		}
		if task.TriggerConfig.EnableWebhook {
			return fmt.Errorf("svn_import does not support webhook trigger")
		}
	} else if task.SourceCredentialID == nil || !isHTTPRepoURL(task.SourceRepoURL) {
		return nil
	}
	sourceCredential, err := s.store.CredentialByOptionalID(ctx, task.SourceCredentialID)
	if err != nil {
		return err
	}
	if sourceCredential != nil && sourceCredential.Type == domain.CredentialTypeSSHKey {
		if task.TaskType == domain.TaskTypeSVNImport {
			return fmt.Errorf("svn_import sourceCredentialId cannot reference ssh_key")
		}
		return fmt.Errorf("sourceRepoUrl uses http/https, sourceCredentialId cannot reference ssh_key")
	}
	if task.TaskType == domain.TaskTypeSVNImport && sourceCredential != nil {
		if (sourceCredential.Type == domain.CredentialTypeHTTPSToken || sourceCredential.Type == domain.CredentialTypeAPIToken) &&
			(strings.TrimSpace(sourceCredential.Username) == "" || strings.TrimSpace(sourceCredential.Secret) == "") {
			return fmt.Errorf("svn_import source credentials must include both username and password")
		}
	}
	return nil
}

func isHTTPRepoURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func defaultSVNAuthorDomain(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "svn.local"
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "svn.local"
	}
	return strings.ToLower(host)
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
	cache, err := s.store.GetCache(ctx, id)
	if err != nil {
		return err
	}
	if cache.CachePath != "" {
		_ = os.RemoveAll(cache.CachePath)
	}
	return s.store.DeleteCache(ctx, id)
}

func (s *Service) MoveCache(ctx context.Context, id int64, targetPath string) (domain.RepoCache, error) {
	cache, err := s.store.GetCache(ctx, id)
	if err != nil {
		return domain.RepoCache{}, err
	}
	sourcePath := filepath.Clean(strings.TrimSpace(cache.CachePath))
	if sourcePath == "" {
		return domain.RepoCache{}, fmt.Errorf("cache path is empty")
	}
	destinationPath := filepath.Clean(strings.TrimSpace(targetPath))
	if destinationPath == "" {
		return domain.RepoCache{}, fmt.Errorf("cachePath is required")
	}
	if sourcePath == destinationPath {
		return cache, nil
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.RepoCache{}, fmt.Errorf("source cache path does not exist")
		}
		return domain.RepoCache{}, err
	}
	if !info.IsDir() {
		return domain.RepoCache{}, fmt.Errorf("source cache path is not a directory")
	}
	if existing, statErr := os.Stat(destinationPath); statErr == nil {
		if existing.IsDir() {
			entries, readErr := os.ReadDir(destinationPath)
			if readErr != nil {
				return domain.RepoCache{}, readErr
			}
			if len(entries) > 0 {
				return domain.RepoCache{}, fmt.Errorf("destination cache path already exists and is not empty")
			}
		} else {
			return domain.RepoCache{}, fmt.Errorf("destination cache path already exists")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return domain.RepoCache{}, statErr
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return domain.RepoCache{}, err
	}
	if err := os.Rename(sourcePath, destinationPath); err != nil {
		return domain.RepoCache{}, err
	}
	now := time.Now().UTC()
	cache.CachePath = destinationPath
	cache.LastUsedAt = &now
	if err := s.store.UpsertCache(ctx, cache); err != nil {
		_ = os.Rename(destinationPath, sourcePath)
		return domain.RepoCache{}, err
	}
	return s.store.GetCache(ctx, id)
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

func buildCacheKey(taskType domain.TaskType, source, target string) string {
	if taskType == "" {
		taskType = domain.TaskTypeGitMirror
	}
	sum := sha1.Sum([]byte(string(taskType) + "|" + source + "|" + target))
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

func resolveCachePath(defaultBase string, taskBase string, cacheKey string, suffix string, existing *domain.RepoCache) string {
	if existing != nil && strings.TrimSpace(existing.CachePath) != "" {
		return filepath.Clean(existing.CachePath)
	}
	cacheRoot := resolveCacheBase(defaultBase, taskBase)
	return filepath.Join(cacheRoot, cacheKey+suffix)
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

	if task.TaskType == domain.TaskTypeSVNImport {
		go s.executeSVNTask(context.Background(), task, execution, rootCredentials)
		return execution, nil
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

func (s *Service) executeSVNTask(ctx context.Context, task domain.SyncTask, execution domain.SyncExecution, credentials repositoryCredentials) {
	defer s.clearTaskRunning(task.ID)
	defer s.dropExecutionState(execution.ID)

	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()

	logger := &executionLogger{store: s.store, execution: &execution}
	counters := &executionCounters{}
	logger.onUpdate = func(updated domain.SyncExecution) {
		s.updateExecutionState(execution.ID, func(detail *domain.ExecutionDetail) {
			detail.Execution = updated
		})
	}
	gitClient := s.git.WithLogger(func(format string, args ...any) {
		logger.log(ctx, format, args...)
	})
	runErr := s.syncSVNRepository(ctx, gitClient, execution.ID, task, credentials, counters, logger)

	finished := east8Now()
	execution.FinishedAt = &finished
	execution.RepoCount = counters.repoCount
	execution.CreatedRepoCount = counters.createdRepoCount
	execution.FailedNodeCount = counters.failedNodeCount
	if runErr != nil {
		execution.Status = domain.ExecutionStatusFailed
		logger.log(ctx, "SVN import failed: %v", runErr)
	} else {
		execution.Status = domain.ExecutionStatusSuccess
		logger.log(ctx, "SVN import completed: synchronized %d repository and auto-created %d targets", counters.repoCount, counters.createdRepoCount)
	}
	logger.flush(ctx)
}

func (s *Service) syncSVNRepository(ctx context.Context, gitClient *git.Client, executionID int64, task domain.SyncTask, credentials repositoryCredentials, counters *executionCounters, logger *executionLogger) error {
	node, err := s.createExecutionNode(ctx, domain.SyncExecutionNode{
		ExecutionID:   executionID,
		RepoPath:      "",
		SourceRepoURL: task.SourceRepoURL,
		TargetRepoURL: task.TargetRepoURL,
		Depth:         0,
		Status:        domain.ExecutionStatusRunning,
	})
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	cacheKey := buildCacheKey(task.TaskType, task.SourceRepoURL, task.TargetRepoURL)
	cacheHit := false
	hitCount := 1
	var existingCache *domain.RepoCache
	if existing, getErr := s.store.GetCacheByKey(ctx, cacheKey); getErr == nil {
		cacheHit = true
		hitCount = existing.HitCount + 1
		existingCache = &existing
	} else if getErr != nil && getErr != sql.ErrNoRows {
		return getErr
	}
	cachePath := resolveCachePath(s.cacheDir, task.CacheBasePath, cacheKey, "", existingCache)
	_ = os.MkdirAll(filepath.Dir(cachePath), 0o755)

	logger.log(ctx, "Ensuring target repository for SVN import")
	autoCreated, createDuration, createErr := s.scm.EnsureRepository(ctx, task.TargetRepoURL, task.ProviderConfig, credentials.TargetAPI)
	node.CacheKey = cacheKey
	node.CacheHit = cacheHit
	node.AutoCreated = autoCreated
	node.CreateDuration = createDuration.Milliseconds()
	if createErr != nil {
		node.Status = domain.ExecutionStatusFailed
		node.ErrorMessage = createErr.Error()
		counters.failedNodeCount++
		_ = s.updateExecutionNode(ctx, node)
		return createErr
	}
	if autoCreated {
		counters.createdRepoCount++
		logger.log(ctx, "Created target repository in %dms", node.CreateDuration)
	} else {
		logger.log(ctx, "Verified target repository in %dms", node.CreateDuration)
	}

	logger.log(ctx, "Refreshing SVN import cache")
	cacheHitFromGit, fetchDuration, fetchErr := gitClient.EnsureSVNCheckout(ctx, task.SourceRepoURL, cachePath, task.SVNConfig, credentials.Source)
	cacheSize := dirSize(cachePath)
	node.CacheHit = node.CacheHit || cacheHitFromGit
	node.FetchDuration = fetchDuration.Milliseconds()
	if fetchErr != nil {
		node.Status = domain.ExecutionStatusFailed
		node.ErrorMessage = fetchErr.Error()
		counters.failedNodeCount++
		_ = s.store.UpsertCache(ctx, domain.RepoCache{
			CacheKey:         cacheKey,
			SourceRepoURL:    task.SourceRepoURL,
			AuthContext:      "managed",
			CachePath:        cachePath,
			LastFetchAt:      &now,
			LastUsedAt:       &now,
			HitCount:         hitCount,
			SizeBytes:        cacheSize,
			HealthStatus:     "broken",
			LastErrorMessage: fetchErr.Error(),
		})
		_ = s.store.LinkCacheToTask(ctx, cacheKey, task.ID)
		_ = s.updateExecutionNode(ctx, node)
		return fetchErr
	}

	if err := s.store.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      cacheKey,
		SourceRepoURL: task.SourceRepoURL,
		AuthContext:   "managed",
		CachePath:     cachePath,
		LastFetchAt:   &now,
		LastUsedAt:    &now,
		HitCount:      hitCount,
		SizeBytes:     cacheSize,
		HealthStatus:  "ready",
	}); err != nil {
		return err
	}
	_ = s.store.LinkCacheToTask(ctx, cacheKey, task.ID)

	logger.log(ctx, "Promoting SVN refs into Git branches/tags")
	promoted, promoteErr := gitClient.PromoteSVNRefs(ctx, cachePath, task.SVNConfig)
	if promoteErr != nil {
		node.Status = domain.ExecutionStatusFailed
		node.ErrorMessage = promoteErr.Error()
		counters.failedNodeCount++
		_ = s.updateExecutionNode(ctx, node)
		return promoteErr
	}
	node.ReferenceCommit = promoted.DefaultCommit
	logger.log(ctx, "Prepared %d branches and %d tags from SVN refs", promoted.BranchCount, promoted.TagCount)

	logger.log(ctx, "Pushing Git branches and tags to target")
	pushDuration, pushErr := gitClient.PushBranchesAndTags(ctx, cachePath, task.TargetRepoURL, credentials.TargetGit)
	node.PushDuration = pushDuration.Milliseconds()
	node.Status = executionStatus(pushErr)
	node.ErrorMessage = errorString(pushErr)
	if pushErr != nil {
		counters.failedNodeCount++
		_ = s.updateExecutionNode(ctx, node)
		return pushErr
	}

	logger.log(ctx, "Completed SVN import in %dms", node.PushDuration)
	counters.repoCount++
	return s.updateExecutionNode(ctx, node)
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
	cacheKey := buildCacheKey(task.TaskType, sourceRepoURL, targetRepoURL)
	cacheHit := false
	hitCount := 1
	var existingCache *domain.RepoCache
	if existing, getErr := s.store.GetCacheByKey(ctx, cacheKey); getErr == nil {
		cacheHit = true
		hitCount = existing.HitCount + 1
		existingCache = &existing
	} else if getErr != nil && getErr != sql.ErrNoRows {
		return syncResult{node: node, effectiveCommit: referenceCommit}, getErr
	}
	cachePath := resolveCachePath(s.cacheDir, task.CacheBasePath, cacheKey, ".git", existingCache)
	_ = os.MkdirAll(filepath.Dir(cachePath), 0o755)

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
		_ = s.store.LinkCacheToTask(ctx, cacheKey, task.ID)
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
	_ = s.store.LinkCacheToTask(ctx, cacheKey, task.ID)
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
			childSource := adjustSubmoduleSourceURL(sourceRepoURL, submodule.URL, currentCredentials.Source)
			childTarget := mapSubmoduleTarget(targetRepoURL, submodule.URL, submodule.Path, task.SubmoduleRewriteProtocol)
			logger.log(ctx, "Discovered submodule %s -> %s", submodule.Path, childTarget)
			if childSource != submodule.URL {
				logger.log(ctx, "Adjusted submodule source URL for %s based on source credential type", submodule.Path)
			}
			childResult, childErr := s.syncRepository(ctx, gitClient, executionID, task, submodule.Path, childSource, childTarget, submodule.Commit, depth+1, &node.ID, submoduleCredentials, submoduleCredentials, visited, counters, logger)
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

func mapSubmoduleTarget(parentTarget string, submoduleURL string, submodulePath string, protocol domain.SubmoduleRewriteProtocol) string {
	repoName := submoduleRepoName(submoduleURL)
	if repoName == "" {
		repoName = fallbackSubmoduleRepoName(submodulePath)
	}
	if strings.TrimSpace(repoName) == "" {
		repoName = "submodule"
	}

	switch protocol {
	case "", domain.SubmoduleRewriteProtocolInherit:
		return normalizeGitTarget(replaceTargetRepo(parentTarget, repoName))
	case domain.SubmoduleRewriteProtocolHTTP:
		if converted, ok := toSubmoduleHTTPURL(parentTarget, repoName); ok {
			return converted
		}
	case domain.SubmoduleRewriteProtocolSSH:
		if converted, ok := toSubmoduleSSHURL(parentTarget, repoName); ok {
			return converted
		}
	}
	return normalizeGitTarget(replaceTargetRepo(parentTarget, repoName))
}

func replaceTargetRepo(parentTarget string, repoName string) string {
	if strings.HasPrefix(parentTarget, "git@") {
		return replaceSCPTargetRepo(parentTarget, repoName)
	}
	if strings.HasPrefix(parentTarget, "http://") || strings.HasPrefix(parentTarget, "https://") || strings.HasPrefix(parentTarget, "ssh://") {
		return replaceURLTargetRepo(parentTarget, repoName)
	}
	return replaceLocalTargetRepo(parentTarget, repoName)
}

func toSubmoduleHTTPURL(parentTarget string, repoName string) (string, bool) {
	replaced := replaceTargetRepo(parentTarget, repoName)
	if strings.HasPrefix(replaced, "http://") || strings.HasPrefix(replaced, "https://") {
		return normalizeGitTarget(replaced), true
	}
	preferredScheme := preferredHTTPScheme(parentTarget)
	if converted, ok := toHTTPURL(replaced, preferredScheme); ok {
		return normalizeGitTarget(converted), true
	}
	return "", false
}

func toSubmoduleSSHURL(parentTarget string, repoName string) (string, bool) {
	replaced := replaceTargetRepo(parentTarget, repoName)
	if strings.HasPrefix(replaced, "git@") || strings.HasPrefix(replaced, "ssh://") {
		return normalizeGitTarget(replaced), true
	}
	username := sshUserFromParent(parentTarget)
	if username == "" {
		username = "git"
	}
	if converted, ok := toSSHURL(replaced, username, parentTarget); ok {
		return normalizeGitTarget(converted), true
	}
	return "", false
}

func adjustSubmoduleSourceURL(parentSourceURL string, rawSubmoduleURL string, credential *domain.Credential) string {
	resolved := resolveSubmoduleURL(parentSourceURL, rawSubmoduleURL)
	if credential == nil {
		return resolved
	}
	switch credential.Type {
	case domain.CredentialTypeSSHKey:
		if adjusted, ok := toSSHURL(resolved, credential.Username, parentSourceURL); ok {
			return adjusted
		}
	case domain.CredentialTypeHTTPSToken, domain.CredentialTypeAPIToken:
		preferredScheme := preferredHTTPScheme(parentSourceURL)
		if adjusted, ok := toHTTPURL(resolved, preferredScheme); ok {
			return adjusted
		}
	}
	return resolved
}

func resolveSubmoduleURL(parentSourceURL string, rawSubmoduleURL string) string {
	trimmed := strings.TrimSpace(rawSubmoduleURL)
	if trimmed == "" {
		return rawSubmoduleURL
	}
	if isSCPLikeGitURL(trimmed) {
		return trimmed
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" {
		return trimmed
	}
	if !strings.HasPrefix(trimmed, "./") && !strings.HasPrefix(trimmed, "../") {
		return trimmed
	}

	if isSCPLikeGitURL(parentSourceURL) {
		hostPart, repoPath, ok := splitSCPLikeGitURL(parentSourceURL)
		if !ok {
			return trimmed
		}
		base := path.Dir(strings.TrimPrefix(repoPath, "/"))
		joined := path.Clean(path.Join(base, trimmed))
		if strings.HasPrefix(joined, "../") {
			return trimmed
		}
		return hostPart + ":" + joined
	}
	parent, err := url.Parse(parentSourceURL)
	if err != nil || parent.Scheme == "" {
		return trimmed
	}
	base := path.Dir(parent.Path)
	parent.Path = path.Clean(path.Join(base, trimmed))
	return parent.String()
}

func toSSHURL(raw string, username string, parentSourceURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	user := strings.TrimSpace(sshUserFromParent(parentSourceURL))
	if user == "" {
		user = strings.TrimSpace(username)
	}
	if user == "" {
		user = "git"
	}
	host := parsed.Host
	if parentHost, ok := sshAuthorityFromParent(parentSourceURL); ok {
		host = parentHost
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(parsed.Path) == "" {
		return "", false
	}
	return fmt.Sprintf("ssh://%s@%s%s", user, host, parsed.Path), true
}

func sshUserFromParent(parentSourceURL string) string {
	trimmed := strings.TrimSpace(parentSourceURL)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme == "ssh" && parsed.User != nil {
		return strings.TrimSpace(parsed.User.Username())
	}
	if isSCPLikeGitURL(trimmed) {
		hostPart, _, ok := splitSCPLikeGitURL(trimmed)
		if !ok {
			return ""
		}
		if at := strings.LastIndex(hostPart, "@"); at > 0 {
			return strings.TrimSpace(hostPart[:at])
		}
	}
	return ""
}

func sshAuthorityFromParent(parentSourceURL string) (string, bool) {
	trimmed := strings.TrimSpace(parentSourceURL)
	if trimmed == "" {
		return "", false
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme == "ssh" && strings.TrimSpace(parsed.Host) != "" {
		return parsed.Host, true
	}
	if isSCPLikeGitURL(trimmed) {
		hostPart, _, ok := splitSCPLikeGitURL(trimmed)
		if !ok {
			return "", false
		}
		if at := strings.LastIndex(hostPart, "@"); at >= 0 && at+1 < len(hostPart) {
			return hostPart[at+1:], true
		}
		return hostPart, true
	}
	return "", false
}

func toHTTPURL(raw string, preferredScheme string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	if parsed, err := url.Parse(trimmed); err == nil {
		switch parsed.Scheme {
		case "http", "https":
			return trimmed, true
		case "ssh":
			if parsed.Host == "" || parsed.Path == "" {
				return "", false
			}
			parsed.Scheme = preferredScheme
			parsed.User = nil
			return parsed.String(), true
		}
	}
	if isSCPLikeGitURL(trimmed) {
		hostPart, repoPath, ok := splitSCPLikeGitURL(trimmed)
		if !ok {
			return "", false
		}
		host := hostPart
		if at := strings.LastIndex(hostPart, "@"); at >= 0 && at+1 < len(hostPart) {
			host = hostPart[at+1:]
		}
		return fmt.Sprintf("%s://%s/%s", preferredScheme, host, strings.TrimPrefix(repoPath, "/")), true
	}
	return "", false
}

func preferredHTTPScheme(parentSourceURL string) string {
	if parsed, err := url.Parse(parentSourceURL); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		return parsed.Scheme
	}
	return "https"
}

func isSCPLikeGitURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.Contains(trimmed, "@") && strings.Contains(trimmed, ":") && !strings.Contains(trimmed, "://")
}

func splitSCPLikeGitURL(raw string) (string, string, bool) {
	trimmed := strings.TrimSpace(raw)
	colon := strings.Index(trimmed, ":")
	if colon <= 0 || colon+1 >= len(trimmed) {
		return "", "", false
	}
	return trimmed[:colon], trimmed[colon+1:], true
}

func normalizeGitTarget(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "ssh://") || strings.HasPrefix(target, "git@") {
		return target
	}
	return filepath.ToSlash(target)
}

func submoduleRepoName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "git@") {
		colon := strings.LastIndex(trimmed, ":")
		if colon >= 0 && colon+1 < len(trimmed) {
			return gitRepoBaseName(trimmed[colon+1:])
		}
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Path != "" {
		return gitRepoBaseName(parsed.Path)
	}
	return gitRepoBaseName(trimmed)
}

func fallbackSubmoduleRepoName(submodulePath string) string {
	trimmed := strings.Trim(strings.TrimSpace(submodulePath), "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	return gitRepoBaseName(parts[len(parts)-1])
}

func gitRepoBaseName(raw string) string {
	base := filepath.Base(strings.Trim(strings.ReplaceAll(raw, "\\", "/"), "/"))
	base = strings.TrimSuffix(base, ".git")
	return strings.TrimSpace(base)
}

func replaceLocalTargetRepo(parentTarget string, repoName string) string {
	dir := filepath.Dir(parentTarget)
	return filepath.Join(dir, repoName+".git")
}

func replaceURLTargetRepo(parentTarget string, repoName string) string {
	parsed, err := url.Parse(parentTarget)
	if err != nil || parsed.Path == "" {
		return parentTarget
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	slash := strings.LastIndex(path, "/")
	if slash < 0 {
		parsed.Path = "/" + repoName + ".git"
		return parsed.String()
	}
	parsed.Path = path[:slash+1] + repoName + ".git"
	return parsed.String()
}

func replaceSCPTargetRepo(parentTarget string, repoName string) string {
	colon := strings.Index(parentTarget, ":")
	if colon < 0 || colon+1 >= len(parentTarget) {
		return parentTarget
	}
	remainder := parentTarget[colon+1:]
	slash := strings.LastIndex(remainder, "/")
	if slash < 0 {
		return parentTarget[:colon+1] + repoName + ".git"
	}
	return parentTarget[:colon+1] + remainder[:slash+1] + repoName + ".git"
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

package service

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
		SummaryLog:  "Execution started",
	})
	if err != nil {
		return domain.SyncExecution{}, err
	}

	now := time.Now().UTC()
	cacheKey := buildCacheKey(task.SourceRepoURL, task.TargetRepoURL)
	cachePath := filepath.Join(s.cacheDir, cacheKey+".git")
	_ = os.MkdirAll(s.cacheDir, 0o755)

	cacheHit := false
	hitCount := 1
	if existing, getErr := s.store.GetCacheByKey(ctx, cacheKey); getErr == nil {
		cacheHit = true
		hitCount = existing.HitCount + 1
	} else if getErr != nil && getErr != sql.ErrNoRows {
		return domain.SyncExecution{}, getErr
	}

	autoCreated, createDuration, createErr := s.scm.EnsureRepository(ctx, task.TargetRepoURL, task.ProviderConfig, targetCredential)
	if createErr != nil {
		finished := time.Now().UTC()
		execution.Status = domain.ExecutionStatusFailed
		execution.FinishedAt = &finished
		execution.FailedNodeCount = 1
		execution.SummaryLog = createErr.Error()
		if _, err := s.store.CreateExecutionNode(ctx, domain.SyncExecutionNode{
			ExecutionID:    execution.ID,
			RepoPath:       "",
			SourceRepoURL:  task.SourceRepoURL,
			TargetRepoURL:  task.TargetRepoURL,
			Depth:          0,
			CacheKey:       cacheKey,
			CacheHit:       cacheHit,
			AutoCreated:    false,
			CreateDuration: createDuration.Milliseconds(),
			Status:         domain.ExecutionStatusFailed,
			ErrorMessage:   createErr.Error(),
		}); err != nil {
			return domain.SyncExecution{}, err
		}
		if err := s.store.UpdateExecution(ctx, execution); err != nil {
			return domain.SyncExecution{}, err
		}
		return execution, createErr
	}

	cacheHitFromGit, fetchDuration, fetchErr := s.git.EnsureMirror(ctx, task.SourceRepoURL, cachePath)
	cacheHit = cacheHit || cacheHitFromGit
	if fetchErr != nil {
		finished := time.Now().UTC()
		execution.Status = domain.ExecutionStatusFailed
		execution.FinishedAt = &finished
		execution.FailedNodeCount = 1
		execution.SummaryLog = fetchErr.Error()
		_ = s.store.UpsertCache(ctx, domain.RepoCache{
			CacheKey:         cacheKey,
			SourceRepoURL:    task.SourceRepoURL,
			AuthContext:      "managed",
			CachePath:        cachePath,
			LastFetchAt:      &now,
			LastUsedAt:       &now,
			HitCount:         hitCount,
			HealthStatus:     "broken",
			LastErrorMessage: fetchErr.Error(),
		})
		if _, err := s.store.CreateExecutionNode(ctx, domain.SyncExecutionNode{
			ExecutionID:    execution.ID,
			RepoPath:       "",
			SourceRepoURL:  task.SourceRepoURL,
			TargetRepoURL:  task.TargetRepoURL,
			Depth:          0,
			CacheKey:       cacheKey,
			CacheHit:       cacheHit,
			AutoCreated:    autoCreated,
			CreateDuration: createDuration.Milliseconds(),
			FetchDuration:  fetchDuration.Milliseconds(),
			Status:         domain.ExecutionStatusFailed,
			ErrorMessage:   fetchErr.Error(),
		}); err != nil {
			return domain.SyncExecution{}, err
		}
		if err := s.store.UpdateExecution(ctx, execution); err != nil {
			return domain.SyncExecution{}, err
		}
		return execution, fetchErr
	}

	pushDuration, pushErr := s.git.MirrorPush(ctx, cachePath, task.TargetRepoURL)
	if err := s.store.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      cacheKey,
		SourceRepoURL: task.SourceRepoURL,
		AuthContext:   "managed",
		CachePath:     cachePath,
		LastFetchAt:   &now,
		LastUsedAt:    &now,
		HitCount:      hitCount,
		SizeBytes:     0,
		HealthStatus:  "ready",
	}); err != nil {
		return domain.SyncExecution{}, err
	}

	if _, err := s.store.CreateExecutionNode(ctx, domain.SyncExecutionNode{
		ExecutionID:     execution.ID,
		RepoPath:        "",
		SourceRepoURL:   task.SourceRepoURL,
		TargetRepoURL:   task.TargetRepoURL,
		ReferenceCommit: s.git.ResolveHEAD(ctx, cachePath),
		Depth:           0,
		CacheKey:        cacheKey,
		CacheHit:        cacheHit,
		AutoCreated:     autoCreated,
		CreateDuration:  createDuration.Milliseconds(),
		FetchDuration:   fetchDuration.Milliseconds(),
		PushDuration:    pushDuration.Milliseconds(),
		Status:          executionStatus(pushErr),
		ErrorMessage:    errorString(pushErr),
	}); err != nil {
		return domain.SyncExecution{}, err
	}

	finished := time.Now().UTC()
	execution.Status = executionStatus(pushErr)
	execution.FinishedAt = &finished
	if pushErr == nil {
		execution.RepoCount = 1
		if autoCreated {
			execution.CreatedRepoCount = 1
		}
		execution.SummaryLog = "Mirror sync completed with all branches, tags, and refs."
	} else {
		execution.FailedNodeCount = 1
		execution.SummaryLog = pushErr.Error()
	}
	if err := s.store.UpdateExecution(ctx, execution); err != nil {
		return domain.SyncExecution{}, err
	}
	if pushErr != nil {
		return execution, pushErr
	}
	return execution, nil
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

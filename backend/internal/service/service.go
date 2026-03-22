package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reposync/backend/internal/domain"
	"reposync/backend/internal/store"
)

type Service struct {
	store    *store.Store
	cacheDir string
	locks    sync.Map
}

func New(db *store.Store, cacheDir string) *Service {
	return &Service{store: db, cacheDir: cacheDir}
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
	_ = os.MkdirAll(cachePath, 0o755)
	if err := s.store.UpsertCache(ctx, domain.RepoCache{
		CacheKey:      cacheKey,
		SourceRepoURL: task.SourceRepoURL,
		AuthContext:   "managed",
		CachePath:     cachePath,
		LastFetchAt:   &now,
		LastUsedAt:    &now,
		HitCount:      1,
		SizeBytes:     0,
		HealthStatus:  "ready",
	}); err != nil {
		return domain.SyncExecution{}, err
	}

	if _, err := s.store.CreateExecutionNode(ctx, domain.SyncExecutionNode{
		ExecutionID:   execution.ID,
		RepoPath:      "",
		SourceRepoURL: task.SourceRepoURL,
		TargetRepoURL: task.TargetRepoURL,
		Depth:         0,
		CacheKey:      cacheKey,
		CacheHit:      false,
		AutoCreated:   true,
		FetchDuration: 20,
		PushDuration:  20,
		Status:        domain.ExecutionStatusSuccess,
	}); err != nil {
		return domain.SyncExecution{}, err
	}

	finished := time.Now().UTC()
	execution.Status = domain.ExecutionStatusSuccess
	execution.FinishedAt = &finished
	execution.RepoCount = 1
	execution.CreatedRepoCount = 1
	execution.SummaryLog = "Execution recorded. Real git mirror and provider integration are the next implementation step."
	if err := s.store.UpdateExecution(ctx, execution); err != nil {
		return domain.SyncExecution{}, err
	}
	return execution, nil
}

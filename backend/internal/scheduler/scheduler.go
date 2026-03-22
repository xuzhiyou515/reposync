package scheduler

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"reposync/backend/internal/domain"
)

type Runner interface {
	RunTask(ctx context.Context, taskID int64, trigger domain.TriggerType) (domain.SyncExecution, error)
}

type Scheduler struct {
	cron    *cron.Cron
	runner  Runner
	mu      sync.Mutex
	entries map[int64]cron.EntryID
}

func New(runner Runner) *Scheduler {
	c := cron.New(cron.WithSeconds())
	s := &Scheduler{
		cron:    c,
		runner:  runner,
		entries: map[int64]cron.EntryID{},
	}
	c.Start()
	return s
}

func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

func (s *Scheduler) SyncTask(task domain.SyncTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id, ok := s.entries[task.ID]; ok {
		s.cron.Remove(id)
		delete(s.entries, task.ID)
	}
	if !task.Enabled || !task.TriggerConfig.EnableSchedule || task.TriggerConfig.Cron == "" {
		return nil
	}
	entryID, err := s.cron.AddFunc(task.TriggerConfig.Cron, func() {
		if _, runErr := s.runner.RunTask(context.Background(), task.ID, domain.TriggerSchedule); runErr != nil {
			log.Printf("scheduler run task %d: %v", task.ID, runErr)
		}
	})
	if err != nil {
		return err
	}
	s.entries[task.ID] = entryID
	return nil
}

func (s *Scheduler) RemoveTask(taskID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.entries[taskID]; ok {
		s.cron.Remove(id)
		delete(s.entries, taskID)
	}
}

func (s *Scheduler) LoadTasks(tasks []domain.SyncTask) error {
	for _, task := range tasks {
		if err := s.SyncTask(task); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) JobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *Scheduler) EnrichTask(task domain.SyncTask) domain.SyncTask {
	s.mu.Lock()
	defer s.mu.Unlock()

	task.ScheduleCron = ""
	task.NextRunAt = nil

	if !task.Enabled {
		return task
	}
	if !task.TriggerConfig.EnableSchedule {
		return task
	}
	if strings.TrimSpace(task.TriggerConfig.Cron) == "" {
		return task
	}
	task.ScheduleCron = task.TriggerConfig.Cron

	entryID, ok := s.entries[task.ID]
	if !ok {
		return task
	}

	entry := s.cron.Entry(entryID)
	if !entry.Valid() {
		return task
	}
	if !entry.Next.IsZero() {
		next := entry.Next.In(time.Local)
		task.NextRunAt = &next
	}
	return task
}

func (s *Scheduler) EnrichTasks(tasks []domain.SyncTask) []domain.SyncTask {
	items := make([]domain.SyncTask, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, s.EnrichTask(task))
	}
	return items
}

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

func (s *Scheduler) Status(task domain.SyncTask) domain.ScheduleStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := domain.ScheduleStatus{
		TaskID:   task.ID,
		TaskName: task.Name,
		Enabled:  task.Enabled,
		Cron:     task.TriggerConfig.Cron,
	}

	if !task.Enabled {
		status.Reason = "task is disabled"
		return status
	}
	if !task.TriggerConfig.EnableSchedule {
		status.Reason = "schedule is disabled"
		return status
	}
	if strings.TrimSpace(task.TriggerConfig.Cron) == "" {
		status.Reason = "cron expression is empty"
		return status
	}

	entryID, ok := s.entries[task.ID]
	if !ok {
		status.Reason = "schedule is not registered"
		return status
	}

	entry := s.cron.Entry(entryID)
	status.Registered = entry.Valid()
	if !entry.Valid() {
		status.Reason = "schedule entry is invalid"
		return status
	}
	if !entry.Next.IsZero() {
		next := entry.Next.In(time.Local)
		status.NextRunAt = &next
	}
	if !entry.Prev.IsZero() {
		prev := entry.Prev.In(time.Local)
		status.PreviousRun = &prev
	}
	return status
}

func (s *Scheduler) Statuses(tasks []domain.SyncTask) []domain.ScheduleStatus {
	items := make([]domain.ScheduleStatus, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, s.Status(task))
	}
	return items
}

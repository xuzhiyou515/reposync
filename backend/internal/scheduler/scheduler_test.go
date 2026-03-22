package scheduler

import (
	"context"
	"testing"

	"reposync/backend/internal/domain"
)

type fakeRunner struct{}

func (f fakeRunner) RunTask(ctx context.Context, taskID int64, trigger domain.TriggerType) (domain.SyncExecution, error) {
	return domain.SyncExecution{}, nil
}

func TestSyncTaskRegistersEnabledSchedule(t *testing.T) {
	s := New(fakeRunner{})
	defer s.Stop()

	err := s.SyncTask(domain.SyncTask{
		ID:      1,
		Enabled: true,
		TriggerConfig: domain.TriggerConfig{
			EnableSchedule: true,
			Cron:           "*/30 * * * * *",
		},
	})
	if err != nil {
		t.Fatalf("sync task: %v", err)
	}
	if s.JobCount() != 1 {
		t.Fatalf("expected 1 job, got %d", s.JobCount())
	}
}

func TestSyncTaskRemovesDisabledSchedule(t *testing.T) {
	s := New(fakeRunner{})
	defer s.Stop()

	_ = s.SyncTask(domain.SyncTask{
		ID:      1,
		Enabled: true,
		TriggerConfig: domain.TriggerConfig{
			EnableSchedule: true,
			Cron:           "*/30 * * * * *",
		},
	})
	_ = s.SyncTask(domain.SyncTask{
		ID:      1,
		Enabled: false,
		TriggerConfig: domain.TriggerConfig{
			EnableSchedule: true,
			Cron:           "*/30 * * * * *",
		},
	})
	if s.JobCount() != 0 {
		t.Fatalf("expected 0 jobs, got %d", s.JobCount())
	}
}

func TestStatusReportsRegisteredSchedule(t *testing.T) {
	s := New(fakeRunner{})
	defer s.Stop()

	task := domain.SyncTask{
		ID:      7,
		Name:    "scheduled-task",
		Enabled: true,
		TriggerConfig: domain.TriggerConfig{
			EnableSchedule: true,
			Cron:           "*/30 * * * * *",
		},
	}
	if err := s.SyncTask(task); err != nil {
		t.Fatalf("sync task: %v", err)
	}

	status := s.Status(task)
	if !status.Registered {
		t.Fatalf("expected schedule to be registered")
	}
	if status.NextRunAt == nil {
		t.Fatalf("expected next run to be populated")
	}
	if status.Reason != "" {
		t.Fatalf("expected empty reason for registered schedule, got %q", status.Reason)
	}
}

func TestStatusReportsDisabledReason(t *testing.T) {
	s := New(fakeRunner{})
	defer s.Stop()

	status := s.Status(domain.SyncTask{
		ID:      8,
		Name:    "disabled-task",
		Enabled: false,
		TriggerConfig: domain.TriggerConfig{
			EnableSchedule: true,
			Cron:           "*/30 * * * * *",
		},
	})
	if status.Registered {
		t.Fatalf("expected disabled schedule not to be registered")
	}
	if status.Reason == "" {
		t.Fatalf("expected disabled reason")
	}
}

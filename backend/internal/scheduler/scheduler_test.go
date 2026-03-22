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

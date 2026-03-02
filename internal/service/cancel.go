package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"you2midi/internal/domain"
	"you2midi/internal/metrics"
	"you2midi/internal/runner"
)

// CancelJob transitions a running or queued job to cancelled.
func (s *TranscriptionService) CancelJob(ctx context.Context, jobID string) error {
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("service: %w: %v", ErrJobMissing, err)
	}
	if job.IsTerminal() {
		return fmt.Errorf("service: job %q is already in terminal state %s", jobID, job.State)
	}

	if cancelled := s.cancelRunning(jobID); cancelled {
		job.State = domain.JobStateCancelled
	}

	job.State = domain.JobStateCancelled
	job.ErrorCode = string(domain.ErrCancelled)
	job.ErrorMessage = domain.UserMessage(domain.ErrCancelled)
	return s.jobs.Update(ctx, job)
}

// StartupOrphanSweep marks in-flight jobs from a crashed process as failed.
func (s *TranscriptionService) StartupOrphanSweep(ctx context.Context) {
	for _, state := range []domain.JobState{domain.JobStateRunning, domain.JobStateRetrying} {
		orphans, err := s.jobs.ListByState(ctx, state)
		if err != nil {
			slog.Error("orphan sweep list failed",
				slog.String("state", string(state)),
				slog.String("error", err.Error()),
			)
			continue
		}
		for _, job := range orphans {
			job.State = domain.JobStateFailed
			job.ErrorCode = string(domain.ErrTransientCrash)
			job.ErrorMessage = "Application restarted while job was running."
			if updateErr := s.jobs.Update(ctx, job); updateErr != nil {
				slog.Error("orphan sweep update failed",
					slog.String("job_id", job.ID),
					slog.String("error", updateErr.Error()),
				)
				continue
			}
			if job.WorkspacePath != "" {
				if removeErr := os.RemoveAll(job.WorkspacePath); removeErr != nil {
					slog.Error("orphan workspace cleanup failed",
						slog.String("job_id", job.ID),
						slog.String("workspace_path", job.WorkspacePath),
						slog.String("error", removeErr.Error()),
					)
				}
			}
		}
	}
}

func (s *TranscriptionService) registerRunning(jobID string, cancel context.CancelFunc) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	s.runningCancels[jobID] = cancel
}

func (s *TranscriptionService) unregisterRunning(jobID string) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	delete(s.runningCancels, jobID)
}

func (s *TranscriptionService) cancelRunning(jobID string) bool {
	s.runningMu.Lock()
	cancel, ok := s.runningCancels[jobID]
	s.runningMu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

func (s *TranscriptionService) isJobCancelled(ctx context.Context, jobID string) bool {
	if err := ctx.Err(); err == context.Canceled {
		return true
	}
	job, err := s.jobs.Get(ctx, jobID)
	return err == nil && job.State == domain.JobStateCancelled
}

func (s *TranscriptionService) cleanupLoop(ctx context.Context) {
	ttl := s.cfg.Workspace.TempFileTTL
	if ttl <= 0 {
		return
	}
	interval := ttl / 2
	if interval < time.Minute {
		interval = time.Minute
	}
	if interval > 30*time.Minute {
		interval = 30 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.purgeExpiredJobs(ctx)
		}
	}
}

func (s *TranscriptionService) purgeExpiredJobs(ctx context.Context) {
	ttl := s.cfg.Workspace.TempFileTTL
	if ttl <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-ttl)

	jobs, err := s.jobs.List(ctx)
	if err != nil {
		slog.Warn("cleanup list jobs failed", slog.String("error", err.Error()))
		return
	}

	for _, job := range jobs {
		if !job.IsTerminal() {
			continue
		}
		reference := job.UpdatedAt
		if job.CompletedAt != nil {
			reference = *job.CompletedAt
		}
		if reference.After(cutoff) {
			continue
		}

		if job.WorkspacePath != "" {
			if err := os.RemoveAll(job.WorkspacePath); err != nil {
				slog.Warn("cleanup workspace failed",
					slog.String("job_id", job.ID),
					slog.String("workspace", job.WorkspacePath),
					slog.String("error", err.Error()),
				)
			}
		}

		// Best-effort output cleanup when output is outside workspace.
		if job.OutputPath != "" && !runner.IsWithinRoot(job.OutputPath, job.WorkspacePath) {
			if err := os.Remove(job.OutputPath); err != nil && !os.IsNotExist(err) {
				slog.Warn("cleanup output file failed",
					slog.String("job_id", job.ID),
					slog.String("output_path", job.OutputPath),
					slog.String("error", err.Error()),
				)
			}
		}

		if err := s.jobs.Delete(ctx, job.ID); err != nil {
			slog.Warn("cleanup delete job failed",
				slog.String("job_id", job.ID),
				slog.String("error", err.Error()),
			)
			continue
		}
		metrics.AddCounter("cleanup.jobs_deleted", 1)
	}
}

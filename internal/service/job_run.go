package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"you2midi/internal/domain"
	"you2midi/internal/metrics"
)

// SubmitYouTube creates a new job for a YouTube URL and enqueues it.
func (s *TranscriptionService) SubmitYouTube(ctx context.Context, url string, opts domain.TranscribeOptions) (*domain.Job, error) {
	if url == "" {
		return nil, fmt.Errorf("service: youtube url is required")
	}
	preferredDevice := opts.Device
	if preferredDevice == "" {
		preferredDevice = "auto"
	}
	actualDevice := s.queueSafeDevice(s.resolveDevice(opts.Device))

	job := &domain.Job{
		State:       domain.JobStateQueued,
		YoutubeURL:  url,
		Engine:      s.primary.Name(),
		Device:      actualDevice,
		StartSec:    opts.StartSec,
		MaxAttempts: s.cfg.Engine.MaxAttempts,
	}
	created, err := s.jobs.Create(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("service: create job: %w", err)
	}

	if ok := s.tryEnqueueJob(created.ID, created.Device); !ok {
		if delErr := s.jobs.Delete(ctx, created.ID); delErr != nil {
			return nil, fmt.Errorf("service: %w and cleanup failed: %v", ErrQueueFull, delErr)
		}
		return nil, fmt.Errorf("service: %w", ErrQueueFull)
	}

	slog.Info("job device resolved",
		slog.String("job_id", created.ID),
		slog.String("preferred_device", preferredDevice),
		slog.String("actual_device", created.Device),
		slog.String("resolved_device", s.cfg.ResolvedDevice),
	)

	return created, nil
}

// RunJob executes one queued job synchronously.
func (s *TranscriptionService) RunJob(ctx context.Context, jobID string) error {
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("service: get job %q: %w", jobID, err)
	}
	if job.State != domain.JobStateQueued && job.State != domain.JobStateRetrying {
		return nil
	}
	if !job.UpdatedAt.IsZero() {
		queueWait := time.Since(job.UpdatedAt)
		if queueWait > 0 {
			metrics.Record("queue_wait", queueWait, jobID)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.registerRunning(jobID, cancel)
	defer func() {
		s.unregisterRunning(jobID)
		cancel()
	}()

	job.State = domain.JobStateRunning
	job.Attempt++
	if err := s.jobs.Update(runCtx, job); err != nil {
		return fmt.Errorf("service: set running state: %w", err)
	}
	metrics.AddCounter("jobs.started", 1)

	jobErr := s.runPipeline(runCtx, job)
	if jobErr == nil {
		metrics.AddCounter("jobs.completed", 1)
		return nil
	}

	engineErr, ok := jobErr.(*domain.EngineError)
	if !ok {
		engineErr = domain.NewEngineError(domain.ErrUnknown, jobErr)
	}
	if engineErr.Code == domain.ErrUnknown {
		engineErr.Message = appendUnknownLogHint(engineErr.Message)
	}

	if current, getErr := s.jobs.Get(ctx, jobID); getErr == nil && current.State == domain.JobStateCancelled {
		return domain.NewEngineError(domain.ErrCancelled, context.Canceled)
	}

	if engineErr.Code == domain.ErrCancelled || runCtx.Err() == context.Canceled {
		job.State = domain.JobStateCancelled
		job.ErrorCode = string(domain.ErrCancelled)
		job.ErrorMessage = domain.UserMessage(domain.ErrCancelled)
		if err := s.jobs.Update(ctx, job); err != nil {
			return fmt.Errorf("service: persist cancelled state: %w", err)
		}
		return domain.NewEngineError(domain.ErrCancelled, context.Canceled)
	}

	if engineErr.FallbackStrategy == "cpu" && job.Device != "cpu" {
		job.Device = "cpu"
		job.State = domain.JobStateRetrying
		job.ErrorCode = string(engineErr.Code)
		job.ErrorMessage = engineErr.Message
		if err := s.jobs.Update(ctx, job); err != nil {
			return fmt.Errorf("service: persist retry state: %w", err)
		}
		return engineErr
	}

	if engineErr.Retryable && job.Attempt < job.MaxAttempts {
		job.State = domain.JobStateRetrying
		job.ErrorCode = string(engineErr.Code)
		job.ErrorMessage = engineErr.Message
	} else if job.Attempt >= job.MaxAttempts {
		job.State = domain.JobStateFailed
		job.ErrorCode = string(domain.ErrRetryExhausted)
		job.ErrorMessage = domain.UserMessage(domain.ErrRetryExhausted)
	} else {
		job.State = domain.JobStateFailed
		job.ErrorCode = string(engineErr.Code)
		job.ErrorMessage = engineErr.Message
	}

	if err := s.jobs.Update(ctx, job); err != nil {
		return fmt.Errorf("service: persist failed state: %w", err)
	}
	if job.State == domain.JobStateFailed {
		metrics.AddCounter("jobs.failed", 1)
	}
	return engineErr
}

func (s *TranscriptionService) retryDelay(ctx context.Context, jobID string) (time.Duration, bool) {
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil || job.State != domain.JobStateRetrying {
		return 0, false
	}
	if job.Device == "cpu" && job.ErrorCode == string(domain.ErrOOM) {
		return 0, true
	}
	delay := time.Duration(job.Attempt) * time.Second
	if delay < time.Second {
		delay = time.Second
	}
	if delay > 15*time.Second {
		delay = 15 * time.Second
	}
	return delay, true
}

func (s *TranscriptionService) scheduleRetry(ctx context.Context, jobID string, delay time.Duration) {
	go func() {
		if delay <= 0 {
			if err := s.enqueueJob(ctx, jobID); err != nil {
				slog.Error("failed to enqueue retry",
					slog.String("job_id", jobID),
					slog.String("error", err.Error()),
				)
			}
			return
		}

		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := s.enqueueJob(ctx, jobID); err != nil {
			slog.Error("failed to enqueue retry",
				slog.String("job_id", jobID),
				slog.String("error", err.Error()),
			)
		}
	}()
}

func (s *TranscriptionService) tryEnqueueJob(jobID, device string) bool {
	queue := s.queueForDevice(device)
	select {
	case queue <- jobID:
		metrics.AddCounter("jobs.enqueued", 1)
		s.recordQueueDepths()
		return true
	default:
		return false
	}
}

func (s *TranscriptionService) enqueueJob(ctx context.Context, jobID string) error {
	job, err := s.jobs.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("service: get job for enqueue %q: %w", jobID, err)
	}
	queue := s.queueForDevice(job.Device)

	select {
	case queue <- jobID:
		metrics.AddCounter("jobs.enqueued", 1)
		s.recordQueueDepths()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *TranscriptionService) queueForDevice(device string) chan string {
	if s.queueNameForDevice(device) == "gpu" {
		return s.gpuQueue
	}
	return s.cpuQueue
}

func (s *TranscriptionService) queueSafeDevice(device string) string {
	if device == "" || device == "auto" {
		return "cpu"
	}
	if device == "cuda" && s.queueNameForDevice(device) != "gpu" {
		return "cpu"
	}
	return device
}

func appendUnknownLogHint(message string) string {
	logPath := strings.TrimSpace(os.Getenv("YOU2MIDI_BACKEND_LOG_PATH"))
	if logPath == "" {
		return message
	}
	hint := "See logs: " + logPath
	if strings.Contains(message, hint) {
		return message
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = domain.UserMessage(domain.ErrUnknown)
	}
	return message + " | " + hint
}

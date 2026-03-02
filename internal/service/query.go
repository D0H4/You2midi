package service

import (
	"context"
	"fmt"
	"time"

	"you2midi/internal/domain"
)

// ListJobs returns all jobs ordered newest-first.
func (s *TranscriptionService) ListJobs(ctx context.Context) ([]*domain.Job, error) {
	jobs, err := s.jobs.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: list jobs: %w", err)
	}
	return jobs, nil
}

// ListJobsPage returns jobs using repository-level pagination when available.
func (s *TranscriptionService) ListJobsPage(ctx context.Context, limit int, before *time.Time) ([]*domain.Job, error) {
	if pager, ok := s.jobs.(domain.JobPageRepository); ok {
		jobs, err := pager.ListPage(ctx, limit, before)
		if err != nil {
			return nil, fmt.Errorf("service: list jobs page: %w", err)
		}
		return jobs, nil
	}

	// Fallback for repositories that only support full scans (e.g. simple test doubles).
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return nil, err
	}

	if before != nil {
		filtered := make([]*domain.Job, 0, len(jobs))
		for _, job := range jobs {
			if job.CreatedAt.Before(*before) {
				filtered = append(filtered, job)
			}
		}
		jobs = filtered
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

// GetJob retrieves a single job by ID.
func (s *TranscriptionService) GetJob(ctx context.Context, id string) (*domain.Job, error) {
	job, err := s.jobs.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: %w: %v", ErrJobMissing, err)
	}
	return job, nil
}

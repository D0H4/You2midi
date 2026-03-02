package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"you2midi/internal/config"
	"you2midi/internal/domain"
	"you2midi/internal/metrics"
	"you2midi/internal/runner"
)

// TranscriptionService orchestrates the full YouTube to MIDI pipeline.
type TranscriptionService struct {
	cfg        *config.Config
	jobs       domain.JobRepository
	cache      domain.CacheRepository
	primary    domain.TranscriptionEngine
	fallback   domain.TranscriptionEngine
	downloader Downloader
	runner     runner.ProcessRunner

	cpuQueue chan string
	gpuQueue chan string

	startOnce sync.Once
	startErr  error

	runningMu      sync.Mutex
	runningCancels map[string]context.CancelFunc
}

// Downloader fetches audio from a URL and writes it to an io.Reader.
type Downloader interface {
	Download(ctx context.Context, url string, destDir string) (audioPath string, err error)
}

// HashingDownloader optionally returns a precomputed content hash with the audio path.
// Implementations can use this to avoid an extra hash pass in service pipeline.
type HashingDownloader interface {
	DownloadAndHash(ctx context.Context, url string, destDir string) (audioPath string, audioHash string, err error)
}

// New creates a TranscriptionService.
func New(
	cfg *config.Config,
	jobs domain.JobRepository,
	cache domain.CacheRepository,
	primary domain.TranscriptionEngine,
	fallback domain.TranscriptionEngine,
	dl Downloader,
	r runner.ProcessRunner,
) *TranscriptionService {
	queueSize := cfg.Engine.QueueSize
	if queueSize < 1 {
		queueSize = 128
	}

	return &TranscriptionService{
		cfg:            cfg,
		jobs:           jobs,
		cache:          cache,
		primary:        primary,
		fallback:       fallback,
		downloader:     dl,
		runner:         r,
		cpuQueue:       make(chan string, queueSize),
		gpuQueue:       make(chan string, queueSize),
		runningCancels: map[string]context.CancelFunc{},
	}
}

// Start launches background workers and resumes queued jobs.
func (s *TranscriptionService) Start(ctx context.Context) error {
	s.startOnce.Do(func() {
		cpuWorkers, gpuWorkers := s.workerCounts()
		for i := 0; i < cpuWorkers; i++ {
			go s.workerLoop(ctx, "cpu", i+1)
		}
		for i := 0; i < gpuWorkers; i++ {
			go s.workerLoop(ctx, "gpu", i+1)
		}
		go s.cleanupLoop(ctx)
		s.purgeExpiredJobs(ctx)
		s.startErr = s.resumePendingJobs(ctx)
	})
	return s.startErr
}

func (s *TranscriptionService) workerLoop(ctx context.Context, queueType string, workerID int) {
	var queue <-chan string
	switch strings.ToLower(queueType) {
	case "gpu":
		queue = s.gpuQueue
	default:
		queueType = "cpu"
		queue = s.cpuQueue
	}

	for {
		select {
		case <-ctx.Done():
			return
		case jobID := <-queue:
			if jobID == "" {
				continue
			}
			s.recordQueueDepths()
			metrics.AddGauge("workers.active", 1)
			metrics.AddGauge("workers."+queueType+".active", 1)
			if err := s.RunJob(ctx, jobID); err != nil {
				if delay, shouldRetry := s.retryDelay(ctx, jobID); shouldRetry {
					s.scheduleRetry(ctx, jobID, delay)
				}
				slog.Warn("worker run failed",
					slog.Int("worker_id", workerID),
					slog.String("queue", queueType),
					slog.String("job_id", jobID),
					slog.String("error", err.Error()),
				)
			}
			metrics.AddGauge("workers.active", -1)
			metrics.AddGauge("workers."+queueType+".active", -1)
		}
	}
}

func (s *TranscriptionService) resumePendingJobs(ctx context.Context) error {
	for _, state := range []domain.JobState{domain.JobStateQueued, domain.JobStateRetrying} {
		jobs, err := s.jobs.ListByState(ctx, state)
		if err != nil {
			return fmt.Errorf("service: list %s jobs: %w", state, err)
		}
		for _, job := range jobs {
			if err := s.enqueueJob(ctx, job.ID); err != nil {
				return fmt.Errorf("service: enqueue resumed job %q: %w", job.ID, err)
			}
		}
	}
	return nil
}

func (s *TranscriptionService) resolveDevice(requested string) string {
	if requested != "" && requested != "auto" {
		return requested
	}
	return s.cfg.ResolvedDevice
}

func (s *TranscriptionService) workerCounts() (cpuWorkers int, gpuWorkers int) {
	cpuWorkers = s.cfg.Engine.MaxConcurrentCPU
	gpuWorkers = s.cfg.Engine.MaxConcurrentGPU

	if cpuWorkers < 0 {
		cpuWorkers = 0
	}
	if gpuWorkers < 0 {
		gpuWorkers = 0
	}
	if cpuWorkers == 0 && gpuWorkers == 0 {
		cpuWorkers = s.cfg.Engine.MaxConcurrentJobs
		if cpuWorkers < 1 {
			cpuWorkers = 1
		}
	}
	return cpuWorkers, gpuWorkers
}

func (s *TranscriptionService) queueNameForDevice(device string) string {
	if strings.EqualFold(device, "cuda") {
		_, gpuWorkers := s.workerCounts()
		if gpuWorkers > 0 {
			return "gpu"
		}
	}
	return "cpu"
}

func (s *TranscriptionService) recordQueueDepths() {
	metrics.SetGauge("queue.depth.cpu", float64(len(s.cpuQueue)))
	metrics.SetGauge("queue.depth.gpu", float64(len(s.gpuQueue)))
}

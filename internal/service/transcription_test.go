package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"you2midi/internal/config"
	"you2midi/internal/domain"
	"you2midi/internal/runner"
	"you2midi/internal/service"
)

// ---- in-memory test doubles -------------------------------------------------

type fakeJobRepo struct {
	mu   sync.Mutex
	jobs map[string]*domain.Job
	seq  int
}

func newFakeJobRepo() *fakeJobRepo { return &fakeJobRepo{jobs: map[string]*domain.Job{}} }

func (f *fakeJobRepo) Create(_ context.Context, j *domain.Job) (*domain.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j.ID == "" {
		f.seq++
		j.ID = fmt.Sprintf("test-job-%d", f.seq)
	}
	now := time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	clone := *j
	f.jobs[j.ID] = &clone
	return &clone, nil
}
func (f *fakeJobRepo) Get(_ context.Context, id string) (*domain.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, errors.New("job not found")
	}
	clone := *j
	return &clone, nil
}
func (f *fakeJobRepo) Update(_ context.Context, j *domain.Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j.UpdatedAt = time.Now().UTC()
	clone := *j
	f.jobs[j.ID] = &clone
	return nil
}
func (f *fakeJobRepo) List(_ context.Context) ([]*domain.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.Job, 0, len(f.jobs))
	for _, j := range f.jobs {
		clone := *j
		out = append(out, &clone)
	}
	return out, nil
}
func (f *fakeJobRepo) ListByState(_ context.Context, state domain.JobState) ([]*domain.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Job
	for _, j := range f.jobs {
		if j.State == state {
			clone := *j
			out = append(out, &clone)
		}
	}
	return out, nil
}
func (f *fakeJobRepo) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.jobs, id)
	return nil
}

type fakeCacheRepo struct{}

func (c *fakeCacheRepo) Lookup(_ context.Context, _ string) (string, bool, error) {
	return "", false, nil
}
func (c *fakeCacheRepo) Store(_ context.Context, _, _ string) error   { return nil }
func (c *fakeCacheRepo) Invalidate(_ context.Context, _ string) error { return nil }

type staleCacheRepo struct {
	invalidateCalls int
	lookupCalls     int
}

func (c *staleCacheRepo) Lookup(_ context.Context, _ string) (string, bool, error) {
	c.lookupCalls++
	if c.lookupCalls == 1 {
		return "/non/existent/path.mid", true, nil
	}
	return "", false, nil
}
func (c *staleCacheRepo) Store(_ context.Context, _, _ string) error { return nil }
func (c *staleCacheRepo) Invalidate(_ context.Context, _ string) error {
	c.invalidateCalls++
	return nil
}

type fakeEngine struct {
	name    string
	version string
	payload []byte
	err     *domain.EngineError
}

func (e *fakeEngine) Name() string { return e.name }
func (e *fakeEngine) Version(_ context.Context) (string, error) {
	return e.version, nil
}
func (e *fakeEngine) HealthCheck(_ context.Context) error { return nil }
func (e *fakeEngine) Transcribe(_ context.Context, _ io.Reader, _ domain.TranscribeOptions) (io.ReadCloser, error) {
	if e.err != nil {
		return nil, e.err
	}
	return io.NopCloser(bytes.NewReader(e.payload)), nil
}

type deviceAwareEngine struct {
	name    string
	version string
	payload []byte
}

func (e *deviceAwareEngine) Name() string { return e.name }
func (e *deviceAwareEngine) Version(_ context.Context) (string, error) {
	return e.version, nil
}
func (e *deviceAwareEngine) HealthCheck(_ context.Context) error { return nil }
func (e *deviceAwareEngine) Transcribe(_ context.Context, _ io.Reader, opts domain.TranscribeOptions) (io.ReadCloser, error) {
	if opts.Device == "cuda" {
		return nil, domain.NewEngineError(domain.ErrOOM, errors.New("cuda oom"))
	}
	return io.NopCloser(bytes.NewReader(e.payload)), nil
}

type timeoutEngine struct{}

func (e *timeoutEngine) Name() string                              { return "timeout-engine" }
func (e *timeoutEngine) Version(_ context.Context) (string, error) { return "1.0", nil }
func (e *timeoutEngine) HealthCheck(_ context.Context) error       { return nil }
func (e *timeoutEngine) Transcribe(_ context.Context, _ io.Reader, _ domain.TranscribeOptions) (io.ReadCloser, error) {
	return nil, domain.NewEngineError(domain.ErrTimeout, errors.New("timeout"))
}

type fakeDownloader struct{}

func (d *fakeDownloader) Download(_ context.Context, _ string, destDir string) (string, error) {
	p := destDir + "/audio.wav"
	if err := os.WriteFile(p, []byte("RIFF....WAVEfmt "), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

type countingDownloader struct {
	calls int32
}

func (d *countingDownloader) Download(_ context.Context, _ string, destDir string) (string, error) {
	atomic.AddInt32(&d.calls, 1)
	p := destDir + "/audio.wav"
	if err := os.WriteFile(p, []byte("RIFF....WAVEfmt "), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func (d *countingDownloader) Calls() int32 {
	return atomic.LoadInt32(&d.calls)
}

type hashAwareDownloader struct {
	hash              string
	downloadCalls     int32
	downloadHashCalls int32
}

func (d *hashAwareDownloader) Download(_ context.Context, _ string, _ string) (string, error) {
	atomic.AddInt32(&d.downloadCalls, 1)
	return "", errors.New("unexpected Download call")
}

func (d *hashAwareDownloader) DownloadAndHash(_ context.Context, _ string, destDir string) (string, string, error) {
	atomic.AddInt32(&d.downloadHashCalls, 1)
	p := destDir + "/audio.wav"
	if err := os.WriteFile(p, []byte("RIFF....WAVEfmt "), 0o644); err != nil {
		return "", "", err
	}
	return p, d.hash, nil
}

func (d *hashAwareDownloader) Calls() (downloadCalls int32, downloadHashCalls int32) {
	return atomic.LoadInt32(&d.downloadCalls), atomic.LoadInt32(&d.downloadHashCalls)
}

type trimRunner struct {
	mu         sync.Mutex
	ffmpegSeen bool
}

func (r *trimRunner) Run(_ context.Context, name string, args []string, _ runner.RunOptions) (*runner.RunResult, error) {
	if name == "ffmpeg" {
		r.mu.Lock()
		r.ffmpegSeen = true
		r.mu.Unlock()
		if len(args) == 0 {
			return nil, errors.New("missing ffmpeg args")
		}
		out := args[len(args)-1]
		if err := os.WriteFile(out, []byte("RIFF....WAVEfmt "), 0o644); err != nil {
			return nil, err
		}
		return &runner.RunResult{ExitCode: 0}, nil
	}
	return &runner.RunResult{ExitCode: 0}, nil
}

func (r *trimRunner) SawFFmpeg() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ffmpegSeen
}

type flakyEngine struct {
	name    string
	version string
	payload []byte
	calls   int32
}

func (e *flakyEngine) Name() string { return e.name }
func (e *flakyEngine) Version(_ context.Context) (string, error) {
	return e.version, nil
}
func (e *flakyEngine) HealthCheck(_ context.Context) error { return nil }
func (e *flakyEngine) Transcribe(_ context.Context, _ io.Reader, _ domain.TranscribeOptions) (io.ReadCloser, error) {
	call := atomic.AddInt32(&e.calls, 1)
	if call == 1 {
		return nil, domain.NewEngineError(domain.ErrTimeout, errors.New("transient timeout"))
	}
	return io.NopCloser(bytes.NewReader(e.payload)), nil
}

// blockingEngine blocks until context is cancelled.
type blockingEngine struct {
	startedOnce sync.Once
	started     chan struct{}
}

func newBlockingEngine() *blockingEngine {
	return &blockingEngine{started: make(chan struct{})}
}

func (e *blockingEngine) Name() string                              { return "blocking" }
func (e *blockingEngine) Version(_ context.Context) (string, error) { return "0", nil }
func (e *blockingEngine) HealthCheck(_ context.Context) error       { return nil }
func (e *blockingEngine) Transcribe(ctx context.Context, _ io.Reader, _ domain.TranscribeOptions) (io.ReadCloser, error) {
	e.startedOnce.Do(func() { close(e.started) })
	<-ctx.Done()
	return nil, domain.NewEngineError(domain.ErrCancelled, ctx.Err())
}
func (e *blockingEngine) waitUntilStarted() { <-e.started }

// ---- helpers ----------------------------------------------------------------

func newTestService(
	t *testing.T,
	jobs domain.JobRepository,
	cache domain.CacheRepository,
	primary domain.TranscriptionEngine,
	fallback domain.TranscriptionEngine,
) *service.TranscriptionService {
	t.Helper()
	if cache == nil {
		cache = &fakeCacheRepo{}
	}
	cfg := &config.Config{}
	cfg.Engine.MaxAttempts = 3
	cfg.Engine.MaxConcurrentJobs = 2
	cfg.Engine.QueueSize = 8
	cfg.ResolvedDevice = "cpu"
	cfg.Workspace.Root = t.TempDir()

	dl := &fakeDownloader{}
	r := runner.NewFakeRunner()
	return service.New(cfg, jobs, cache, primary, fallback, dl, r)
}

func newTestServiceWithDownloader(
	t *testing.T,
	jobs domain.JobRepository,
	cache domain.CacheRepository,
	primary domain.TranscriptionEngine,
	fallback domain.TranscriptionEngine,
	dl service.Downloader,
) *service.TranscriptionService {
	t.Helper()
	if cache == nil {
		cache = &fakeCacheRepo{}
	}
	cfg := &config.Config{}
	cfg.Engine.MaxAttempts = 3
	cfg.Engine.MaxConcurrentJobs = 2
	cfg.Engine.QueueSize = 8
	cfg.ResolvedDevice = "cpu"
	cfg.Workspace.Root = t.TempDir()

	r := runner.NewFakeRunner()
	return service.New(cfg, jobs, cache, primary, fallback, dl, r)
}

func newTestServiceWithRunner(
	t *testing.T,
	jobs domain.JobRepository,
	cache domain.CacheRepository,
	primary domain.TranscriptionEngine,
	fallback domain.TranscriptionEngine,
	dl service.Downloader,
	r runner.ProcessRunner,
) *service.TranscriptionService {
	t.Helper()
	if cache == nil {
		cache = &fakeCacheRepo{}
	}
	cfg := &config.Config{}
	cfg.Engine.MaxAttempts = 3
	cfg.Engine.MaxConcurrentJobs = 2
	cfg.Engine.QueueSize = 8
	cfg.ResolvedDevice = "cpu"
	cfg.Workspace.Root = t.TempDir()

	return service.New(cfg, jobs, cache, primary, fallback, dl, r)
}

// ---- tests ------------------------------------------------------------------

func TestSubmitYouTube_CreatesQueuedJob(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	svc := newTestService(t, newFakeJobRepo(), nil, engine, nil)

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{})
	if err != nil {
		t.Fatalf("SubmitYouTube: unexpected error: %v", err)
	}
	if job.State != domain.JobStateQueued {
		t.Errorf("expected state=queued, got %s", job.State)
	}
	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
}

func TestSubmitYouTube_PersistsStartSec(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	svc := newTestService(t, newFakeJobRepo(), nil, engine, nil)

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{StartSec: 20})
	if err != nil {
		t.Fatalf("SubmitYouTube: unexpected error: %v", err)
	}
	if job.StartSec != 20 {
		t.Fatalf("expected start_sec=20, got %d", job.StartSec)
	}
}

// TestRunJob_CancelMidFlight verifies deterministic cancel (Issue #10-A).
func TestRunJob_CancelMidFlight(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())

	blocking := newBlockingEngine()
	svc := newTestService(t, newFakeJobRepo(), nil, blocking, nil)

	job, _ := svc.SubmitYouTube(ctx, "https://youtube.com/watch?v=test", domain.TranscribeOptions{})

	done := make(chan error, 1)
	go func() { done <- svc.RunJob(ctx, job.ID) }()

	blocking.waitUntilStarted()
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error after cancel, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunJob did not return after cancel")
	}
}

func TestCancelJob_CancelsRunningExecution(t *testing.T) {
	t.Parallel()
	blocking := newBlockingEngine()
	repo := newFakeJobRepo()
	svc := newTestService(t, repo, nil, blocking, nil)

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{})
	if err != nil {
		t.Fatalf("SubmitYouTube: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- svc.RunJob(context.Background(), job.ID) }()

	blocking.waitUntilStarted()

	if err := svc.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("expected cancelled error from RunJob, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunJob did not stop after CancelJob")
	}

	updated, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.State != domain.JobStateCancelled {
		t.Fatalf("expected state=cancelled, got %s", updated.State)
	}
}

func TestStart_WorkersProcessQueuedJobs(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	repo := newFakeJobRepo()
	svc := newTestService(t, repo, nil, engine, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{})
	if err != nil {
		t.Fatalf("SubmitYouTube: %v", err)
	}

	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("job was not completed by background workers")
		case <-tick.C:
			updated, getErr := repo.Get(context.Background(), job.ID)
			if getErr != nil {
				t.Fatalf("Get: %v", getErr)
			}
			if updated.State == domain.JobStateCompleted {
				return
			}
		}
	}
}

func TestSubmitYouTube_QueueFullReturnsError(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	repo := newFakeJobRepo()
	cfg := &config.Config{}
	cfg.Engine.MaxAttempts = 3
	cfg.Engine.MaxConcurrentJobs = 1
	cfg.Engine.QueueSize = 1
	cfg.ResolvedDevice = "cpu"
	cfg.Workspace.Root = t.TempDir()

	svc := service.New(cfg, repo, &fakeCacheRepo{}, engine, nil, &fakeDownloader{}, runner.NewFakeRunner())

	if _, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=1", domain.TranscribeOptions{}); err != nil {
		t.Fatalf("first SubmitYouTube: %v", err)
	}
	if _, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=2", domain.TranscribeOptions{}); err == nil {
		t.Fatal("expected queue full error on second submit")
	}
}

func TestRunJob_OOMFallsBackToCPUAndCompletes(t *testing.T) {
	t.Parallel()
	engine := &deviceAwareEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	repo := newFakeJobRepo()
	cfg := &config.Config{}
	cfg.Engine.MaxAttempts = 3
	cfg.Engine.MaxConcurrentJobs = 0
	cfg.Engine.MaxConcurrentCPU = 1
	cfg.Engine.MaxConcurrentGPU = 1
	cfg.Engine.QueueSize = 8
	cfg.ResolvedDevice = "cpu"
	cfg.Workspace.Root = t.TempDir()
	svc := service.New(cfg, repo, &fakeCacheRepo{}, engine, nil, &fakeDownloader{}, runner.NewFakeRunner())

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{Device: "cuda"})
	if err != nil {
		t.Fatalf("SubmitYouTube: %v", err)
	}

	if err := svc.RunJob(context.Background(), job.ID); err == nil {
		t.Fatalf("expected retryable OOM error on first run")
	}
	mid, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get after first run: %v", err)
	}
	if mid.State != domain.JobStateRetrying {
		t.Fatalf("expected retrying after first run, got %s", mid.State)
	}
	if mid.Device != "cpu" {
		t.Fatalf("expected CPU fallback device after first run, got %s", mid.Device)
	}

	if err := svc.RunJob(context.Background(), job.ID); err != nil {
		t.Fatalf("RunJob second attempt: %v", err)
	}

	updated, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.State != domain.JobStateCompleted {
		t.Fatalf("expected completed after fallback, got %s", updated.State)
	}
	if updated.Device != "cpu" {
		t.Fatalf("expected device cpu after fallback, got %s", updated.Device)
	}
	if updated.Attempt != 2 {
		t.Fatalf("expected attempt=2 after fallback retry, got %d", updated.Attempt)
	}
}

func TestRunJob_RetryReusesDownloadedAudio(t *testing.T) {
	t.Parallel()
	repo := newFakeJobRepo()
	dl := &countingDownloader{}
	engine := &flakyEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	svc := newTestServiceWithDownloader(t, repo, nil, engine, nil, dl)

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{})
	if err != nil {
		t.Fatalf("SubmitYouTube: %v", err)
	}

	if err := svc.RunJob(context.Background(), job.ID); err == nil {
		t.Fatal("expected first run error to trigger retry")
	}
	if err := svc.RunJob(context.Background(), job.ID); err != nil {
		t.Fatalf("second RunJob: %v", err)
	}

	if got := dl.Calls(); got != 1 {
		t.Fatalf("expected downloader to run once across retry, got %d", got)
	}
	updated, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.AudioPath == "" {
		t.Fatal("expected audio_path to be persisted for retries")
	}
	if updated.AudioHash == "" {
		t.Fatal("expected audio_hash to be persisted for retries")
	}
}

func TestRunJob_UsesDownloaderProvidedHash(t *testing.T) {
	t.Parallel()
	repo := newFakeJobRepo()
	const expectedHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dl := &hashAwareDownloader{hash: expectedHash}
	engine := &fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	svc := newTestServiceWithDownloader(t, repo, nil, engine, nil, dl)

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{})
	if err != nil {
		t.Fatalf("SubmitYouTube: %v", err)
	}
	if err := svc.RunJob(context.Background(), job.ID); err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	updated, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.AudioHash != expectedHash {
		t.Fatalf("expected downloader-provided hash %s, got %s", expectedHash, updated.AudioHash)
	}
	downloadCalls, hashCalls := dl.Calls()
	if downloadCalls != 0 {
		t.Fatalf("expected Download() not to be called, got %d", downloadCalls)
	}
	if hashCalls != 1 {
		t.Fatalf("expected DownloadAndHash() once, got %d", hashCalls)
	}
}

func TestRunJob_StartSecAppliesFFmpegTrim(t *testing.T) {
	t.Parallel()
	repo := newFakeJobRepo()
	engine := &fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	dl := &fakeDownloader{}
	r := &trimRunner{}
	svc := newTestServiceWithRunner(t, repo, nil, engine, nil, dl, r)

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{StartSec: 5})
	if err != nil {
		t.Fatalf("SubmitYouTube: %v", err)
	}
	if err := svc.RunJob(context.Background(), job.ID); err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	updated, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !r.SawFFmpeg() {
		t.Fatal("expected ffmpeg trim invocation")
	}
	if !strings.HasSuffix(updated.AudioPath, "audio.trimmed.wav") {
		t.Fatalf("expected trimmed audio path, got %s", updated.AudioPath)
	}
}

func TestRunJob_RetryExhaustedAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	repo := newFakeJobRepo()
	cfg := &config.Config{}
	cfg.Engine.MaxAttempts = 2
	cfg.Engine.MaxConcurrentJobs = 1
	cfg.Engine.QueueSize = 8
	cfg.ResolvedDevice = "cpu"
	cfg.Workspace.Root = t.TempDir()

	svc := service.New(cfg, repo, &fakeCacheRepo{}, &timeoutEngine{}, nil, &fakeDownloader{}, runner.NewFakeRunner())
	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{})
	if err != nil {
		t.Fatalf("SubmitYouTube: %v", err)
	}

	if err := svc.RunJob(context.Background(), job.ID); err == nil {
		t.Fatal("expected first attempt error")
	}
	if err := svc.RunJob(context.Background(), job.ID); err == nil {
		t.Fatal("expected second attempt error")
	}

	updated, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.State != domain.JobStateFailed {
		t.Fatalf("expected failed state, got %s", updated.State)
	}
	if updated.ErrorCode != string(domain.ErrRetryExhausted) {
		t.Fatalf("expected ErrRetryExhausted, got %s", updated.ErrorCode)
	}
}

func TestRunJob_InvalidatesStaleCacheEntry(t *testing.T) {
	t.Parallel()
	engine := &fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}
	repo := newFakeJobRepo()
	cache := &staleCacheRepo{}
	svc := newTestService(t, repo, cache, engine, nil)

	job, err := svc.SubmitYouTube(context.Background(), "https://youtube.com/watch?v=test", domain.TranscribeOptions{})
	if err != nil {
		t.Fatalf("SubmitYouTube: %v", err)
	}
	if err := svc.RunJob(context.Background(), job.ID); err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if cache.invalidateCalls != 1 {
		t.Fatalf("expected stale cache invalidation once, got %d", cache.invalidateCalls)
	}
}

func TestStartupOrphanSweep_MarksJobsFailedAndCleansWorkspace(t *testing.T) {
	t.Parallel()
	repo := newFakeJobRepo()
	svc := newTestService(t, repo, nil, &fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")}, nil)

	ws1 := filepath.Join(t.TempDir(), "orphan1")
	ws2 := filepath.Join(t.TempDir(), "orphan2")
	if err := os.MkdirAll(ws1, 0o755); err != nil {
		t.Fatalf("mkdir ws1: %v", err)
	}
	if err := os.MkdirAll(ws2, 0o755); err != nil {
		t.Fatalf("mkdir ws2: %v", err)
	}

	j1, _ := repo.Create(context.Background(), &domain.Job{ID: "j1", State: domain.JobStateRunning, WorkspacePath: ws1})
	j2, _ := repo.Create(context.Background(), &domain.Job{ID: "j2", State: domain.JobStateRetrying, WorkspacePath: ws2})
	if j1 == nil || j2 == nil {
		t.Fatal("failed to seed jobs")
	}

	svc.StartupOrphanSweep(context.Background())

	for _, id := range []string{"j1", "j2"} {
		job, err := repo.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if job.State != domain.JobStateFailed {
			t.Fatalf("job %s expected failed, got %s", id, job.State)
		}
		if job.ErrorCode != string(domain.ErrTransientCrash) {
			t.Fatalf("job %s expected transient crash code, got %s", id, job.ErrorCode)
		}
	}
	if _, err := os.Stat(ws1); !os.IsNotExist(err) {
		t.Fatalf("expected ws1 removed, stat err=%v", err)
	}
	if _, err := os.Stat(ws2); !os.IsNotExist(err) {
		t.Fatalf("expected ws2 removed, stat err=%v", err)
	}
}

// TestErrorRetryPolicy verifies retryable and non-retryable error codes.
func TestErrorRetryPolicy(t *testing.T) {
	t.Parallel()
	retryable := []domain.ErrorCode{domain.ErrOOM, domain.ErrTimeout, domain.ErrTransientCrash}
	for _, code := range retryable {
		if !domain.NewEngineError(code, nil).Retryable {
			t.Errorf("expected %s to be retryable", code)
		}
	}

	nonRetryable := []domain.ErrorCode{
		domain.ErrEngineNotFound, domain.ErrModelMissing,
		domain.ErrCorruptFile, domain.ErrCopyrightBlocked,
	}
	for _, code := range nonRetryable {
		if domain.NewEngineError(code, nil).Retryable {
			t.Errorf("expected %s to be non-retryable", code)
		}
	}
}

// TestOOMFallbackStrategy_IsCPU verifies OOM error has GPU→CPU fallback.
func TestOOMFallbackStrategy_IsCPU(t *testing.T) {
	t.Parallel()
	err := domain.NewEngineError(domain.ErrOOM, nil)
	if err.FallbackStrategy != "cpu" {
		t.Errorf("OOM FallbackStrategy: want cpu, got %q", err.FallbackStrategy)
	}
}

// TestJobRoundTrip_JSONSerialization ensures domain types survive JSON round-trip (Issue #11-A).
func TestJobRoundTrip_JSONSerialization(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	original := &domain.Job{
		ID:          "abc",
		State:       domain.JobStateCompleted,
		YoutubeURL:  "https://youtube.com/watch?v=test",
		Engine:      "transkun",
		Device:      "cpu",
		OutputPath:  "/tmp/out.mid",
		Attempt:     1,
		MaxAttempts: 3,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded domain.Job
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.ID != original.ID {
		t.Errorf("ID mismatch: %q vs %q", decoded.ID, original.ID)
	}
	if decoded.State != original.State {
		t.Errorf("State mismatch: %q vs %q", decoded.State, original.State)
	}
	if decoded.Engine != original.Engine {
		t.Errorf("Engine mismatch: %q vs %q", decoded.Engine, original.Engine)
	}
}

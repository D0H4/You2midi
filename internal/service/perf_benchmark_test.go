package service_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"you2midi/internal/config"
	"you2midi/internal/domain"
	"you2midi/internal/runner"
	"you2midi/internal/service"
)

func BenchmarkRunJobColdPath(b *testing.B) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	repo := newFakeJobRepo()
	cfg := &config.Config{}
	cfg.Engine.MaxAttempts = 3
	cfg.Engine.MaxConcurrentJobs = 1
	cfg.Engine.QueueSize = 32
	cfg.ResolvedDevice = "cpu"
	cfg.Workspace.Root = b.TempDir()

	svc := service.New(
		cfg,
		repo,
		&fakeCacheRepo{},
		&fakeEngine{name: "transkun", version: "2.0", payload: []byte("MThd")},
		nil,
		&fakeDownloader{},
		runner.NewFakeRunner(),
	)

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job, err := repo.Create(ctx, &domain.Job{
			State:       domain.JobStateQueued,
			YoutubeURL:  fmt.Sprintf("https://youtube.com/watch?v=%d", i),
			Engine:      "transkun",
			Device:      "cpu",
			MaxAttempts: 3,
		})
		if err != nil {
			b.Fatalf("Create: %v", err)
		}
		if err := svc.RunJob(ctx, job.ID); err != nil {
			b.Fatalf("RunJob: %v", err)
		}
	}
}

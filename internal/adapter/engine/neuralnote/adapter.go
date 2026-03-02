// Package neuralnote implements the TranscriptionEngine interface using the
// NeuralNote standalone binary (https://github.com/DamRsn/NeuralNote).
package neuralnote

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"you2midi/internal/adapter/engine/common"
	"you2midi/internal/domain"
	"you2midi/internal/runner"
)

// Adapter implements domain.TranscriptionEngine for NeuralNote standalone.
type Adapter struct {
	bin string
	r   runner.ProcessRunner
}

func New(bin string, r runner.ProcessRunner) *Adapter {
	if bin == "" {
		bin = "neuralnote"
	}
	return &Adapter{bin: bin, r: r}
}

func (a *Adapter) Name() string { return "neuralnote" }

func (a *Adapter) Version(ctx context.Context) (string, error) {
	res, err := a.r.Run(ctx, a.bin, []string{"--version"}, runner.RunOptions{Timeout: 10 * time.Second})
	if err != nil {
		return "", common.WrapRunError(err)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (a *Adapter) HealthCheck(ctx context.Context) error {
	return runner.HealthCheck(ctx, a.r, a.bin)
}

func (a *Adapter) Transcribe(ctx context.Context, input io.Reader, _ domain.TranscribeOptions) (io.ReadCloser, error) {
	tmpDir, err := os.MkdirTemp("", "neuralnote-*")
	if err != nil {
		return nil, fmt.Errorf("neuralnote: create temp dir: %w", err)
	}

	inPath := filepath.Join(tmpDir, "input.audio")
	if err := common.WriteStream(inPath, input); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("neuralnote: write input: %w", err)
	}

	outPath := filepath.Join(tmpDir, "output.mid")
	args := []string{"--input", inPath, "--output", outPath}

	start := time.Now()
	res, err := a.r.Run(ctx, a.bin, args, runner.RunOptions{Dir: tmpDir})
	elapsed := time.Since(start)

	slog.Info("neuralnote inference complete",
		slog.Duration("duration", elapsed),
		slog.Int("exit_code", func() int {
			if res != nil {
				return res.ExitCode
			}
			return -1
		}()),
	)

	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, common.WrapRunError(err)
	}
	if res.ExitCode != 0 {
		_ = os.RemoveAll(tmpDir)
		return nil, common.ClassifyStderr(res.Stderr, false)
	}

	f, err := os.Open(outPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("neuralnote: open output: %w", err)
	}
	return &common.CleanupReader{ReadCloser: f, Dir: tmpDir}, nil
}

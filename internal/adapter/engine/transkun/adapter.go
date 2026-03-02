// Package transkun implements the TranscriptionEngine interface using the
// Transkun CLI (https://github.com/Yujia-Yan/Transkun).
package transkun

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"you2midi/internal/adapter/engine/common"
	"you2midi/internal/domain"
	"you2midi/internal/runner"
)

// Adapter implements domain.TranscriptionEngine for Transkun.
type Adapter struct {
	bin    string
	device string
	r      runner.ProcessRunner
}

var progressPattern = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)\s*%`)

// New creates a new Transkun adapter.
func New(bin, device string, r runner.ProcessRunner) *Adapter {
	if bin == "" {
		bin = "transkun"
	}
	return &Adapter{bin: bin, device: device, r: r}
}

func (a *Adapter) Name() string { return "transkun" }

func (a *Adapter) Version(ctx context.Context) (string, error) {
	res, err := a.r.Run(ctx, a.bin, []string{"--version"}, runner.RunOptions{Timeout: 10 * time.Second})
	if err != nil {
		return "", common.WrapRunError(err)
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		out = strings.TrimSpace(string(res.Stderr))
	}
	return out, nil
}

func (a *Adapter) HealthCheck(ctx context.Context) error {
	return runner.HealthCheck(ctx, a.r, a.bin)
}

func (a *Adapter) Transcribe(ctx context.Context, input io.Reader, opts domain.TranscribeOptions) (io.ReadCloser, error) {
	tmpDir, err := os.MkdirTemp("", "transkun-*")
	if err != nil {
		return nil, fmt.Errorf("transkun: create temp dir: %w", err)
	}

	inPath := filepath.Join(tmpDir, "input.audio")
	if err := common.WriteStream(inPath, input); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("transkun: write input: %w", err)
	}

	outPath := filepath.Join(tmpDir, "output.mid")
	args := []string{inPath, outPath, "--device", resolveDevice(a.device, opts.Device)}

	start := time.Now()
	res, err := a.r.Run(ctx, a.bin, args, runner.RunOptions{Dir: tmpDir})
	elapsed := time.Since(start)

	slog.Info("transkun inference complete",
		slog.String("device", resolveDevice(a.device, opts.Device)),
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

	progress := extractProgressPercents(res.Stderr)
	if len(progress) > 0 {
		slog.Debug("transkun progress summary",
			slog.Float64("progress_first_pct", progress[0]),
			slog.Float64("progress_last_pct", progress[len(progress)-1]),
			slog.Int("progress_points", len(progress)),
		)
	}
	if res.ExitCode != 0 {
		_ = os.RemoveAll(tmpDir)
		return nil, common.ClassifyStderr(res.Stderr, true)
	}

	f, err := os.Open(outPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("transkun: open output: %w", err)
	}
	return &common.CleanupReader{ReadCloser: f, Dir: tmpDir}, nil
}

func resolveDevice(adapterDevice, optsDevice string) string {
	if optsDevice != "" {
		return optsDevice
	}
	if adapterDevice != "" && adapterDevice != "auto" {
		return adapterDevice
	}
	return "cpu"
}

func extractProgressPercents(stderr []byte) []float64 {
	scanner := bufio.NewScanner(strings.NewReader(string(stderr)))
	out := make([]float64, 0, 16)
	last := -1.0
	for scanner.Scan() {
		line := scanner.Text()
		matches := progressPattern.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			pct, err := strconv.ParseFloat(m[1], 64)
			if err != nil || pct < 0 || pct > 100 {
				continue
			}
			// Keep monotonically increasing points to avoid noisy repeats.
			if pct > last {
				out = append(out, pct)
				last = pct
			}
		}
	}
	return out
}

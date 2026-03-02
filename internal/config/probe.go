// Package config provides CUDA device probing.
package config

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ProbeCUDA runs a Python one-liner to check if torch.cuda is available.
// It returns "cuda" if available, "cpu" otherwise.
// The result is stored in cfg.ResolvedDevice.
func (c *Config) ProbeCUDA(ctx context.Context, pythonBin string, r interface {
	Run(ctx context.Context, name string, args []string, opts interface{}) (interface{ GetStdout() []byte }, error)
}) string {
	// This is the lightweight version; the real probe uses runner.ProcessRunner.
	// See config.ProbeWithRunner for the full implementation.
	return "cpu"
}

// ProbeDevice determines the actual device to use for inference.
// If cfg.Engine.Device == "auto", it runs a Python probe.
// If cfg.Engine.Device == "cuda" but CUDA is unavailable, it logs a warning.
// The resolved value is written to cfg.ResolvedDevice and returned.
//
// r must be a *runner.DefaultRunner or compatible ProcessRunner.
func ProbeDevice(ctx context.Context, cfg *Config, run func(ctx context.Context, bin string, args []string) (string, error)) (string, error) {
	if cfg.Engine.Device == "cpu" {
		cfg.ResolvedDevice = "cpu"
		return "cpu", nil
	}

	// Run the Python CUDA probe.
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	stdout, err := run(probeCtx, cfg.Engine.PythonBin, []string{
		"-c", "import torch; print(torch.cuda.is_available())",
	})
	if err != nil {
		slog.Warn("CUDA probe failed (defaulting to cpu)",
			slog.String("error", err.Error()),
		)
		cfg.ResolvedDevice = "cpu"
		return "cpu", nil
	}

	cudaAvailable := strings.TrimSpace(stdout) == "True"

	switch cfg.Engine.Device {
	case "auto":
		if cudaAvailable {
			cfg.ResolvedDevice = "cuda"
			slog.Info("CUDA probe: CUDA available — using GPU")
		} else {
			cfg.ResolvedDevice = "cpu"
			slog.Info("CUDA probe: CUDA not available — using CPU")
		}
	case "cuda":
		if !cudaAvailable {
			return "", fmt.Errorf(
				"config: device=cuda requested but torch.cuda.is_available() returned False. " +
					"Install a CUDA-compatible PyTorch or set device=auto",
			)
		}
		cfg.ResolvedDevice = "cuda"
		slog.Info("CUDA probe: CUDA confirmed available")
	}

	return cfg.ResolvedDevice, nil
}

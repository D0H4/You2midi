// Package runner provides a subprocess execution layer with OS-agnostic kill-tree support.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// ProcessRunner executes external commands and guarantees cleanup on context cancellation.
type ProcessRunner interface {
	Run(ctx context.Context, name string, args []string, opts RunOptions) (*RunResult, error)
}

// RunOptions configures a single subprocess invocation.
type RunOptions struct {
	Stdin   io.Reader
	Env     []string      // additional env vars appended to os.Environ()
	Timeout time.Duration // 0 = no timeout (context deadline used)
	Dir     string        // working directory; "" = current dir
}

// RunResult captures the outcome of a completed subprocess.
type RunResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Duration time.Duration
}

// HealthCheck verifies that the listed binaries are accessible.
// For binaries whose --version exits non-zero (e.g. transkun), it falls back
// to checking that the binary is reachable at all (exec.LookPath equivalent).
func HealthCheck(ctx context.Context, r ProcessRunner, binaries ...string) error {
	for _, bin := range binaries {
		if bin == "" {
			continue
		}
		res, err := r.Run(ctx, bin, []string{"--version"}, RunOptions{
			Timeout: 10 * time.Second,
		})
		if err != nil {
			return fmt.Errorf("health check failed for %q: %w", bin, err)
		}
		// Accept exit 0 OR exit 2 (argparse "required args missing") as "healthy".
		// transkun exits 2 when called without audio/output path arguments.
		if res.ExitCode != 0 && res.ExitCode != 2 {
			return fmt.Errorf(
				"health check failed for %q (exit %d): %s",
				bin, res.ExitCode, bytes.TrimSpace(res.Stderr),
			)
		}
	}
	return nil
}

// ResolveBinary returns the first candidate that passes HealthCheck.
// The first candidate should be the configured path/name; remaining candidates
// are fallback names (typically PATH-resolved binaries).
func ResolveBinary(ctx context.Context, r ProcessRunner, configured string, fallbacks ...string) (resolved string, usedFallback bool, err error) {
	candidates := make([]string, 0, 1+len(fallbacks))
	seen := map[string]struct{}{}
	addCandidate := func(bin string) {
		bin = strings.TrimSpace(bin)
		if bin == "" {
			return
		}
		if _, ok := seen[bin]; ok {
			return
		}
		seen[bin] = struct{}{}
		candidates = append(candidates, bin)
	}

	addCandidate(configured)
	for _, fb := range fallbacks {
		addCandidate(fb)
	}

	if len(candidates) == 0 {
		return "", false, fmt.Errorf("no binary candidates provided")
	}

	errs := make([]string, 0, len(candidates))
	for i, candidate := range candidates {
		if err := HealthCheck(ctx, r, candidate); err == nil {
			return candidate, i > 0, nil
		} else {
			errs = append(errs, fmt.Sprintf("%s: %v", candidate, err))
		}
	}

	return candidates[0], false, fmt.Errorf("all runtime binary candidates failed health check: %s", strings.Join(errs, "; "))
}

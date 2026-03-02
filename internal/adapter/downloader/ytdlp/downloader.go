// Package ytdlp provides a yt-dlp based Downloader for the transcription service.
package ytdlp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"you2midi/internal/runner"
)

// Downloader downloads audio from YouTube URLs using yt-dlp.
type Downloader struct {
	bin     string
	nodeBin string // path to node.js executable for JS challenge solving
	runner  runner.ProcessRunner
}

// New creates a Downloader. bin defaults to "yt-dlp" if empty.
// nodeBin is auto-detected if empty.
func New(bin string, r runner.ProcessRunner) *Downloader {
	if bin == "" {
		bin = "yt-dlp"
	}
	bin = normalizeBinPath(bin)
	return &Downloader{
		bin:     bin,
		nodeBin: detectNode(),
		runner:  r,
	}
}

// normalizeBinPath converts relative path-like binary values to absolute paths.
// This prevents breakage when command working directory changes per job.
func normalizeBinPath(bin string) string {
	trimmed := strings.TrimSpace(bin)
	if trimmed == "" {
		return trimmed
	}
	if !looksLikePath(trimmed) || filepath.IsAbs(trimmed) {
		return trimmed
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return abs
	}
	return trimmed
}

func looksLikePath(bin string) bool {
	return strings.ContainsAny(bin, `/\`) || strings.HasPrefix(bin, ".")
}

// detectNode returns the absolute path to node.exe/node if available on PATH.
func detectNode() string {
	if configured := strings.TrimSpace(os.Getenv("YOU2MIDI_NODE_BIN")); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
	}

	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return ""
}

// Download fetches audio from url into destDir and returns the path to the audio file.
// It uses yt-dlp's best audio format and retries with Node.js JS runtime if needed.
func (d *Downloader) Download(ctx context.Context, url string, destDir string) (string, error) {
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return "", fmt.Errorf("ytdlp: mkdir %q: %w", destDir, err)
	}

	outTemplate := filepath.Join(destDir, "audio.%(ext)s")
	primaryArgs := d.buildArgs(outTemplate, url)
	fallbackArgs := d.buildNoJSFallbackArgs(outTemplate, url)
	var (
		lastErr error
		bins    = downloadCandidates(d.bin)
	)
	for i, bin := range bins {
		start := time.Now()
		res, err := d.runner.Run(ctx, bin, primaryArgs, runner.RunOptions{Dir: destDir})
		elapsed := time.Since(start)
		if err != nil {
			lastErr = fmt.Errorf("ytdlp: run %q for %q: %w", bin, url, err)
			if i+1 < len(bins) {
				slog.Warn("yt-dlp launch failed; trying fallback binary",
					slog.String("configured_bin", d.bin),
					slog.String("failed_bin", bin),
					slog.String("fallback_bin", bins[i+1]),
					slog.String("error", err.Error()),
				)
				continue
			}
			break
		}

		stderr := strings.TrimSpace(string(res.Stderr))
		stdout := strings.TrimSpace(string(res.Stdout))

		slog.Info("yt-dlp download finished",
			slog.String("url", url),
			slog.String("bin", bin),
			slog.Duration("duration", elapsed),
			slog.Int("exit_code", res.ExitCode),
		)

		// yt-dlp may exit non-zero while still downloading successfully.
		if res.ExitCode != 0 {
			if shouldRetryWithNoJSFallback(stderr, stdout) {
				slog.Warn("yt-dlp primary attempt failed; retrying with no-JS fallback strategy",
					slog.Int("exit_code", res.ExitCode),
					slog.String("stderr_snippet", truncate(stderr, 280)),
				)
				retryStart := time.Now()
				retryRes, retryErr := d.runner.Run(ctx, bin, fallbackArgs, runner.RunOptions{Dir: destDir})
				retryElapsed := time.Since(retryStart)
				if retryErr != nil {
					lastErr = fmt.Errorf("ytdlp: fallback run %q for %q: %w", bin, url, retryErr)
				} else {
					retryStderr := strings.TrimSpace(string(retryRes.Stderr))
					retryStdout := strings.TrimSpace(string(retryRes.Stdout))
					slog.Info("yt-dlp fallback download finished",
						slog.String("url", url),
						slog.String("bin", bin),
						slog.Duration("duration", retryElapsed),
						slog.Int("exit_code", retryRes.ExitCode),
					)
					if retryRes.ExitCode != 0 && isHardError(retryStderr, retryStdout) {
						return "", fmt.Errorf("ytdlp: yt-dlp fallback failed (exit %d): %s", retryRes.ExitCode, retryStderr)
					}
				}
			} else if isHardError(stderr, stdout) {
				return "", fmt.Errorf("ytdlp: yt-dlp failed (exit %d): %s", res.ExitCode, stderr)
			}
			slog.Warn("yt-dlp exited non-zero but may have succeeded; checking for output file",
				slog.Int("exit_code", res.ExitCode),
				slog.String("stderr_snippet", truncate(stderr, 200)),
			)
		}

		audioPath, findErr := findAudioFile(destDir)
		if findErr == nil {
			d.bin = bin // stick to the last known-good binary for next runs
			return audioPath, nil
		}
		lastErr = fmt.Errorf("ytdlp: no audio file in output dir after download (exit %d): %s", res.ExitCode, truncate(stderr, 300))
		if i+1 < len(bins) {
			slog.Warn("yt-dlp output missing; trying fallback binary",
				slog.String("failed_bin", bin),
				slog.String("fallback_bin", bins[i+1]),
			)
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("ytdlp: no downloader candidates available")
}

// DownloadAndHash downloads audio and returns a content hash so callers can avoid a second hash pass.
func (d *Downloader) DownloadAndHash(ctx context.Context, url string, destDir string) (audioPath string, audioHash string, err error) {
	audioPath, err = d.Download(ctx, url, destDir)
	if err != nil {
		return "", "", err
	}
	audioHash, err = sha256File(audioPath)
	if err != nil {
		return "", "", fmt.Errorf("ytdlp: hash downloaded audio: %w", err)
	}
	return audioPath, audioHash, nil
}

// buildArgs constructs the yt-dlp argument list.
// Includes Node.js runtime if available, to resolve YouTube JS challenges.
func (d *Downloader) buildArgs(outTemplate, url string) []string {
	args := []string{
		"--no-playlist",
		"-f", "bestaudio", // avoid conversion that requires ffmpeg
		"-o", outTemplate,
		"--no-progress",
	}

	if d.nodeBin != "" {
		args = append(args,
			"--no-js-runtimes",
			"--js-runtimes", "node:"+d.nodeBin,
		)
		slog.Debug("yt-dlp: using node.js runtime", slog.String("node", d.nodeBin))
	}

	args = append(args, url)
	return args
}

// buildNoJSFallbackArgs constructs a fallback strategy for hosts without Node.js.
// It broadens format selection and uses non-web YouTube clients to reduce JS runtime reliance.
func (d *Downloader) buildNoJSFallbackArgs(outTemplate, url string) []string {
	args := []string{
		"--no-playlist",
		"-f", "bestaudio/best",
		"-o", outTemplate,
		"--no-progress",
		"--extractor-args", "youtube:player_client=android,ios,tv",
	}
	args = append(args, url)
	return args
}

// isHardError returns true if stderr indicates a genuine download failure.
func isHardError(stderr, stdout string) bool {
	lower := strings.ToLower(stderr + " " + stdout)
	hardErrors := []string{
		"video unavailable",
		"this video is private",
		"sign in to confirm your age",
		"has been removed",
		"account associated with this video has been terminated",
		"no video formats found",
		"requested format is not available",
	}
	for _, e := range hardErrors {
		if strings.Contains(lower, e) {
			return true
		}
	}
	return false
}

func shouldRetryWithNoJSFallback(stderr, stdout string) bool {
	lower := strings.ToLower(stderr + " " + stdout)
	signals := []string{
		"no supported javascript runtime could be found",
		"youtube extraction without a js runtime has been deprecated",
		"requested format is not available",
		"no video formats found",
	}
	for _, s := range signals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// findAudioFile returns the first audio file found in dir.
func findAudioFile(dir string) (string, error) {
	audioExts := map[string]bool{
		".wav": true, ".mp3": true, ".flac": true,
		".ogg": true, ".aiff": true, ".m4a": true,
		".opus": true, ".webm": true, ".aac": true,
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if audioExts[strings.ToLower(filepath.Ext(e.Name()))] {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no audio file found in %q", dir)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func downloadCandidates(configured string) []string {
	candidates := []string{}
	seen := map[string]struct{}{}
	add := func(bin string) {
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

	add(configured)
	add("yt-dlp")
	return candidates
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"you2midi/internal/updater"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "updater failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	patchRef := flag.String("patch", "", "patch zip path or HTTP(S) URL")
	appDir := flag.String("app-dir", "", "target app directory (default: updater exe directory)")
	parentPID := flag.Int("parent-pid", 0, "PID to wait for before patching")
	waitTimeout := flag.Duration("wait-timeout", 2*time.Minute, "timeout to wait for parent process exit")
	launch := flag.String("launch", "you2midi-desktop.exe", "executable to launch after patch")
	logFile := flag.String("log-file", "", "optional updater log file path")
	stateFile := flag.String("state-file", "", "optional launcher state file path to write after successful patch")
	stateReleaseTag := flag.String("state-release-tag", "", "release tag written to state-file after successful patch")
	flag.Parse()

	if strings.TrimSpace(*patchRef) == "" {
		return errors.New("missing required -patch")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve updater executable path: %w", err)
	}
	defaultAppDir := filepath.Dir(exePath)
	if strings.TrimSpace(*appDir) == "" {
		*appDir = defaultAppDir
	}
	absAppDir, err := filepath.Abs(*appDir)
	if err != nil {
		return fmt.Errorf("resolve app dir: %w", err)
	}

	closers := make([]io.Closer, 0, 1)
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	writers := []io.Writer{os.Stdout}
	if strings.TrimSpace(*logFile) != "" {
		if err := os.MkdirAll(filepath.Dir(*logFile), 0o755); err != nil {
			return fmt.Errorf("create log directory: %w", err)
		}
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		closers = append(closers, f)
		writers = append(writers, f)
	}
	logger := log.New(io.MultiWriter(writers...), "", log.LstdFlags|log.Lmicroseconds)

	logger.Printf("Updater started")
	logger.Printf("App directory: %s", absAppDir)

	patchPath, cleanupPatch, err := resolvePatch(*patchRef, logger)
	if cleanupPatch != nil {
		defer cleanupPatch()
	}
	if err != nil {
		return err
	}
	logger.Printf("Patch source ready: %s", patchPath)

	if *parentPID > 0 {
		logger.Printf("Waiting for parent PID %d", *parentPID)
		if err := updater.WaitForPIDExit(*parentPID, *waitTimeout); err != nil {
			return err
		}
		logger.Printf("Parent PID %d exited", *parentPID)
	}

	skipFiles := map[string]struct{}{}
	if rel, err := filepath.Rel(absAppDir, exePath); err == nil && rel != "" {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if !strings.HasPrefix(rel, "../") && rel != ".." {
			skipFiles[rel] = struct{}{}
		}
	}

	opts := updater.DefaultApplyOptions()
	opts.Logger = logger
	opts.SkipFiles = skipFiles

	manifest, err := updater.ApplyPatchZip(absAppDir, patchPath, opts)
	if err != nil {
		return err
	}
	logger.Printf("Patch applied (version=%q, files=%d)", manifest.Version, len(manifest.Files))
	if strings.TrimSpace(*stateFile) != "" && strings.TrimSpace(*stateReleaseTag) != "" {
		if err := writeLauncherState(*stateFile, strings.TrimSpace(*stateReleaseTag)); err != nil {
			logger.Printf("Warning: write state file failed: %v", err)
		}
	}

	launchTarget := strings.TrimSpace(*launch)
	if launchTarget == "" && manifest != nil {
		launchTarget = strings.TrimSpace(manifest.LaunchExecutable)
	}
	if launchTarget == "" {
		return nil
	}

	launchPath, err := updater.ResolvePathUnderRoot(absAppDir, launchTarget)
	if err != nil {
		return fmt.Errorf("resolve launch path: %w", err)
	}
	if err := launchExecutable(launchPath, absAppDir, logger); err != nil {
		return err
	}
	logger.Printf("Launched: %s", launchPath)
	return nil
}

func resolvePatch(patchRef string, logger *log.Logger) (path string, cleanup func(), err error) {
	if isHTTPURL(patchRef) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		return downloadPatch(ctx, patchRef, logger)
	}

	abs, err := filepath.Abs(patchRef)
	if err != nil {
		return "", nil, fmt.Errorf("resolve patch path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", nil, fmt.Errorf("patch file not found %q: %w", abs, err)
	}
	return abs, nil, nil
}

func downloadPatch(ctx context.Context, patchURL string, logger *log.Logger) (path string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "you2midi-updater-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir for patch download: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	dst := filepath.Join(tmpDir, "patch.zip")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, patchURL, nil)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("build patch download request: %w", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("download patch: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		cleanup()
		return "", nil, fmt.Errorf("download patch failed: HTTP %d", res.StatusCode)
	}

	out, err := os.Create(dst)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create patch file: %w", err)
	}
	if _, err := io.Copy(out, res.Body); err != nil {
		_ = out.Close()
		cleanup()
		return "", nil, fmt.Errorf("write patch file: %w", err)
	}
	if err := out.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close patch file: %w", err)
	}

	logger.Printf("Downloaded patch: %s", dst)
	return dst, cleanup, nil
}

func isHTTPURL(value string) bool {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func launchExecutable(executablePath string, workDir string, logger *log.Logger) error {
	cmd := exec.Command(executablePath)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	applyPlatformStartAttrs(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start launch target: %w", err)
	}
	logger.Printf("Launch command started with PID=%d", cmd.Process.Pid)
	return nil
}

type launcherState struct {
	LastReleaseTag string `json:"last_release_tag"`
	UpdatedAtUTC   string `json:"updated_at_utc"`
}

func writeLauncherState(path string, releaseTag string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	state := launcherState{
		LastReleaseTag: releaseTag,
		UpdatedAtUTC:   time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	return nil
}

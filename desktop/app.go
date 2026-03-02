package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx            context.Context
	backendCmd     *exec.Cmd
	backendExitCh  chan error
	backendBaseURL string
	statusMu       sync.RWMutex
	lastBackendErr string
	runtimeMu      sync.Mutex
}

type runtimeBootstrapEvent struct {
	Status       string `json:"status"`
	Stage        string `json:"stage"`
	Message      string `json:"message"`
	UpdatedAtUTC string `json:"updated_at_utc"`
}

const defaultDesktopConfigTOML = `# You2Midi desktop runtime configuration
schema_version = 1

[engine]
  device              = "auto"  # auto | cpu | cuda
  max_attempts        = 3
  max_concurrent_jobs = 2
  max_concurrent_cpu  = 1
  max_concurrent_gpu  = 1
  queue_size          = 128
`

func NewApp() *App {
	return &App{
		backendBaseURL: "http://127.0.0.1:8080",
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.startBackend(); err != nil {
		a.setLastBackendErr(err.Error())
		wruntime.LogError(ctx, fmt.Sprintf("backend startup failed: %v", err))
		return
	}
	wruntime.LogInfo(ctx, "backend startup complete")
}

func (a *App) beforeClose(_ context.Context) bool {
	a.stopBackend()
	return false
}

func (a *App) shutdown(_ context.Context) {
	a.stopBackend()
}

// Ping is a simple sanity check for desktop frontend integration.
func (a *App) Ping() string {
	return "pong"
}

// AppDataDir returns a writable app data directory path.
func (a *App) AppDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".you2midi"), nil
}

// OpenExternal delegates URL open to the system browser.
func (a *App) OpenExternal(url string) {
	if a.ctx == nil {
		return
	}
	wruntime.BrowserOpenURL(a.ctx, url)
}

// StartPatchUpdate launches the updater and then quits the desktop app.
func (a *App) StartPatchUpdate(patchRef string) error {
	patchRef = strings.TrimSpace(patchRef)
	if patchRef == "" {
		return errors.New("patch reference is required")
	}

	updaterPath, err := findUpdaterBinary()
	if err != nil {
		return err
	}
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve desktop executable path: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	launchExe := filepath.Base(exePath)

	logDir := filepath.Join(exeDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create updater log directory: %w", err)
	}
	logFile := filepath.Join(logDir, "updater.log")

	cmd := exec.Command(
		updaterPath,
		"-patch", patchRef,
		"-app-dir", exeDir,
		"-parent-pid", strconv.Itoa(os.Getpid()),
		"-launch", launchExe,
		"-log-file", logFile,
	)
	cmd.Dir = exeDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start updater process: %w", err)
	}

	if a.ctx != nil {
		go func() {
			time.Sleep(200 * time.Millisecond)
			wruntime.Quit(a.ctx)
		}()
	}
	return nil
}

// BackendOfflineReason returns a short reason string for UI tooltip usage.
func (a *App) BackendOfflineReason() string {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(a.backendBaseURL + "/health")
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return "Backend is healthy."
		}
		return fmt.Sprintf("Health check failed: HTTP %d", resp.StatusCode)
	}

	lastErr := strings.TrimSpace(a.getLastBackendErr())
	if lastErr != "" {
		return lastErr
	}
	return fmt.Sprintf("Health check failed: %v", err)
}

func (a *App) startBackend() error {
	a.publishRuntimeBootstrap("running", "startup", "앱 초기 설정을 준비하는 중입니다.")
	backendPath, err := findBackendBinary()
	if err != nil {
		a.setLastBackendErr(err.Error())
		a.publishRuntimeBootstrap("error", "startup", err.Error())
		return err
	}
	workspaceRoot, err := a.desktopWorkspaceRoot()
	if err != nil {
		a.setLastBackendErr(err.Error())
		a.publishRuntimeBootstrap("error", "startup", err.Error())
		return err
	}
	if err := os.MkdirAll(workspaceRoot, 0o750); err != nil {
		wrapped := fmt.Errorf("create desktop workspace: %w", err)
		a.setLastBackendErr(wrapped.Error())
		a.publishRuntimeBootstrap("error", "workspace", wrapped.Error())
		return wrapped
	}

	workDir := chooseBackendWorkingDir(filepath.Dir(backendPath))
	configPath, err := a.ensureDesktopConfig(workDir)
	if err != nil {
		wrapped := fmt.Errorf("ensure desktop config: %w", err)
		a.setLastBackendErr(wrapped.Error())
		a.publishRuntimeBootstrap("error", "config", wrapped.Error())
		return wrapped
	}

	cmd := exec.Command(backendPath, "-config", configPath)
	cmd.Dir = workDir

	pythonScripts, err := a.resolvePythonRuntimeScripts(filepath.Dir(backendPath))
	if err != nil {
		wrapped := fmt.Errorf("resolve python runtime: %w", err)
		a.setLastBackendErr(wrapped.Error())
		a.publishRuntimeBootstrap("error", "runtime", wrapped.Error())
		return wrapped
	}

	env := os.Environ()
	env = setEnvValue(env, "YOU2MIDI_HOST", "127.0.0.1")
	env = setEnvValue(env, "YOU2MIDI_ALLOWED_ORIGINS", "*")
	env = setEnvValue(env, "YOU2MIDI_WORKSPACE_ROOT", workspaceRoot)
	if pythonScripts != "" {
		env = prependEnvPath(env, pythonScripts)
		pythonBin := filepath.Join(pythonScripts, binaryWithExe("python"))
		if !fileExists(pythonBin) {
			runtimeRoot := runtimeRootFromScriptsDir(pythonScripts)
			alt := filepath.Join(runtimeRoot, binaryWithExe("python"))
			if fileExists(alt) {
				pythonBin = alt
			}
		}
		if fileExists(pythonBin) {
			env = setEnvValue(env, "YOU2MIDI_PYTHON_BIN", pythonBin)
		}
		if transkunBin := filepath.Join(pythonScripts, binaryWithExe("transkun")); fileExists(transkunBin) {
			env = setEnvValue(env, "YOU2MIDI_TRANSKUN_BIN", transkunBin)
		}
		if ytDlpBin := filepath.Join(pythonScripts, binaryWithExe("yt-dlp")); fileExists(ytDlpBin) {
			env = setEnvValue(env, "YOU2MIDI_YTDLP_BIN", ytDlpBin)
		}
	}
	if ffmpegBin := findBundledFFmpegBin(filepath.Dir(backendPath)); ffmpegBin != "" {
		env = prependEnvPath(env, ffmpegBin)
	}
	if cudaBin, cudaRoot := findBundledCUDARuntime(filepath.Dir(backendPath)); cudaBin != "" {
		env = prependEnvPath(env, cudaBin)
		env = setEnvValue(env, "CUDA_PATH", cudaRoot)
		env = setEnvValue(env, "CUDA_HOME", cudaRoot)
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}

	if err := cmd.Start(); err != nil {
		wrapped := fmt.Errorf("start backend process: %w", err)
		a.setLastBackendErr(wrapped.Error())
		a.publishRuntimeBootstrap("error", "backend", wrapped.Error())
		return wrapped
	}

	exitCh := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		if waitErr != nil {
			msg := fmt.Sprintf("backend process exited: %v", waitErr)
			a.setLastBackendErr(msg)
			if a.ctx != nil {
				wruntime.LogError(a.ctx, msg)
			}
		}
		exitCh <- waitErr
	}()

	if err := waitForHealth(a.backendBaseURL, 45*time.Second, exitCh); err != nil {
		_ = cmd.Process.Kill()
		<-exitCh
		wrapped := fmt.Errorf("backend health check failed: %w", err)
		a.setLastBackendErr(wrapped.Error())
		a.publishRuntimeBootstrap("error", "health", wrapped.Error())
		return wrapped
	}

	a.statusMu.Lock()
	a.backendCmd = cmd
	a.backendExitCh = exitCh
	a.statusMu.Unlock()
	a.setLastBackendErr("")
	a.publishRuntimeBootstrap("done", "ready", "초기 설정이 완료되었습니다. 백엔드가 준비되었습니다.")
	return nil
}

func (a *App) stopBackend() {
	a.statusMu.RLock()
	cmd := a.backendCmd
	exitCh := a.backendExitCh
	a.statusMu.RUnlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	_ = cmd.Process.Signal(os.Interrupt)

	if exitCh == nil {
		_ = cmd.Process.Kill()
		a.statusMu.Lock()
		a.backendCmd = nil
		a.backendExitCh = nil
		a.statusMu.Unlock()
		return
	}

	select {
	case <-exitCh:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-exitCh:
		case <-time.After(2 * time.Second):
		}
	}

	a.statusMu.Lock()
	a.backendCmd = nil
	a.backendExitCh = nil
	a.statusMu.Unlock()
}

func waitForHealth(baseURL string, timeout time.Duration, exitCh <-chan error) error {
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(timeout)
	lastErr := errors.New("health endpoint unavailable")

	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("unexpected status %d", resp.StatusCode)
		} else {
			lastErr = err
		}

		select {
		case err := <-exitCh:
			if err == nil {
				return errors.New("backend exited before health check without error")
			}
			return fmt.Errorf("backend exited during startup: %w", err)
		case <-time.After(250 * time.Millisecond):
		}
	}

	return fmt.Errorf("timeout after %s: %w", timeout, lastErr)
}

func findBackendBinary() (string, error) {
	if envPath := strings.TrimSpace(os.Getenv("YOU2MIDI_BACKEND_BIN")); envPath != "" {
		if fileExists(envPath) {
			return envPath, nil
		}
		return "", fmt.Errorf("YOU2MIDI_BACKEND_BIN not found: %s", envPath)
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	name := backendBinaryName()

	candidates := []string{
		filepath.Join(exeDir, name),
		filepath.Join(exeDir, "dist", "desktop", name),
		filepath.Join(exeDir, "..", "dist", "desktop", name),
		filepath.Join(exeDir, "..", "..", "dist", "desktop", name),
		filepath.Join(exeDir, "..", "..", "..", "dist", "desktop", name),
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("backend binary not found; looked for %q near %s", name, exeDir)
}

func findUpdaterBinary() (string, error) {
	if envPath := strings.TrimSpace(os.Getenv("YOU2MIDI_UPDATER_BIN")); envPath != "" {
		if fileExists(envPath) {
			return envPath, nil
		}
		return "", fmt.Errorf("YOU2MIDI_UPDATER_BIN not found: %s", envPath)
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	name := updaterBinaryName()

	candidates := []string{
		filepath.Join(exeDir, name),
		filepath.Join(exeDir, "dist", "desktop", name),
		filepath.Join(exeDir, "..", "dist", "desktop", name),
		filepath.Join(exeDir, "..", "..", "dist", "desktop", name),
		filepath.Join(exeDir, "..", "..", "..", "dist", "desktop", name),
	}

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("updater binary not found; looked for %q near %s", name, exeDir)
}

func chooseBackendWorkingDir(backendDir string) string {
	candidates := []string{
		backendDir,
		filepath.Clean(filepath.Join(backendDir, "..")),
		filepath.Clean(filepath.Join(backendDir, "..", "..")),
	}
	for _, dir := range candidates {
		if fileExists(filepath.Join(dir, "config.toml")) {
			return dir
		}
	}
	return backendDir
}

func (a *App) desktopWorkspaceRoot() (string, error) {
	appDataDir, err := a.AppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(appDataDir, "desktop-jobs"), nil
}

func backendBinaryName() string {
	return binaryWithExe("you2midi-backend")
}

func updaterBinaryName() string {
	return binaryWithExe("you2midi-updater")
}

func binaryWithExe(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func runtimeRootFromScriptsDir(scriptsDir string) string {
	base := strings.ToLower(filepath.Base(scriptsDir))
	if base == "scripts" || base == "bin" {
		return filepath.Dir(scriptsDir)
	}
	return scriptsDir
}

func (a *App) ensureDesktopConfig(workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return "", errors.New("work directory is empty")
	}

	primaryPath := filepath.Join(workDir, "config.toml")
	if fileExists(primaryPath) {
		return primaryPath, nil
	}

	raw := []byte(defaultDesktopConfigTOML)
	if err := os.WriteFile(primaryPath, raw, 0o644); err == nil {
		return primaryPath, nil
	}

	// Fallback for read-only install directories (e.g. Program Files).
	appDataDir, err := a.AppDataDir()
	if err != nil {
		return "", fmt.Errorf("write %s failed and app data unavailable: %w", primaryPath, err)
	}
	if mkErr := os.MkdirAll(appDataDir, 0o755); mkErr != nil {
		return "", fmt.Errorf("create app data dir: %w", mkErr)
	}

	fallbackPath := filepath.Join(appDataDir, "config.toml")
	if fileExists(fallbackPath) {
		return fallbackPath, nil
	}
	if err := os.WriteFile(fallbackPath, raw, 0o644); err != nil {
		return "", fmt.Errorf("write config fallback %s: %w", fallbackPath, err)
	}
	return fallbackPath, nil
}

func findBundledCUDARuntime(backendDir string) (binDir string, rootDir string) {
	candidates := []string{
		filepath.Join(backendDir, "runtime", "cuda"),
		filepath.Join(backendDir, "..", "runtime", "cuda"),
		filepath.Join(backendDir, "..", "..", "runtime", "cuda"),
		filepath.Join(backendDir, "..", "..", "..", "runtime", "cuda"),
	}
	for _, candidate := range candidates {
		bin := filepath.Join(candidate, "bin")
		if dirExists(bin) {
			return bin, candidate
		}
	}
	return "", ""
}

func findBundledVenvScripts(backendDir string) string {
	candidates := []string{
		filepath.Join(backendDir, "runtime", "venv"),
		filepath.Join(backendDir, "..", "runtime", "venv"),
		filepath.Join(backendDir, "..", "..", "runtime", "venv"),
		filepath.Join(backendDir, "..", "..", "..", "runtime", "venv"),
	}
	scriptsDir := "bin"
	if runtime.GOOS == "windows" {
		scriptsDir = "Scripts"
	}
	for _, candidate := range candidates {
		scripts := filepath.Join(candidate, scriptsDir)
		if dirExists(scripts) {
			return scripts
		}
	}
	return ""
}

func findBundledFFmpegBin(backendDir string) string {
	candidates := []string{
		filepath.Join(backendDir, "runtime", "ffmpeg"),
		filepath.Join(backendDir, "..", "runtime", "ffmpeg"),
		filepath.Join(backendDir, "..", "..", "runtime", "ffmpeg"),
		filepath.Join(backendDir, "..", "..", "..", "runtime", "ffmpeg"),
	}
	for _, candidate := range candidates {
		bin := filepath.Join(candidate, "bin")
		if fileExists(filepath.Join(bin, binaryWithExe("ffmpeg"))) {
			return bin
		}
		if fileExists(filepath.Join(candidate, binaryWithExe("ffmpeg"))) {
			return candidate
		}
	}
	return ""
}

func prependEnvPath(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	sep := string(os.PathListSeparator)
	current := os.Getenv("PATH")
	if current == "" {
		return setEnvValue(env, "PATH", dir)
	}
	return setEnvValue(env, "PATH", dir+sep+current)
}

func setEnvValue(env []string, key string, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if hasEnvKey(entry, key) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, prefix+value)
}

func hasEnvKey(entry string, key string) bool {
	i := strings.IndexByte(entry, '=')
	if i <= 0 {
		return false
	}
	name := entry[:i]
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, key)
	}
	return name == key
}

func (a *App) setLastBackendErr(message string) {
	a.statusMu.Lock()
	defer a.statusMu.Unlock()
	a.lastBackendErr = message
}

func (a *App) getLastBackendErr() string {
	a.statusMu.RLock()
	defer a.statusMu.RUnlock()
	return a.lastBackendErr
}

func (a *App) publishRuntimeBootstrap(status string, stage string, message string) {
	status = strings.TrimSpace(status)
	stage = strings.TrimSpace(stage)
	message = strings.TrimSpace(message)
	if status == "" || message == "" {
		return
	}

	if status == "running" {
		a.setLastBackendErr("초기 설정 진행 중: " + message)
	}

	if a.ctx == nil {
		return
	}
	wruntime.EventsEmit(a.ctx, "you2midi:runtime-bootstrap", runtimeBootstrapEvent{
		Status:       status,
		Stage:        stage,
		Message:      message,
		UpdatedAtUTC: time.Now().UTC().Format(time.RFC3339),
	})
}

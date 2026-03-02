package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAppExecutable     = "you2midi-desktop.exe"
	defaultUpdaterExecutable = "you2midi-updater.exe"
	defaultInstallerPattern  = `^You2Midi-Setup-.*\.exe$`
	defaultPatchPattern      = `^you2midi-patch-.*\.zip$`
	defaultGitHubAPIBase     = "https://api.github.com"
	defaultHTTPTimeout       = 30 * time.Second
)

type launcherConfig struct {
	GitHubRepo            string   `json:"github_repo"`
	GitHubAPIBase         string   `json:"github_api_base"`
	InstallerAssetPattern string   `json:"installer_asset_pattern"`
	PatchAssetPattern     string   `json:"patch_asset_pattern"`
	AppExecutable         string   `json:"app_executable"`
	UpdaterExecutable     string   `json:"updater_executable"`
	InstallDirCandidates  []string `json:"install_dir_candidates"`
	RequestTimeoutSeconds int      `json:"request_timeout_seconds"`
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type launcherState struct {
	LastReleaseTag string `json:"last_release_tag"`
	UpdatedAtUTC   string `json:"updated_at_utc"`
}

func main() {
	if err := run(); err != nil {
		_ = showError("You2Midi Launcher", err.Error())
		os.Exit(1)
	}
}

func run() error {
	configPathFlag := flag.String("config", "", "launcher config path (default: alongside launcher)")
	appDirFlag := flag.String("app-dir", "", "override app directory")
	skipUpdate := flag.Bool("skip-update", false, "skip update check")
	flag.Parse()
	reportStatus("Launcher started")

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve launcher executable path: %w", err)
	}
	exeDir := filepath.Dir(exePath)

	cfgPath := strings.TrimSpace(*configPathFlag)
	if cfgPath == "" {
		cfgPath = filepath.Join(exeDir, "launcher-config.json")
	}
	reportStatus("Loading launcher config: %s", cfgPath)
	cfg, err := loadLauncherConfig(cfgPath)
	if err != nil {
		return err
	}

	appDir, desktopExe := findInstalledDesktop(cfg, exeDir, strings.TrimSpace(*appDirFlag))
	if appDir == "" {
		reportStatus("Installed app not found; entering first-install flow")
		return runFirstInstallFlow(cfg)
	}
	reportStatus("Installed app found: %s", appDir)

	if !*skipUpdate {
		reportStatus("Checking updates from GitHub Releases")
		updated, err := tryStartUpdater(cfg, appDir)
		if err != nil {
			reportStatus("Update check failed: %v", err)
			_ = showInfo("You2Midi Launcher", "업데이트 확인에 실패하여 현재 버전으로 실행합니다.\n\n"+err.Error())
		}
		if updated {
			reportStatus("Updater started; launcher exits now")
			return nil
		}
	}

	notifyFirstRunRuntimeBootstrap(appDir)
	reportStatus("Launching desktop app: %s", desktopExe)
	return launchExecutable(desktopExe, appDir)
}

func loadLauncherConfig(path string) (*launcherConfig, error) {
	cfg := &launcherConfig{
		GitHubRepo:            strings.TrimSpace(os.Getenv("YOU2MIDI_GITHUB_REPO")),
		GitHubAPIBase:         defaultGitHubAPIBase,
		InstallerAssetPattern: defaultInstallerPattern,
		PatchAssetPattern:     defaultPatchPattern,
		AppExecutable:         defaultAppExecutable,
		UpdaterExecutable:     defaultUpdaterExecutable,
		InstallDirCandidates: []string{
			`%ProgramFiles%\You2Midi`,
			`%LOCALAPPDATA%\Programs\You2Midi`,
		},
		RequestTimeoutSeconds: int(defaultHTTPTimeout.Seconds()),
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read launcher config: %w", err)
	}

	var decoded launcherConfig
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("parse launcher config: %w", err)
	}

	if strings.TrimSpace(decoded.GitHubRepo) != "" {
		cfg.GitHubRepo = strings.TrimSpace(decoded.GitHubRepo)
	}
	if strings.TrimSpace(decoded.GitHubAPIBase) != "" {
		cfg.GitHubAPIBase = strings.TrimSpace(decoded.GitHubAPIBase)
	}
	if strings.TrimSpace(decoded.InstallerAssetPattern) != "" {
		cfg.InstallerAssetPattern = strings.TrimSpace(decoded.InstallerAssetPattern)
	}
	if strings.TrimSpace(decoded.PatchAssetPattern) != "" {
		cfg.PatchAssetPattern = strings.TrimSpace(decoded.PatchAssetPattern)
	}
	if strings.TrimSpace(decoded.AppExecutable) != "" {
		cfg.AppExecutable = strings.TrimSpace(decoded.AppExecutable)
	}
	if strings.TrimSpace(decoded.UpdaterExecutable) != "" {
		cfg.UpdaterExecutable = strings.TrimSpace(decoded.UpdaterExecutable)
	}
	if len(decoded.InstallDirCandidates) > 0 {
		cfg.InstallDirCandidates = decoded.InstallDirCandidates
	}
	if decoded.RequestTimeoutSeconds > 0 {
		cfg.RequestTimeoutSeconds = decoded.RequestTimeoutSeconds
	}

	return cfg, nil
}

func findInstalledDesktop(cfg *launcherConfig, launcherDir string, overrideAppDir string) (string, string) {
	candidates := make([]string, 0, 8)
	appendCandidate := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		candidates = append(candidates, path)
	}

	appendCandidate(overrideAppDir)
	appendCandidate(launcherDir)

	for _, c := range cfg.InstallDirCandidates {
		appendCandidate(expandPathCandidates(c))
	}
	for _, c := range probeRegisteredInstallDirs() {
		appendCandidate(c)
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, exists := seen[abs]; exists {
			continue
		}
		seen[abs] = struct{}{}

		desktopPath := filepath.Join(abs, cfg.AppExecutable)
		if fileExists(desktopPath) {
			return abs, desktopPath
		}
	}

	return "", ""
}

func runFirstInstallFlow(cfg *launcherConfig) error {
	if strings.TrimSpace(cfg.GitHubRepo) == "" {
		return errors.New("app is not installed and github_repo is missing in launcher-config.json")
	}
	reportStatus("First install: fetching latest release for %s", cfg.GitHubRepo)

	release, err := fetchLatestRelease(cfg)
	if err != nil {
		return err
	}
	installerAsset, err := selectReleaseAsset(release.Assets, cfg.InstallerAssetPattern)
	if err != nil {
		return err
	}
	if installerAsset == nil {
		return fmt.Errorf("installer asset not found (pattern: %s)", cfg.InstallerAssetPattern)
	}
	reportStatus("First install: installer asset found: %s", installerAsset.Name)

	message := fmt.Sprintf("You2Midi가 설치되어 있지 않습니다.\n지금 설치하시겠습니까?\n\n릴리즈: %s", strings.TrimSpace(release.TagName))
	proceed, err := askYesNo("You2Midi 설치", message)
	if err != nil {
		return err
	}
	if !proceed {
		reportStatus("First install: user cancelled")
		return nil
	}

	reportStatus("First install: downloading installer from %s", installerAsset.BrowserDownloadURL)
	_ = showInfo("You2Midi 설치", "설치 파일 다운로드를 시작합니다.\n진행 상황은 콘솔/launcher.log에서 확인할 수 있습니다.")
	downloadPath, cleanup, err := downloadAsset(installerAsset, requestTimeout(cfg))
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	reportStatus("First install: installer downloaded: %s", downloadPath)

	reportStatus("First install: running installer")
	_ = showInfo("You2Midi 설치", "설치 프로그램을 실행합니다.")
	if err := runInstaller(downloadPath); err != nil {
		return err
	}

	reportStatus("First install: installer finished")
	_ = showInfo("You2Midi 설치", "설치가 완료되었습니다.\nLauncher를 다시 실행하면 You2Midi가 실행됩니다.")
	return nil
}

func tryStartUpdater(cfg *launcherConfig, appDir string) (bool, error) {
	if strings.TrimSpace(cfg.GitHubRepo) == "" {
		return false, nil
	}

	reportStatus("Update: fetching latest release for %s", cfg.GitHubRepo)
	release, err := fetchLatestRelease(cfg)
	if err != nil {
		return false, err
	}
	patchAsset, err := selectReleaseAsset(release.Assets, cfg.PatchAssetPattern)
	if err != nil {
		return false, nil
	}
	if patchAsset == nil {
		reportStatus("Update: patch asset not found in latest release")
		return false, nil
	}
	reportStatus("Update: patch asset found: %s", patchAsset.Name)

	statePath := launcherStatePath()
	state, _ := readLauncherState(statePath)
	if strings.TrimSpace(release.TagName) != "" && state.LastReleaseTag == strings.TrimSpace(release.TagName) {
		reportStatus("Update: already applied release %s, skipping", strings.TrimSpace(release.TagName))
		return false, nil
	}

	updaterPath := filepath.Join(appDir, cfg.UpdaterExecutable)
	if !fileExists(updaterPath) {
		reportStatus("Update: updater binary not found: %s", updaterPath)
		return false, nil
	}

	logPath := launcherUpdaterLogPath()
	reportStatus("Update: starting updater")
	reportStatus("Update: patch URL: %s", patchAsset.BrowserDownloadURL)
	reportStatus("Update: updater log: %s", logPath)
	_ = showInfo("You2Midi 업데이트", "패치 적용을 시작합니다.\n진행 상황은 콘솔 또는 updater.log에서 확인할 수 있습니다.")
	if err := startUpdater(updaterPath, appDir, cfg.AppExecutable, patchAsset.BrowserDownloadURL, logPath, statePath, strings.TrimSpace(release.TagName)); err != nil {
		return false, err
	}
	return true, nil
}

func fetchLatestRelease(cfg *launcherConfig) (*githubRelease, error) {
	owner, repo, err := parseGitHubRepo(cfg.GitHubRepo)
	if err != nil {
		return nil, err
	}

	base := strings.TrimRight(strings.TrimSpace(cfg.GitHubAPIBase), "/")
	if base == "" {
		base = defaultGitHubAPIBase
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/latest", base, owner, repo)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout(cfg))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "You2Midi-Launcher")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("release api http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release response: %w", err)
	}
	return &release, nil
}

func parseGitHubRepo(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".git")
	if strings.HasPrefix(strings.ToLower(value), "https://github.com/") || strings.HasPrefix(strings.ToLower(value), "http://github.com/") {
		u, err := url.Parse(value)
		if err == nil {
			value = strings.Trim(u.Path, "/")
		}
	}
	value = strings.TrimPrefix(value, "github.com/")

	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("invalid github_repo %q (expected owner/repo)", value)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func selectReleaseAsset(assets []githubAsset, pattern string) (*githubAsset, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid asset regex %q: %w", pattern, err)
	}

	matched := make([]githubAsset, 0, len(assets))
	for _, asset := range assets {
		if !re.MatchString(asset.Name) {
			continue
		}
		if strings.TrimSpace(asset.BrowserDownloadURL) == "" {
			continue
		}
		matched = append(matched, asset)
	}
	if len(matched) == 0 {
		return nil, nil
	}

	sort.Slice(matched, func(i, j int) bool {
		return strings.ToLower(matched[i].Name) > strings.ToLower(matched[j].Name)
	})
	selected := matched[0]
	return &selected, nil
}

func downloadAsset(asset *githubAsset, timeout time.Duration) (string, func(), error) {
	if asset == nil {
		return "", nil, errors.New("asset is nil")
	}

	u, err := url.Parse(strings.TrimSpace(asset.BrowserDownloadURL))
	if err != nil {
		return "", nil, fmt.Errorf("parse asset url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", nil, fmt.Errorf("unsupported asset url scheme: %s", u.Scheme)
	}

	tmpDir, err := os.MkdirTemp("", "you2midi-launcher-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	fileName := sanitizeFileName(asset.Name)
	if fileName == "" {
		fileName = "download.bin"
	}
	dst := filepath.Join(tmpDir, fileName)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("User-Agent", "You2Midi-Launcher")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		cleanup()
		return "", nil, fmt.Errorf("asset download http %d", resp.StatusCode)
	}

	out, err := os.Create(dst)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create downloaded file: %w", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		cleanup()
		return "", nil, fmt.Errorf("write downloaded file: %w", err)
	}
	if err := out.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close downloaded file: %w", err)
	}

	return dst, cleanup, nil
}

func runInstaller(installerPath string) error {
	cmd := exec.Command(installerPath)
	cmd.Dir = filepath.Dir(installerPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run installer: %w", err)
	}
	return nil
}

func startUpdater(updaterPath string, appDir string, launchExecutable string, patchURL string, logPath string, statePath string, releaseTag string) error {
	args := []string{
		"-patch", patchURL,
		"-app-dir", appDir,
		"-parent-pid", strconv.Itoa(os.Getpid()),
		"-launch", launchExecutable,
	}
	if strings.TrimSpace(logPath) != "" {
		args = append(args, "-log-file", logPath)
	}
	if updaterSupportsStateFlags(updaterPath) && strings.TrimSpace(statePath) != "" && strings.TrimSpace(releaseTag) != "" {
		args = append(args, "-state-file", statePath, "-state-release-tag", strings.TrimSpace(releaseTag))
	}

	cmd := exec.Command(updaterPath, args...)
	cmd.Dir = appDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start updater process: %w", err)
	}
	return nil
}

func updaterSupportsStateFlags(updaterPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, updaterPath, "-h")
	output, err := cmd.CombinedOutput()
	text := strings.ToLower(string(output))
	if strings.Contains(text, "state-file") && strings.Contains(text, "state-release-tag") {
		return true
	}
	if err != nil {
		return false
	}
	return false
}

func launchExecutable(executablePath string, workDir string) error {
	if !fileExists(executablePath) {
		return fmt.Errorf("desktop executable not found: %s", executablePath)
	}
	cmd := exec.Command(executablePath)
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch desktop executable: %w", err)
	}
	return nil
}

func launcherStatePath() string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "You2Midi", "launcher-state.json")
}

func launcherUpdaterLogPath() string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "You2Midi", "logs", "updater.log")
}

func launcherLogPath() string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "You2Midi", "logs", "launcher.log")
}

func notifyFirstRunRuntimeBootstrap(appDir string) {
	manifestPath := filepath.Join(appDir, "runtime", "python-runtime.json")
	markerPath := filepath.Join(appDir, "runtime", "python", ".deps_ready.json")
	if !fileExists(manifestPath) || fileExists(markerPath) {
		return
	}
	reportStatus("Runtime bootstrap pending (marker missing): %s", markerPath)
	_ = showInfo(
		"You2Midi 초기 설정",
		"최초 실행에서는 Python/AI 런타임 설치가 진행됩니다.\n"+
			"네트워크 환경에 따라 5~20분 이상 걸릴 수 있습니다.\n"+
			"설치가 끝나면 You2Midi 창이 자동으로 열립니다.",
	)
}

func reportStatus(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	line := fmt.Sprintf("[%s] %s", time.Now().Format("2006-01-02 15:04:05"), msg)
	_, _ = fmt.Fprintln(os.Stdout, line)

	logPath := launcherLogPath()
	if strings.TrimSpace(logPath) == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintln(f, line)
}
func readLauncherState(path string) (*launcherState, error) {
	if strings.TrimSpace(path) == "" {
		return &launcherState{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &launcherState{}, nil
		}
		return nil, err
	}
	var state launcherState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func requestTimeout(cfg *launcherConfig) time.Duration {
	seconds := cfg.RequestTimeoutSeconds
	if seconds <= 0 {
		seconds = int(defaultHTTPTimeout.Seconds())
	}
	return time.Duration(seconds) * time.Second
}

func expandPathCandidates(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	re := regexp.MustCompile(`%([^%]+)%`)
	expanded := re.ReplaceAllStringFunc(trimmed, func(token string) string {
		key := strings.Trim(token, "%")
		if key == "" {
			return token
		}
		env := os.Getenv(key)
		if env == "" {
			return token
		}
		return env
	})
	expanded = os.ExpandEnv(expanded)
	return strings.TrimSpace(expanded)
}

func sanitizeFileName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	clean = strings.ReplaceAll(clean, "/", "_")
	clean = strings.ReplaceAll(clean, "\\", "_")
	return clean
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

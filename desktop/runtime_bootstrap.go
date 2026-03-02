package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const pythonRuntimeManifestName = "python-runtime.json"

const (
	runtimeTorchVersion      = "2.10.0+cu128"
	runtimeTorchaudioVersion = "2.10.0+cu128"
	runtimeTranskunVersion   = "2.0.1"
	runtimeYtDlpVersion      = "2026.2.21"
	runtimeNumpyVersion      = "2.4.2"
	runtimeSoxrVersion       = "1.0.0"
	runtimeModuleconfVersion = "0.1.4"
	runtimeMidoVersion       = "1.3.3"
	runtimePrettyMidiVersion = "0.2.11"
	runtimePydubVersion      = "0.25.1"
	runtimeDepsMarkerFile    = ".deps_ready.json"
	runtimeDepsProfile       = "py312-cu128-transkun2.0.1-ytdlp2026.2.21-v3-pinned"
)

type pythonRuntimeManifest struct {
	Version       string `json:"version"`
	ArchiveURL    string `json:"archive_url"`
	ArchiveSHA256 string `json:"archive_sha256"`
	ScriptsRel    string `json:"scripts_rel_path"`
	ArchiveType   string `json:"archive_type"`
}

type runtimeDepsMarker struct {
	Profile          string `json:"profile"`
	RuntimeVersion   string `json:"runtime_version"`
	ResolvedAtUTC    string `json:"resolved_at_utc"`
	PythonExecutable string `json:"python_executable"`
}

func (a *App) resolvePythonRuntimeScripts(backendDir string) (string, error) {
	// Backward-compatible legacy layout.
	if scripts := findBundledVenvScripts(backendDir); scripts != "" {
		a.publishRuntimeBootstrap("running", "runtime", "내장 Python 런타임을 확인하는 중입니다.")
		return scripts, nil
	}
	manifestPath, runtimeRoot := findPythonRuntimeManifest(backendDir)
	scripts := findBundledPythonScripts(backendDir)
	if manifestPath == "" {
		// Preferred managed runtime layout without remote manifest.
		if scripts != "" {
			return scripts, nil
		}
		return "", nil
	}

	a.runtimeMu.Lock()
	defer a.runtimeMu.Unlock()

	// Re-resolve after lock.
	scripts = findBundledPythonScripts(backendDir)

	manifest, err := loadPythonRuntimeManifest(manifestPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(manifest.ArchiveURL) == "" {
		return "", fmt.Errorf("%s missing archive_url", manifestPath)
	}
	archiveType := normalizeArchiveType(manifest.ArchiveType, manifest.ArchiveURL)
	switch archiveType {
	case "zip", "tar.gz":
	default:
		return "", fmt.Errorf("unsupported python runtime archive_type %q (supported: zip, tar.gz)", archiveType)
	}

	if scripts != "" {
		if healthy, reason := pythonRuntimeHealthy(runtimeRoot, scripts); !healthy {
			if a.ctx != nil {
				wruntime.LogWarning(a.ctx, fmt.Sprintf("python runtime appears corrupted; reprovisioning: %s", reason))
			}
			a.publishRuntimeBootstrap("running", "repair", "Python 런타임 상태가 손상되어 복구를 시작합니다.")
			_ = os.Remove(filepath.Join(runtimeRoot, runtimeDepsMarkerFile))
			scripts = ""
		}
	}

	if scripts == "" {
		if a.ctx != nil {
			wruntime.LogInfo(a.ctx, "python runtime missing; provisioning from remote archive")
		}
		a.publishRuntimeBootstrap("running", "download", "Python 런타임을 다운로드하는 중입니다.")
		if err := provisionPythonRuntime(runtimeRoot, manifest, archiveType); err != nil {
			return "", err
		}
		a.publishRuntimeBootstrap("running", "extract", "Python 런타임 파일을 설치하는 중입니다.")
		scripts = findBundledPythonScripts(backendDir)
		if scripts == "" {
			return "", fmt.Errorf("python runtime provisioned but scripts dir not found under %s", runtimeRoot)
		}
		if healthy, reason := pythonRuntimeHealthy(runtimeRoot, scripts); !healthy {
			return "", fmt.Errorf("python runtime provisioned but health check failed: %s", reason)
		}
	}

	if err := a.ensurePythonRuntimeDependencies(runtimeRoot, scripts, manifest); err != nil {
		return "", err
	}
	return scripts, nil
}

func findPythonRuntimeManifest(backendDir string) (manifestPath string, runtimeRoot string) {
	candidates := []string{
		filepath.Join(backendDir, "runtime"),
		filepath.Join(backendDir, "..", "runtime"),
		filepath.Join(backendDir, "..", "..", "runtime"),
		filepath.Join(backendDir, "..", "..", "..", "runtime"),
	}
	for _, runtimeDir := range candidates {
		manifest := filepath.Join(runtimeDir, pythonRuntimeManifestName)
		if fileExists(manifest) {
			return manifest, filepath.Join(runtimeDir, "python")
		}
	}
	return "", ""
}

func findBundledPythonScripts(backendDir string) string {
	candidates := []string{
		filepath.Join(backendDir, "runtime", "python"),
		filepath.Join(backendDir, "..", "runtime", "python"),
		filepath.Join(backendDir, "..", "..", "runtime", "python"),
		filepath.Join(backendDir, "..", "..", "..", "runtime", "python"),
	}
	scriptsDir := "bin"
	if runtime.GOOS == "windows" {
		scriptsDir = "Scripts"
	}
	for _, candidate := range candidates {
		scripts := filepath.Join(candidate, scriptsDir)
		if !dirExists(scripts) {
			continue
		}
		if fileExists(filepath.Join(scripts, binaryWithExe("python"))) {
			return scripts
		}
		// python-build-standalone install_only layout keeps python.exe at runtime root
		// and scripts in runtimeRoot/Scripts.
		if fileExists(filepath.Join(candidate, binaryWithExe("python"))) {
			return scripts
		}
	}
	return ""
}

func loadPythonRuntimeManifest(path string) (*pythonRuntimeManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runtime manifest: %w", err)
	}
	var manifest pythonRuntimeManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse runtime manifest: %w", err)
	}
	return &manifest, nil
}

func provisionPythonRuntime(runtimeRoot string, manifest *pythonRuntimeManifest, archiveType string) error {
	if strings.TrimSpace(runtimeRoot) == "" {
		return errors.New("runtime root is empty")
	}

	parentDir := filepath.Dir(runtimeRoot)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("create runtime parent dir: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "you2midi-python-runtime-*")
	if err != nil {
		return fmt.Errorf("create temp runtime dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archiveName := "runtime.zip"
	if archiveType == "tar.gz" {
		archiveName = "runtime.tar.gz"
	}
	archivePath := filepath.Join(tmpDir, archiveName)
	if err := downloadFile(manifest.ArchiveURL, archivePath, 10*time.Minute); err != nil {
		return fmt.Errorf("download runtime archive: %w", err)
	}
	if strings.TrimSpace(manifest.ArchiveSHA256) != "" {
		sum, err := fileSHA256Hex(archivePath)
		if err != nil {
			return fmt.Errorf("hash runtime archive: %w", err)
		}
		if !strings.EqualFold(sum, strings.TrimSpace(manifest.ArchiveSHA256)) {
			return fmt.Errorf("runtime archive sha256 mismatch")
		}
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return fmt.Errorf("create extract dir: %w", err)
	}
	switch archiveType {
	case "zip":
		if err := extractZipSecure(archivePath, extractDir); err != nil {
			return fmt.Errorf("extract runtime zip: %w", err)
		}
	case "tar.gz":
		if err := extractTarGzSecure(archivePath, extractDir); err != nil {
			return fmt.Errorf("extract runtime tar.gz: %w", err)
		}
	default:
		return fmt.Errorf("unsupported archive type %q", archiveType)
	}

	scriptsRel := strings.TrimSpace(manifest.ScriptsRel)
	if scriptsRel == "" {
		if runtime.GOOS == "windows" {
			scriptsRel = "Scripts"
		} else {
			scriptsRel = "bin"
		}
	}

	installSource, err := resolveRuntimeRootFromExtract(extractDir, scriptsRel)
	if err != nil {
		return err
	}

	stagingRoot := runtimeRoot + ".new"
	backupRoot := runtimeRoot + ".old"
	_ = os.RemoveAll(stagingRoot)
	_ = os.RemoveAll(backupRoot)

	if err := copyDir(installSource, stagingRoot); err != nil {
		return fmt.Errorf("stage runtime files: %w", err)
	}

	if err := os.RemoveAll(backupRoot); err != nil {
		return fmt.Errorf("cleanup runtime backup: %w", err)
	}
	if dirExists(runtimeRoot) {
		if err := os.Rename(runtimeRoot, backupRoot); err != nil {
			return fmt.Errorf("backup old runtime: %w", err)
		}
	}
	if err := os.Rename(stagingRoot, runtimeRoot); err != nil {
		_ = os.Rename(backupRoot, runtimeRoot)
		return fmt.Errorf("activate new runtime: %w", err)
	}
	_ = os.RemoveAll(backupRoot)
	return nil
}

func resolveRuntimeRootFromExtract(extractDir string, scriptsRel string) (string, error) {
	scriptsRel = filepath.FromSlash(strings.Trim(strings.ReplaceAll(scriptsRel, "\\", "/"), "/"))
	candidates := []string{
		filepath.Join(extractDir, scriptsRel),
	}
	if children, err := os.ReadDir(extractDir); err == nil && len(children) == 1 && children[0].IsDir() {
		candidates = append(candidates, filepath.Join(extractDir, children[0].Name(), scriptsRel))
	}

	for _, scriptsPath := range candidates {
		if !dirExists(scriptsPath) {
			continue
		}
		if fileExists(filepath.Join(scriptsPath, binaryWithExe("python"))) {
			return filepath.Dir(scriptsPath), nil
		}
		parent := filepath.Dir(scriptsPath)
		if fileExists(filepath.Join(parent, binaryWithExe("python"))) {
			return parent, nil
		}
	}
	return "", fmt.Errorf("runtime archive does not contain %q with python executable", scriptsRel)
}

func downloadFile(url string, destPath string, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return nil
}

func fileSHA256Hex(path string) (string, error) {
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

func extractZipSecure(zipPath string, destDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		targetPath, err := safeZipTarget(destDir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func safeZipTarget(destDir string, fileName string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(fileName))
	if clean == "." || clean == "" {
		return "", errors.New("empty zip entry path")
	}
	target := filepath.Join(destDir, clean)
	rel, err := filepath.Rel(destDir, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("zip entry escapes destination: %s", fileName)
	}
	return target, nil
}

func extractTarGzSecure(tarGzPath string, destDir string) error {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr == nil {
			continue
		}

		targetPath, err := safeZipTarget(destDir, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tar entry type %d for %q", hdr.Typeflag, hdr.Name)
		}
	}
	return nil
}

func normalizeArchiveType(rawType string, archiveURL string) string {
	t := strings.ToLower(strings.TrimSpace(rawType))
	switch t {
	case "", "auto":
		u := strings.ToLower(strings.TrimSpace(archiveURL))
		switch {
		case strings.HasSuffix(u, ".tar.gz"), strings.HasSuffix(u, ".tgz"):
			return "tar.gz"
		case strings.HasSuffix(u, ".zip"):
			return "zip"
		default:
			return "zip"
		}
	case "zip":
		return "zip"
	case "tar.gz", "tgz", "tar-gz":
		return "tar.gz"
	default:
		return t
	}
}

func copyDir(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = in.Close()
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = in.Close()
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			_ = in.Close()
			return err
		}
		if err := out.Close(); err != nil {
			_ = in.Close()
			return err
		}
		return in.Close()
	})
}

func (a *App) ensurePythonRuntimeDependencies(runtimeRoot string, scriptsDir string, manifest *pythonRuntimeManifest) error {
	pythonBin := resolvePythonExecutable(runtimeRoot, scriptsDir)
	if pythonBin == "" {
		return fmt.Errorf("python executable not found under %s", runtimeRoot)
	}
	if !pythonStdlibPresent(runtimeRoot) {
		return fmt.Errorf("python stdlib missing under %s (expected Lib/encodings or python*.zip)", runtimeRoot)
	}
	if runtimeDepsReady(runtimeRoot, scriptsDir, manifest.Version, pythonBin) {
		a.publishRuntimeBootstrap("running", "deps", "필수 런타임이 이미 설치되어 있습니다.")
		return nil
	}

	if a.ctx != nil {
		wruntime.LogInfo(a.ctx, "installing python runtime dependencies (first run)")
	}
	a.publishRuntimeBootstrap("running", "deps", "Python 패키지 설치를 준비하는 중입니다.")

	// Ensure pip exists.
	a.publishRuntimeBootstrap("running", "pip", "pip 환경을 점검하는 중입니다.")
	if err := runPython(pythonBin, 2*time.Minute, "-m", "pip", "--version"); err != nil {
		if a.ctx != nil {
			wruntime.LogInfo(a.ctx, "pip not found, running ensurepip")
		}
		a.publishRuntimeBootstrap("running", "pip", "pip을 초기화하는 중입니다.")
		if ensureErr := runPython(pythonBin, 5*time.Minute, "-m", "ensurepip", "--upgrade"); ensureErr != nil {
			return fmt.Errorf("ensure pip: %w", ensureErr)
		}
	}

	a.publishRuntimeBootstrap("running", "pip-tools", "pip/wheel/setuptools를 업데이트하는 중입니다.")
	if err := runPython(
		pythonBin,
		10*time.Minute,
		"-m", "pip", "install", "--upgrade",
		"pip",
		"wheel",
		"setuptools<71",
	); err != nil {
		return fmt.Errorf("bootstrap pip tooling: %w", err)
	}

	if a.ctx != nil {
		wruntime.LogInfo(a.ctx, "installing torch/torchaudio runtime (this may take several minutes)")
	}
	a.publishRuntimeBootstrap("running", "torch", "AI 런타임(torch/torchaudio)을 설치하는 중입니다. 시간이 오래 걸릴 수 있습니다.")
	if err := runPython(
		pythonBin,
		60*time.Minute,
		"-m", "pip", "install", "--upgrade",
		"--index-url", "https://download.pytorch.org/whl/cu128",
		"--extra-index-url", "https://pypi.org/simple",
		"torch=="+runtimeTorchVersion,
		"torchaudio=="+runtimeTorchaudioVersion,
	); err != nil {
		return fmt.Errorf("install torch runtime: %w", err)
	}

	if a.ctx != nil {
		wruntime.LogInfo(a.ctx, "installing transcription dependencies")
	}
	a.publishRuntimeBootstrap("running", "tools", "추론 필수 패키지를 설치하는 중입니다.")
	if err := runPython(
		pythonBin,
		20*time.Minute,
		"-m", "pip", "install", "--upgrade", "--only-binary=:all:",
		"numpy=="+runtimeNumpyVersion,
		"soxr=="+runtimeSoxrVersion,
	); err != nil {
		return fmt.Errorf("install binary runtime prerequisites: %w", err)
	}

	if err := runPython(
		pythonBin,
		20*time.Minute,
		"-m", "pip", "install", "--upgrade",
		"moduleconf=="+runtimeModuleconfVersion,
		"mido=="+runtimeMidoVersion,
		"pydub=="+runtimePydubVersion,
	); err != nil {
		return fmt.Errorf("install pure-python runtime prerequisites: %w", err)
	}

	// pretty_midi currently ships sdist only; install without deps after explicit deps are satisfied.
	if err := runPython(
		pythonBin,
		10*time.Minute,
		"-m", "pip", "install", "--upgrade", "--no-deps",
		"pretty_midi=="+runtimePrettyMidiVersion,
	); err != nil {
		return fmt.Errorf("install pretty_midi runtime: %w", err)
	}

	// transkun declares training/eval dependencies (including ncls) that trigger native builds on Windows.
	// For end-user inference runtime, install transkun without deps and provide only required runtime deps above.
	if err := runPython(
		pythonBin,
		20*time.Minute,
		"-m", "pip", "install", "--upgrade", "--no-deps",
		"transkun=="+runtimeTranskunVersion,
	); err != nil {
		return fmt.Errorf("install transkun runtime (no-deps): %w", err)
	}

	if err := runPython(
		pythonBin,
		10*time.Minute,
		"-m", "pip", "install", "--upgrade",
		"yt-dlp=="+runtimeYtDlpVersion,
	); err != nil {
		return fmt.Errorf("install yt-dlp runtime: %w", err)
	}

	// Verify imports at end to avoid false-ready marker.
	a.publishRuntimeBootstrap("running", "verify", "설치 결과를 검증하는 중입니다.")
	if err := runPython(
		pythonBin,
		2*time.Minute,
		"-c", "import torch, torchaudio, transkun, yt_dlp; print(torch.__version__)",
	); err != nil {
		return fmt.Errorf("verify runtime dependencies: %w", err)
	}
	if !fileExists(filepath.Join(scriptsDir, binaryWithExe("transkun"))) {
		return fmt.Errorf("transkun executable missing in %s", scriptsDir)
	}
	if !fileExists(filepath.Join(scriptsDir, binaryWithExe("yt-dlp"))) {
		return fmt.Errorf("yt-dlp executable missing in %s", scriptsDir)
	}

	if err := writeRuntimeDepsMarker(runtimeRoot, manifest.Version, pythonBin); err != nil {
		return fmt.Errorf("write runtime deps marker: %w", err)
	}
	if a.ctx != nil {
		wruntime.LogInfo(a.ctx, "python runtime dependencies installed successfully")
	}
	a.publishRuntimeBootstrap("done", "deps", "Python/AI 런타임 설치가 완료되었습니다.")
	return nil
}

func runtimeDepsReady(runtimeRoot string, scriptsDir string, runtimeVersion string, pythonBin string) bool {
	if !fileExists(pythonBin) {
		return false
	}
	if !pythonStdlibPresent(runtimeRoot) {
		return false
	}

	markerPath := filepath.Join(runtimeRoot, runtimeDepsMarkerFile)
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return false
	}
	var marker runtimeDepsMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return false
	}
	if marker.Profile != runtimeDepsProfile {
		return false
	}
	if strings.TrimSpace(runtimeVersion) != "" && marker.RuntimeVersion != runtimeVersion {
		return false
	}
	if marker.PythonExecutable != pythonBin {
		return false
	}
	if !fileExists(filepath.Join(scriptsDir, binaryWithExe("transkun"))) {
		return false
	}
	if !fileExists(filepath.Join(scriptsDir, binaryWithExe("yt-dlp"))) {
		return false
	}
	return true
}

func resolvePythonExecutable(runtimeRoot string, scriptsDir string) string {
	candidates := []string{
		filepath.Join(scriptsDir, binaryWithExe("python")),
		filepath.Join(runtimeRoot, binaryWithExe("python")),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func pythonStdlibPresent(runtimeRoot string) bool {
	if fileExists(filepath.Join(runtimeRoot, "Lib", "encodings", "__init__.py")) {
		return true
	}
	matches, err := filepath.Glob(filepath.Join(runtimeRoot, "python*.zip"))
	return err == nil && len(matches) > 0
}

func pythonRuntimeHealthy(runtimeRoot string, scriptsDir string) (bool, string) {
	pythonBin := resolvePythonExecutable(runtimeRoot, scriptsDir)
	if pythonBin == "" {
		return false, "python executable missing"
	}
	if !pythonStdlibPresent(runtimeRoot) {
		return false, "stdlib missing (Lib/encodings and python*.zip not found)"
	}
	if err := runPython(pythonBin, 30*time.Second, "-c", "import encodings; import sys; print(sys.prefix)"); err != nil {
		return false, fmt.Sprintf("python startup check failed: %v", err)
	}
	return true, ""
}

func writeRuntimeDepsMarker(runtimeRoot string, runtimeVersion string, pythonBin string) error {
	marker := runtimeDepsMarker{
		Profile:          runtimeDepsProfile,
		RuntimeVersion:   runtimeVersion,
		ResolvedAtUTC:    time.Now().UTC().Format(time.RFC3339),
		PythonExecutable: pythonBin,
	}
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(runtimeRoot, runtimeDepsMarkerFile)
	return os.WriteFile(path, raw, 0o644)
}

func runPython(pythonBin string, timeout time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, pythonBin, args...)
	env := os.Environ()
	env = append(env,
		"PIP_DISABLE_PIP_VERSION_CHECK=1",
		"PIP_NO_INPUT=1",
		"PYTHONDONTWRITEBYTECODE=1",
	)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if len(msg) > 800 {
			msg = msg[len(msg)-800:]
		}
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

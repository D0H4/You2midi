package updater

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const patchManifestFile = "patch-manifest.json"

// PatchManifest describes a patch ZIP payload.
type PatchManifest struct {
	Version          string      `json:"version"`
	CreatedAtUTC     string      `json:"created_at_utc"`
	LaunchExecutable string      `json:"launch_executable"`
	Files            []PatchFile `json:"files"`
}

// PatchFile describes a single patched file.
type PatchFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// Logger is the minimal logger contract used by patch application.
type Logger interface {
	Printf(format string, v ...any)
}

// ApplyOptions controls patch application behavior.
type ApplyOptions struct {
	Retries    int
	RetryDelay time.Duration
	Logger     Logger
	SkipFiles  map[string]struct{}
}

// DefaultApplyOptions returns conservative defaults for desktop updates.
func DefaultApplyOptions() ApplyOptions {
	return ApplyOptions{
		Retries:    30,
		RetryDelay: 300 * time.Millisecond,
	}
}

// ApplyPatchZip validates and applies a patch ZIP onto appDir.
func ApplyPatchZip(appDir string, patchZipPath string, opts ApplyOptions) (*PatchManifest, error) {
	if strings.TrimSpace(appDir) == "" {
		return nil, errors.New("appDir is required")
	}
	if strings.TrimSpace(patchZipPath) == "" {
		return nil, errors.New("patchZipPath is required")
	}

	absAppDir, err := filepath.Abs(appDir)
	if err != nil {
		return nil, fmt.Errorf("resolve app dir: %w", err)
	}

	if opts.Retries < 1 {
		opts.Retries = 1
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = 300 * time.Millisecond
	}
	if opts.SkipFiles == nil {
		opts.SkipFiles = map[string]struct{}{}
	}

	zr, err := zip.OpenReader(patchZipPath)
	if err != nil {
		return nil, fmt.Errorf("open patch zip: %w", err)
	}
	defer zr.Close()

	manifest, err := readPatchManifest(zr.File)
	if err != nil {
		return nil, err
	}
	if len(manifest.Files) == 0 {
		return nil, errors.New("patch manifest must include at least one file")
	}

	stageDir, err := os.MkdirTemp("", "you2midi-patch-stage-*")
	if err != nil {
		return nil, fmt.Errorf("create stage dir: %w", err)
	}
	defer os.RemoveAll(stageDir)

	if err := extractPatchFiles(zr.File, stageDir); err != nil {
		return nil, err
	}
	if err := validateManifestFiles(stageDir, manifest); err != nil {
		return nil, err
	}

	for _, entry := range manifest.Files {
		rel, err := normalizeRelativePath(entry.Path)
		if err != nil {
			return nil, fmt.Errorf("manifest path %q invalid: %w", entry.Path, err)
		}
		if _, skip := opts.SkipFiles[rel]; skip {
			if opts.Logger != nil {
				opts.Logger.Printf("Skipping file in patch: %s", rel)
			}
			continue
		}

		src, err := resolvePathUnderRoot(stageDir, rel)
		if err != nil {
			return nil, err
		}
		dst, err := resolvePathUnderRoot(absAppDir, rel)
		if err != nil {
			return nil, err
		}

		if opts.Logger != nil {
			opts.Logger.Printf("Applying file: %s", rel)
		}
		if err := replaceFileWithRetries(src, dst, opts.Retries, opts.RetryDelay); err != nil {
			return nil, err
		}
	}

	return manifest, nil
}

// ResolvePathUnderRoot resolves rel beneath root and rejects path traversal.
func ResolvePathUnderRoot(root string, rel string) (string, error) {
	return resolvePathUnderRoot(root, rel)
}

func readPatchManifest(files []*zip.File) (*PatchManifest, error) {
	for _, f := range files {
		rel, err := normalizeRelativePath(f.Name)
		if err != nil {
			continue
		}
		if rel != patchManifestFile {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open patch manifest: %w", err)
		}
		defer rc.Close()

		var manifest PatchManifest
		if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
			return nil, fmt.Errorf("decode patch manifest: %w", err)
		}
		return &manifest, nil
	}
	return nil, fmt.Errorf("%s not found in patch", patchManifestFile)
}

func extractPatchFiles(files []*zip.File, stageDir string) error {
	for _, f := range files {
		rel, err := normalizeRelativePath(f.Name)
		if err != nil {
			return fmt.Errorf("invalid path in patch %q: %w", f.Name, err)
		}
		if rel == patchManifestFile {
			continue
		}

		dst, err := resolvePathUnderRoot(stageDir, rel)
		if err != nil {
			return err
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("create stage dir %q: %w", dst, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("create stage parent dir %q: %w", dst, err)
		}
		if err := writeZipEntryToFile(f, dst); err != nil {
			return err
		}
	}
	return nil
}

func validateManifestFiles(stageDir string, manifest *PatchManifest) error {
	seen := map[string]struct{}{}
	for _, entry := range manifest.Files {
		rel, err := normalizeRelativePath(entry.Path)
		if err != nil {
			return fmt.Errorf("manifest path %q invalid: %w", entry.Path, err)
		}
		if _, exists := seen[rel]; exists {
			return fmt.Errorf("duplicate manifest path %q", rel)
		}
		seen[rel] = struct{}{}

		stagePath, err := resolvePathUnderRoot(stageDir, rel)
		if err != nil {
			return err
		}
		info, err := os.Stat(stagePath)
		if err != nil {
			return fmt.Errorf("staged file missing %q: %w", rel, err)
		}
		if info.IsDir() {
			return fmt.Errorf("staged path is directory, expected file: %q", rel)
		}
		if entry.SizeBytes > 0 && info.Size() != entry.SizeBytes {
			return fmt.Errorf("size mismatch for %q: manifest=%d actual=%d", rel, entry.SizeBytes, info.Size())
		}
		if strings.TrimSpace(entry.SHA256) != "" {
			sum, err := fileSHA256(stagePath)
			if err != nil {
				return fmt.Errorf("hash file %q: %w", rel, err)
			}
			if !strings.EqualFold(sum, strings.TrimSpace(entry.SHA256)) {
				return fmt.Errorf("sha256 mismatch for %q", rel)
			}
		}
	}
	return nil
}

func writeZipEntryToFile(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %q: %w", f.Name, err)
	}
	defer rc.Close()

	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create stage file %q: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("extract zip entry %q: %w", f.Name, err)
	}
	return nil
}

func replaceFileWithRetries(src string, dst string, retries int, retryDelay time.Duration) error {
	var lastErr error
	for i := 0; i < retries; i++ {
		if err := replaceFile(src, dst); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(retryDelay)
	}
	return fmt.Errorf("replace file %q failed after %d attempts: %w", dst, retries, lastErr)
}

func replaceFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat source file: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}

	tmpDst := dst + ".updtmp"
	_ = os.Remove(tmpDst)

	out, err := os.OpenFile(tmpDst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("open temporary destination: %w", err)
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpDst)
		return fmt.Errorf("copy destination temp file: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpDst)
		return fmt.Errorf("close destination temp file: %w", closeErr)
	}

	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmpDst)
		return fmt.Errorf("remove existing destination: %w", err)
	}
	if err := os.Rename(tmpDst, dst); err != nil {
		_ = os.Remove(tmpDst)
		return fmt.Errorf("rename temp destination: %w", err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
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

func normalizeRelativePath(input string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(input), "\\", "/")
	normalized = strings.TrimPrefix(normalized, "/")
	normalized = path.Clean(normalized)

	if normalized == "." || normalized == "" {
		return "", errors.New("empty relative path")
	}
	if path.IsAbs(normalized) {
		return "", errors.New("absolute path is not allowed")
	}
	if strings.HasPrefix(normalized, "../") || normalized == ".." {
		return "", errors.New("path traversal is not allowed")
	}
	if len(normalized) >= 2 && normalized[1] == ':' {
		return "", errors.New("drive-qualified path is not allowed")
	}
	return normalized, nil
}

func resolvePathUnderRoot(root string, rel string) (string, error) {
	normalizedRel, err := normalizeRelativePath(rel)
	if err != nil {
		return "", err
	}

	joined := filepath.Join(root, filepath.FromSlash(normalizedRel))
	cleanRoot := filepath.Clean(root)
	cleanJoined := filepath.Clean(joined)

	relToRoot, err := filepath.Rel(cleanRoot, cleanJoined)
	if err != nil {
		return "", fmt.Errorf("resolve path under root: %w", err)
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(os.PathSeparator)) || filepath.IsAbs(relToRoot) {
		return "", fmt.Errorf("resolved path escapes root: %q", rel)
	}
	return cleanJoined, nil
}

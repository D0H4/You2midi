package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (s *TranscriptionService) persistCacheFile(cacheKey, srcPath string) (string, error) {
	cacheDir := s.cfg.CacheDir()
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir cache dir: %w", err)
	}
	dstPath := filepath.Join(cacheDir, cacheFileName(cacheKey))
	if fileExists(dstPath) {
		return dstPath, nil
	}

	// Hard-link first to avoid extra I/O; fallback to copy across filesystems.
	if err := os.Link(srcPath, dstPath); err != nil {
		if err := copyFile(srcPath, dstPath); err != nil {
			return "", fmt.Errorf("cache file link/copy failed: %w", err)
		}
	}
	return dstPath, nil
}

func cacheFileName(cacheKey string) string {
	sum := sha256.Sum256([]byte(cacheKey))
	return hex.EncodeToString(sum[:]) + ".mid"
}

func copyFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hashFile(path string) (string, error) {
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

func writeStream(path string, r io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

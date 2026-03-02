package runner

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeJoin joins path elements under root and rejects traversal outside root.
func SafeJoin(root string, elems ...string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("root path is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("abs root: %w", err)
	}
	joined := filepath.Join(append([]string{absRoot}, elems...)...)
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("abs joined: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absJoined)
	if err != nil {
		return "", fmt.Errorf("rel path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root: %q", absJoined)
	}
	return absJoined, nil
}

// IsWithinRoot returns true when candidate resolves inside root.
func IsWithinRoot(candidate, root string) bool {
	if strings.TrimSpace(candidate) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absCandidate)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

// SanitizePathSegment validates a single untrusted path segment.
// It rejects absolute paths, traversal, and path separators.
func SanitizePathSegment(seg string) (string, error) {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return "", fmt.Errorf("empty path segment")
	}
	if filepath.IsAbs(seg) {
		return "", fmt.Errorf("absolute segment is not allowed")
	}
	clean := filepath.Clean(seg)
	if clean == "." || clean == ".." {
		return "", fmt.Errorf("invalid segment: %q", seg)
	}
	if strings.Contains(clean, "/") || strings.Contains(clean, "\\") {
		return "", fmt.Errorf("segment must not contain path separators: %q", seg)
	}
	if strings.Contains(clean, ":") {
		return "", fmt.Errorf("segment must not contain drive separator: %q", seg)
	}
	return clean, nil
}

// ValidateAllowedExtension checks whether path has an allowed extension.
func ValidateAllowedExtension(path string, allowed ...string) error {
	if len(allowed) == 0 {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return fmt.Errorf("missing file extension")
	}
	for _, a := range allowed {
		candidate := strings.ToLower(strings.TrimSpace(a))
		if candidate == "" {
			continue
		}
		if !strings.HasPrefix(candidate, ".") {
			candidate = "." + candidate
		}
		if ext == candidate {
			return nil
		}
	}
	return fmt.Errorf("extension %q is not allowed", ext)
}

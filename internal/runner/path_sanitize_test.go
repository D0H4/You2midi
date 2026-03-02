package runner

import (
	"path/filepath"
	"testing"
)

func TestSafeJoin_PreventsTraversal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := SafeJoin(root, "..", "evil.txt"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestSafeJoin_AllowsNestedPathUnderRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p, err := SafeJoin(root, "jobs", "a", "output.mid")
	if err != nil {
		t.Fatalf("SafeJoin: %v", err)
	}
	if !IsWithinRoot(p, root) {
		t.Fatalf("expected path within root: %s", p)
	}
}

func TestSanitizePathSegment(t *testing.T) {
	t.Parallel()
	valid, err := SanitizePathSegment("job-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valid != "job-123" {
		t.Fatalf("unexpected sanitized segment: %s", valid)
	}
	if _, err := SanitizePathSegment("../job"); err == nil {
		t.Fatal("expected traversal segment rejection")
	}
	if _, err := SanitizePathSegment("a/b"); err == nil {
		t.Fatal("expected separator rejection")
	}
}

func TestValidateAllowedExtension(t *testing.T) {
	t.Parallel()
	if err := ValidateAllowedExtension(filepath.Join("x", "out.mid"), ".mid"); err != nil {
		t.Fatalf("expected .mid to be allowed: %v", err)
	}
	if err := ValidateAllowedExtension(filepath.Join("x", "out.exe"), ".mid"); err == nil {
		t.Fatal("expected .exe to be rejected")
	}
}

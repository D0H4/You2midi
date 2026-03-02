package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func mustWriteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestPythonStdlibPresent_WithEncodings(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "Lib", "encodings", "__init__.py"))

	if !pythonStdlibPresent(root) {
		t.Fatalf("expected stdlib to be detected via Lib/encodings")
	}
}

func TestPythonStdlibPresent_WithPythonZip(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "python312.zip"))

	if !pythonStdlibPresent(root) {
		t.Fatalf("expected stdlib to be detected via python*.zip")
	}
}

func TestRuntimeDepsReady_RequiresStdlib(t *testing.T) {
	root := t.TempDir()
	scripts := filepath.Join(root, "Scripts")
	pythonBin := filepath.Join(root, binaryWithExe("python"))
	mustWriteFile(t, pythonBin)
	mustWriteFile(t, filepath.Join(scripts, binaryWithExe("transkun")))
	mustWriteFile(t, filepath.Join(scripts, binaryWithExe("yt-dlp")))

	marker := runtimeDepsMarker{
		Profile:          runtimeDepsProfile,
		RuntimeVersion:   "v-test",
		ResolvedAtUTC:    "2026-03-02T00:00:00Z",
		PythonExecutable: pythonBin,
	}
	raw, err := json.Marshal(marker)
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, runtimeDepsMarkerFile), raw, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if runtimeDepsReady(root, scripts, "v-test", pythonBin) {
		t.Fatalf("expected false when stdlib is missing")
	}

	mustWriteFile(t, filepath.Join(root, "Lib", "encodings", "__init__.py"))
	if !runtimeDepsReady(root, scripts, "v-test", pythonBin) {
		t.Fatalf("expected true after stdlib is present")
	}
}

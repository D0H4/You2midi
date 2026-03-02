package updater

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyPatchZip_ReplacesFile(t *testing.T) {
	appDir := t.TempDir()
	targetRel := "you2midi-backend.exe"
	targetAbs := filepath.Join(appDir, targetRel)

	oldContent := []byte("old backend binary")
	if err := os.WriteFile(targetAbs, oldContent, 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
	}

	newContent := []byte("new backend binary")
	patchPath := filepath.Join(t.TempDir(), "patch.zip")
	manifest := &PatchManifest{
		Version:          "0.1.1",
		LaunchExecutable: "you2midi-desktop.exe",
		Files: []PatchFile{
			{
				Path:      targetRel,
				SHA256:    sha256Hex(newContent),
				SizeBytes: int64(len(newContent)),
			},
		},
	}

	if err := createPatchZip(patchPath, manifest, map[string][]byte{
		targetRel: newContent,
	}); err != nil {
		t.Fatalf("create patch zip: %v", err)
	}

	appliedManifest, err := ApplyPatchZip(appDir, patchPath, DefaultApplyOptions())
	if err != nil {
		t.Fatalf("ApplyPatchZip: %v", err)
	}
	if appliedManifest.Version != "0.1.1" {
		t.Fatalf("unexpected applied version: %q", appliedManifest.Version)
	}

	got, err := os.ReadFile(targetAbs)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(got) != string(newContent) {
		t.Fatalf("patched file mismatch: got=%q want=%q", string(got), string(newContent))
	}
}

func TestApplyPatchZip_HashMismatchFails(t *testing.T) {
	appDir := t.TempDir()
	targetRel := "you2midi-desktop.exe"
	targetAbs := filepath.Join(appDir, targetRel)

	original := []byte("desktop-old")
	if err := os.WriteFile(targetAbs, original, 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
	}

	newContent := []byte("desktop-new")
	patchPath := filepath.Join(t.TempDir(), "patch.zip")
	manifest := &PatchManifest{
		Version: "0.1.2",
		Files: []PatchFile{
			{
				Path:      targetRel,
				SHA256:    "deadbeef",
				SizeBytes: int64(len(newContent)),
			},
		},
	}

	if err := createPatchZip(patchPath, manifest, map[string][]byte{
		targetRel: newContent,
	}); err != nil {
		t.Fatalf("create patch zip: %v", err)
	}

	if _, err := ApplyPatchZip(appDir, patchPath, DefaultApplyOptions()); err == nil {
		t.Fatal("expected ApplyPatchZip to fail on hash mismatch")
	}

	got, err := os.ReadFile(targetAbs)
	if err != nil {
		t.Fatalf("read destination file: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("destination should remain unchanged on validation failure")
	}
}

func TestApplyPatchZip_RejectsTraversalPath(t *testing.T) {
	appDir := t.TempDir()
	patchPath := filepath.Join(t.TempDir(), "patch.zip")

	content := []byte("payload")
	manifest := &PatchManifest{
		Version: "0.1.3",
		Files: []PatchFile{
			{
				Path:      "../escape.exe",
				SHA256:    sha256Hex(content),
				SizeBytes: int64(len(content)),
			},
		},
	}

	if err := createPatchZip(patchPath, manifest, map[string][]byte{
		"../escape.exe": content,
	}); err != nil {
		t.Fatalf("create patch zip: %v", err)
	}

	if _, err := ApplyPatchZip(appDir, patchPath, DefaultApplyOptions()); err == nil {
		t.Fatal("expected ApplyPatchZip to reject traversal path")
	}
}

func createPatchZip(zipPath string, manifest *PatchManifest, files map[string][]byte) error {
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)

	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		_ = zw.Close()
		return err
	}
	mf, err := zw.Create(patchManifestFile)
	if err != nil {
		_ = zw.Close()
		return err
	}
	if _, err := mf.Write(manifestBytes); err != nil {
		_ = zw.Close()
		return err
	}

	for rel, data := range files {
		fw, err := zw.Create(rel)
		if err != nil {
			_ = zw.Close()
			return err
		}
		if _, err := fw.Write(data); err != nil {
			_ = zw.Close()
			return err
		}
	}

	return zw.Close()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

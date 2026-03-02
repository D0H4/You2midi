package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_ConfigSchemaMissing_MigratesToCurrent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[server]
allowed_origins = ["http://localhost:5173"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SchemaVersion != currentConfigSchemaVersion {
		t.Fatalf("unexpected schema version: got=%d want=%d", cfg.SchemaVersion, currentConfigSchemaVersion)
	}
}

func TestLoad_ConfigSchemaFuture_Fails(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
schema_version = 999

[server]
allowed_origins = ["http://localhost:5173"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected Load to fail for future schema version")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

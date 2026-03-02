// Package config loads, validates, and exposes application configuration.
// Load precedence (lowest -> highest): defaults -> config file -> env vars.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const currentConfigSchemaVersion = 1

// Config is the root configuration structure.
type Config struct {
	SchemaVersion int             `toml:"schema_version"`
	Engine        EngineConfig    `toml:"engine"`
	Workspace     WorkspaceConfig `toml:"workspace"`
	Server        ServerConfig    `toml:"server"`

	// ResolvedDevice is set at startup by the CUDA probe; never from config file.
	ResolvedDevice string `toml:"-"`
}

// EngineConfig holds settings specific to transcription runtimes.
type EngineConfig struct {
	PythonBin         string `toml:"python_bin"`
	VenvPath          string `toml:"venv_path"`
	TranskunBin       string `toml:"transkun_bin"`
	NeuralNoteBin     string `toml:"neuralnote_bin"`
	YtDlpBin          string `toml:"ytdlp_bin"`
	Device            string `toml:"device"` // auto | cpu | cuda
	MaxAttempts       int    `toml:"max_attempts"`
	MaxConcurrentJobs int    `toml:"max_concurrent_jobs"` // legacy fallback when CPU/GPU counts are both zero
	MaxConcurrentCPU  int    `toml:"max_concurrent_cpu"`
	MaxConcurrentGPU  int    `toml:"max_concurrent_gpu"`
	QueueSize         int    `toml:"queue_size"`
}

// WorkspaceConfig controls temporary and output file storage.
type WorkspaceConfig struct {
	Root        string        `toml:"root"`
	TempFileTTL time.Duration `toml:"temp_file_ttl"`
	DiskQuotaMB int64         `toml:"disk_quota_mb"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host           string   `toml:"host"`
	Port           int      `toml:"port"`
	JWTSecret      string   `toml:"jwt_secret"`
	AllowedOrigins []string `toml:"allowed_origins"`
}

// defaults returns a Config with sensible defaults.
func defaults() Config {
	home, _ := os.UserHomeDir()
	pythonBin, transkunBin, ytdlpBin := detectVenvBins()

	return Config{
		SchemaVersion: currentConfigSchemaVersion,
		Engine: EngineConfig{
			PythonBin:         pythonBin,
			TranskunBin:       transkunBin,
			NeuralNoteBin:     "neuralnote",
			YtDlpBin:          ytdlpBin,
			Device:            "auto",
			MaxAttempts:       3,
			MaxConcurrentJobs: 2,
			MaxConcurrentCPU:  0,
			MaxConcurrentGPU:  0,
			QueueSize:         128,
		},
		Workspace: WorkspaceConfig{
			Root:        filepath.Join(home, ".you2midi", "jobs"),
			TempFileTTL: 24 * time.Hour,
			DiskQuotaMB: 10240,
		},
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
			AllowedOrigins: []string{
				"http://localhost:5173",
				"http://127.0.0.1:5173",
			},
		},
	}
}

// detectVenvBins returns absolute binary paths from .venv if present,
// otherwise binary names expected on PATH.
func detectVenvBins() (python, transkun, ytdlp string) {
	var scriptsDir string
	var ext string
	if runtime.GOOS == "windows" {
		scriptsDir = filepath.Join(".venv", "Scripts")
		ext = ".exe"
	} else {
		scriptsDir = filepath.Join(".venv", "bin")
		ext = ""
	}

	absScripts, err := filepath.Abs(scriptsDir)
	if err != nil {
		absScripts = scriptsDir
	}

	if info, err := os.Stat(absScripts); err == nil && info.IsDir() {
		return filepath.Join(absScripts, "python"+ext),
			filepath.Join(absScripts, "transkun"+ext),
			filepath.Join(absScripts, "yt-dlp"+ext)
	}

	if runtime.GOOS == "windows" {
		return "python", "transkun", "yt-dlp"
	}
	return "python3", "transkun", "yt-dlp"
}

// Load reads configuration and applies env overrides.
func Load(filePath string) (*Config, error) {
	cfg := defaults()

	if filePath != "" {
		md, err := toml.DecodeFile(filePath, &cfg)
		if err != nil {
			return nil, fmt.Errorf("config: decode %q: %w", filePath, err)
		}
		if !md.IsDefined("schema_version") {
			cfg.SchemaVersion = 0
		}
		if err := migrateConfigSchema(&cfg); err != nil {
			return nil, fmt.Errorf("config: migrate schema: %w", err)
		}
	} else {
		cfg.SchemaVersion = currentConfigSchemaVersion
	}

	applyEnv(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config: validation failed: %w", err)
	}

	return &cfg, nil
}

func migrateConfigSchema(cfg *Config) error {
	switch {
	case cfg.SchemaVersion == 0:
		cfg.SchemaVersion = currentConfigSchemaVersion
		return nil
	case cfg.SchemaVersion == currentConfigSchemaVersion:
		return nil
	case cfg.SchemaVersion > currentConfigSchemaVersion:
		return fmt.Errorf(
			"config schema version %d is newer than supported %d",
			cfg.SchemaVersion,
			currentConfigSchemaVersion,
		)
	default:
		return fmt.Errorf("unsupported legacy config schema version %d", cfg.SchemaVersion)
	}
}

// applyEnv overrides config fields from environment variables.
func applyEnv(cfg *Config) {
	if v := os.Getenv("YOU2MIDI_PYTHON_BIN"); v != "" {
		cfg.Engine.PythonBin = v
	}
	if v := os.Getenv("YOU2MIDI_TRANSKUN_BIN"); v != "" {
		cfg.Engine.TranskunBin = v
	}
	if v := os.Getenv("YOU2MIDI_NEURALNOTE_BIN"); v != "" {
		cfg.Engine.NeuralNoteBin = v
	}
	if v := os.Getenv("YOU2MIDI_YTDLP_BIN"); v != "" {
		cfg.Engine.YtDlpBin = v
	}
	if v := os.Getenv("YOU2MIDI_DEVICE"); v != "" {
		cfg.Engine.Device = v
	}
	if v := os.Getenv("YOU2MIDI_WORKSPACE_ROOT"); v != "" {
		cfg.Workspace.Root = v
	}
	if v := os.Getenv("YOU2MIDI_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("YOU2MIDI_JWT_SECRET"); v != "" {
		cfg.Server.JWTSecret = v
	}
	if v := os.Getenv("YOU2MIDI_ALLOWED_ORIGINS"); v != "" {
		parts := strings.Split(v, ",")
		origins := make([]string, 0, len(parts))
		for _, p := range parts {
			origin := strings.TrimSpace(p)
			if origin != "" {
				origins = append(origins, origin)
			}
		}
		cfg.Server.AllowedOrigins = origins
	}
}

// validate checks that configuration is internally consistent.
func validate(cfg *Config) error {
	switch cfg.Engine.Device {
	case "auto", "cpu", "cuda":
	default:
		return fmt.Errorf("engine.device must be one of: auto, cpu, cuda (got %q)", cfg.Engine.Device)
	}
	if cfg.Engine.MaxAttempts < 1 {
		return fmt.Errorf("engine.max_attempts must be >= 1")
	}
	if cfg.Engine.MaxConcurrentJobs < 0 {
		return fmt.Errorf("engine.max_concurrent_jobs must be >= 0")
	}
	if cfg.Engine.MaxConcurrentCPU < 0 {
		return fmt.Errorf("engine.max_concurrent_cpu must be >= 0")
	}
	if cfg.Engine.MaxConcurrentGPU < 0 {
		return fmt.Errorf("engine.max_concurrent_gpu must be >= 0")
	}
	if cfg.Engine.MaxConcurrentJobs == 0 && cfg.Engine.MaxConcurrentCPU == 0 && cfg.Engine.MaxConcurrentGPU == 0 {
		return fmt.Errorf("at least one of engine.max_concurrent_jobs/max_concurrent_cpu/max_concurrent_gpu must be > 0")
	}
	if cfg.Engine.QueueSize < 1 {
		return fmt.Errorf("engine.queue_size must be >= 1")
	}
	if cfg.Workspace.TempFileTTL <= 0 {
		return fmt.Errorf("workspace.temp_file_ttl must be positive")
	}
	if strings.TrimSpace(cfg.Server.Host) == "" {
		return fmt.Errorf("server.host must not be empty")
	}
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be 1..65535")
	}
	if len(cfg.Server.AllowedOrigins) == 0 {
		return fmt.Errorf("server.allowed_origins must include at least one origin")
	}
	return nil
}

// WorkspaceDir returns the workspace directory for a given job ID.
func (c *Config) WorkspaceDir(jobID string) string {
	return filepath.Join(c.Workspace.Root, jobID)
}

// CacheDir returns the shared cache directory.
func (c *Config) CacheDir() string {
	return filepath.Join(c.Workspace.Root, "cache")
}

// IsPWAMode returns true when remote API auth should be enforced.
func (c *Config) IsPWAMode() bool {
	return strings.TrimSpace(c.Server.JWTSecret) != ""
}

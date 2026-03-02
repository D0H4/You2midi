// You2Midi application entrypoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"you2midi/internal/adapter/downloader/ytdlp"
	neuralnote "you2midi/internal/adapter/engine/neuralnote"
	transkun "you2midi/internal/adapter/engine/transkun"
	sqlitestore "you2midi/internal/adapter/storage/sqlite"
	apihttp "you2midi/internal/api/http"
	"you2midi/internal/config"
	"you2midi/internal/domain"
	"you2midi/internal/runner"
	"you2midi/internal/service"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to config TOML file (optional)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	r := runner.NewDefaultRunner()
	ctx := context.Background()

	pythonBin, usedFallback, err := runner.ResolveBinary(ctx, r, cfg.Engine.PythonBin, pythonFallbackCandidates()...)
	if err != nil {
		slog.Warn("python runtime unavailable; continuing with degraded mode",
			slog.String("configured", cfg.Engine.PythonBin),
			slog.String("error", err.Error()),
		)
		pythonBin = strings.TrimSpace(cfg.Engine.PythonBin)
	} else {
		if usedFallback {
			slog.Warn("runtime fallback activated",
				slog.String("runtime", "python"),
				slog.String("configured", cfg.Engine.PythonBin),
				slog.String("resolved", pythonBin),
			)
		}
	}
	cfg.Engine.PythonBin = pythonBin

	probeRun := func(ctx context.Context, bin string, args []string) (string, error) {
		res, err := r.Run(ctx, bin, args, runner.RunOptions{Timeout: 15 * time.Second})
		if err != nil {
			return "", err
		}
		return string(res.Stdout), nil
	}
	resolvedDevice, err := config.ProbeDevice(context.Background(), cfg, probeRun)
	if err != nil {
		return fmt.Errorf("device probe: %w", err)
	}
	slog.Info("device resolved", slog.String("device", resolvedDevice))

	transkunAvailable := true
	if resolved, used, resolveErr := runner.ResolveBinary(ctx, r, cfg.Engine.TranskunBin, "transkun"); resolveErr != nil {
		transkunAvailable = false
		slog.Warn("transkun runtime unavailable",
			slog.String("configured", cfg.Engine.TranskunBin),
			slog.String("error", resolveErr.Error()),
		)
	} else {
		if used {
			slog.Warn("runtime fallback activated",
				slog.String("runtime", "transkun"),
				slog.String("configured", cfg.Engine.TranskunBin),
				slog.String("resolved", resolved),
			)
		}
		cfg.Engine.TranskunBin = resolved
	}

	neuralnoteAvailable := true
	if resolved, used, resolveErr := runner.ResolveBinary(ctx, r, cfg.Engine.NeuralNoteBin, "neuralnote"); resolveErr != nil {
		neuralnoteAvailable = false
		slog.Warn("neuralnote runtime unavailable",
			slog.String("configured", cfg.Engine.NeuralNoteBin),
			slog.String("error", resolveErr.Error()),
		)
	} else {
		if used {
			slog.Warn("runtime fallback activated",
				slog.String("runtime", "neuralnote"),
				slog.String("configured", cfg.Engine.NeuralNoteBin),
				slog.String("resolved", resolved),
			)
		}
		cfg.Engine.NeuralNoteBin = resolved
	}

	if resolved, used, resolveErr := runner.ResolveBinary(ctx, r, cfg.Engine.YtDlpBin, "yt-dlp"); resolveErr != nil {
		slog.Warn("yt-dlp runtime unavailable",
			slog.String("configured", cfg.Engine.YtDlpBin),
			slog.String("error", resolveErr.Error()),
		)
	} else {
		if used {
			slog.Warn("runtime fallback activated",
				slog.String("runtime", "yt-dlp"),
				slog.String("configured", cfg.Engine.YtDlpBin),
				slog.String("resolved", resolved),
			)
		}
		cfg.Engine.YtDlpBin = resolved
	}

	var primaryEngine domain.TranscriptionEngine
	var fallbackEngine domain.TranscriptionEngine
	switch {
	case transkunAvailable:
		primaryEngine = transkun.New(cfg.Engine.TranskunBin, cfg.ResolvedDevice, r)
		if neuralnoteAvailable {
			fallbackEngine = neuralnote.New(cfg.Engine.NeuralNoteBin, r)
		}
	case neuralnoteAvailable:
		slog.Warn("promoting neuralnote to primary engine due to transkun unavailability")
		primaryEngine = neuralnote.New(cfg.Engine.NeuralNoteBin, r)
	default:
		// Keep service startup alive for diagnostics/repair flows.
		slog.Warn("no transcription runtime available at startup; jobs may fail until runtime is repaired")
		primaryEngine = transkun.New(cfg.Engine.TranskunBin, cfg.ResolvedDevice, r)
	}

	if err := os.MkdirAll(cfg.Workspace.Root, 0o750); err != nil {
		return fmt.Errorf("workspace: mkdir %q: %w", cfg.Workspace.Root, err)
	}
	dbPath := filepath.Join(cfg.Workspace.Root, "you2midi.db")
	db, err := sqlitestore.Open(dbPath)
	if err != nil {
		return fmt.Errorf("sqlite: open: %w", err)
	}
	defer db.Close()

	jobRepo := sqlitestore.NewJobRepo(db)
	cacheRepo := sqlitestore.NewCacheRepo(db)
	dl := ytdlp.New(cfg.Engine.YtDlpBin, r)

	svc := service.New(cfg, jobRepo, cacheRepo, primaryEngine, fallbackEngine, dl, r)
	svc.StartupOrphanSweep(ctx)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	if err := svc.Start(workerCtx); err != nil {
		return fmt.Errorf("service start: %w", err)
	}

	mux := http.NewServeMux()
	apihttp.NewHandler(cfg, svc).RegisterRoutes(mux)

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("HTTP server listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", slog.String("error", err.Error()))
		}
	}()

	<-quit
	slog.Info("shutting down")
	workerCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func pythonFallbackCandidates() []string {
	if runtime.GOOS == "windows" {
		return []string{"python"}
	}
	return []string{"python3", "python"}
}

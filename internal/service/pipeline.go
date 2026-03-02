package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"you2midi/internal/domain"
	"you2midi/internal/metrics"
	"you2midi/internal/runner"
)

// runPipeline executes download -> transcribe -> persist for one attempt.
func (s *TranscriptionService) runPipeline(ctx context.Context, job *domain.Job) error {
	totalStart := time.Now()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	defer func(startHeapAlloc uint64) {
		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)
		slog.Info("job memory snapshot",
			slog.String("job_id", job.ID),
			slog.Int64("heap_alloc_bytes", int64(memAfter.HeapAlloc)),
			slog.Int64("heap_inuse_bytes", int64(memAfter.HeapInuse)),
			slog.Int64("heap_sys_bytes", int64(memAfter.HeapSys)),
			slog.Int64("heap_alloc_delta_bytes", int64(memAfter.HeapAlloc)-int64(startHeapAlloc)),
		)
	}(memBefore.HeapAlloc)

	jobDirName, err := runner.SanitizePathSegment(job.ID)
	if err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	wsDir, err := runner.SafeJoin(s.cfg.Workspace.Root, jobDirName)
	if err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	inputDir, err := runner.SafeJoin(wsDir, "input")
	if err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	outputDir, err := runner.SafeJoin(wsDir, "output")
	if err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	if err := os.MkdirAll(inputDir, 0o750); err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	job.WorkspacePath = wsDir
	if err := s.jobs.Update(ctx, job); err != nil {
		return fmt.Errorf("service: persist workspace path: %w", err)
	}

	audioPath := job.AudioPath
	audioHash := job.AudioHash
	if !fileExists(audioPath) {
		audioPath = ""
		audioHash = ""
	}

	if audioPath == "" {
		if job.YoutubeURL == "" {
			return domain.NewEngineError(domain.ErrCorruptFile, fmt.Errorf("missing audio input path"))
		}
		dlStart := time.Now()
		var dlErr error
		hashingDownloader, ok := s.downloader.(HashingDownloader)
		if ok {
			audioPath, audioHash, dlErr = hashingDownloader.DownloadAndHash(ctx, job.YoutubeURL, inputDir)
		} else {
			audioPath, dlErr = s.downloader.Download(ctx, job.YoutubeURL, inputDir)
		}
		if dlErr != nil {
			e := domain.NewEngineError(domain.ErrUnknown, dlErr)
			e.Message = dlErr.Error()
			return e
		}
		job.DownloadMs = time.Since(dlStart).Milliseconds()
		metrics.Record("download", time.Since(dlStart), job.ID)
	}
	if job.StartSec > 0 {
		trimmedPath, trimErr := s.trimAudioFromStart(ctx, inputDir, audioPath, job.StartSec)
		if trimErr != nil {
			e := domain.NewEngineError(domain.ErrUnknown, trimErr)
			e.Message = trimErr.Error()
			return e
		}
		audioPath = trimmedPath
		audioHash = ""
	}
	if err := runner.ValidateAllowedExtension(audioPath, ".wav", ".mp3", ".flac", ".ogg", ".aiff", ".m4a", ".opus", ".webm", ".aac"); err != nil {
		return domain.NewEngineError(domain.ErrCorruptFile, err)
	}
	if audioHash == "" {
		hashed, err := hashFile(audioPath)
		if err != nil {
			return domain.NewEngineError(domain.ErrCorruptFile, err)
		}
		audioHash = hashed
	}
	job.AudioPath = audioPath
	job.AudioHash = audioHash
	if err := s.jobs.Update(ctx, job); err != nil {
		return fmt.Errorf("service: persist input metadata: %w", err)
	}

	engineVer, _ := s.primary.Version(ctx)
	cacheKey := buildCacheKey(audioHash, s.primary.Name(), engineVer, job.Device)
	if midiPath, hit, err := s.cache.Lookup(ctx, cacheKey); err == nil && hit {
		metrics.AddCounter("cache.hit", 1)
		if fileExists(midiPath) {
			if s.isJobCancelled(ctx, job.ID) {
				return domain.NewEngineError(domain.ErrCancelled, context.Canceled)
			}
			job.OutputPath = midiPath
			job.State = domain.JobStateCompleted
			now := time.Now()
			job.CompletedAt = &now
			job.TotalMs = time.Since(totalStart).Milliseconds()
			return s.jobs.Update(ctx, job)
		}
		if err := s.cache.Invalidate(ctx, cacheKey); err != nil {
			return fmt.Errorf("service: invalidate stale cache: %w", err)
		}
		metrics.AddCounter("cache.stale", 1)
	} else if err == nil {
		metrics.AddCounter("cache.miss", 1)
	} else {
		metrics.AddCounter("cache.error", 1)
	}

	f, err := os.Open(audioPath)
	if err != nil {
		return domain.NewEngineError(domain.ErrCorruptFile, err)
	}
	defer f.Close()

	opts := domain.TranscribeOptions{Device: job.Device}
	infStart := time.Now()
	engine := s.primary
	preferredEngine := s.primary.Name()
	midiRC, transcribeErr := engine.Transcribe(ctx, f, opts)
	if transcribeErr != nil {
		engineErr, _ := transcribeErr.(*domain.EngineError)
		if engineErr != nil && engineErr.Code == domain.ErrEngineNotFound && s.fallback != nil {
			engine = s.fallback
			if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
				return domain.NewEngineError(domain.ErrCorruptFile, seekErr)
			}
			midiRC, transcribeErr = engine.Transcribe(ctx, f, opts)
		}
	}
	if transcribeErr != nil {
		return transcribeErr
	}
	defer midiRC.Close()

	job.InferenceMs = time.Since(infStart).Milliseconds()
	metrics.Record("inference", time.Since(infStart), job.ID)

	outFileName, err := runner.SanitizePathSegment("output.mid")
	if err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	outPath, err := runner.SafeJoin(outputDir, outFileName)
	if err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	if err := runner.ValidateAllowedExtension(outPath, ".mid"); err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}
	if err := writeStream(outPath, midiRC); err != nil {
		return domain.NewEngineError(domain.ErrUnknown, err)
	}

	cachePath, cacheErr := s.persistCacheFile(cacheKey, outPath)
	if cacheErr != nil {
		slog.Warn("cache copy failed",
			slog.String("job_id", job.ID),
			slog.String("cache_key", cacheKey),
			slog.String("error", cacheErr.Error()),
		)
	} else if err := s.cache.Store(ctx, cacheKey, cachePath); err != nil {
		return fmt.Errorf("service: store cache entry: %w", err)
	}

	if s.isJobCancelled(ctx, job.ID) {
		return domain.NewEngineError(domain.ErrCancelled, context.Canceled)
	}
	now := time.Now()
	job.OutputPath = outPath
	job.State = domain.JobStateCompleted
	job.CompletedAt = &now
	job.TotalMs = time.Since(totalStart).Milliseconds()
	job.Engine = engine.Name()
	slog.Info("job execution resolved",
		slog.String("job_id", job.ID),
		slog.String("preferred_engine", preferredEngine),
		slog.String("actual_engine", job.Engine),
		slog.String("actual_device", job.Device),
	)

	return s.jobs.Update(ctx, job)
}

func buildCacheKey(audioHash, engine, engineVersion, device string) string {
	paramStr := strings.Join([]string{engine, engineVersion, device}, ":")
	paramHash := sha256.Sum256([]byte(paramStr))
	return audioHash + ":" + hex.EncodeToString(paramHash[:])
}

func (s *TranscriptionService) trimAudioFromStart(ctx context.Context, inputDir, inputPath string, startSec int) (string, error) {
	outputPath, err := runner.SafeJoin(inputDir, "audio.trimmed.wav")
	if err != nil {
		return "", fmt.Errorf("trim: output path: %w", err)
	}
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-ss", strconv.Itoa(startSec),
		"-i", inputPath,
		"-vn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		outputPath,
	}
	res, runErr := s.runner.Run(ctx, "ffmpeg", args, runner.RunOptions{
		Dir:     inputDir,
		Timeout: 2 * time.Minute,
	})
	if runErr != nil {
		return "", fmt.Errorf("trim: ffmpeg failed to start: %w", runErr)
	}
	if res.ExitCode != 0 {
		stderr := strings.TrimSpace(string(res.Stderr))
		if stderr == "" {
			stderr = "unknown ffmpeg error"
		}
		return "", fmt.Errorf("trim: ffmpeg exited %d: %s", res.ExitCode, stderr)
	}
	if _, err := os.Stat(outputPath); err != nil {
		return "", fmt.Errorf("trim: output missing at %s: %w", filepath.Base(outputPath), err)
	}
	slog.Info("audio trimmed with ffmpeg",
		slog.Int("start_sec", startSec),
		slog.String("input_path", inputPath),
		slog.String("output_path", outputPath),
	)
	return outputPath, nil
}

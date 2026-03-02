// Package http provides REST API handlers for You2Midi.
// Handlers are thin adapters over the service layer.
package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"you2midi/internal/config"
	"you2midi/internal/domain"
	"you2midi/internal/runner"
	"you2midi/internal/service"
)

// Handler aggregates all HTTP route handlers.
type Handler struct {
	cfg *config.Config
	svc *service.TranscriptionService
}

func NewHandler(cfg *config.Config, svc *service.TranscriptionService) *Handler {
	return &Handler{cfg: cfg, svc: svc}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	protected := func(next http.HandlerFunc) http.Handler {
		return h.cors(h.jwtAuth(next))
	}

	mux.Handle("GET /health", h.cors(http.HandlerFunc(h.handleHealth)))
	mux.Handle("GET /jobs", protected(h.handleListJobs))
	mux.Handle("POST /jobs", protected(h.handleCreateJob))
	mux.Handle("GET /jobs/{id}", protected(h.handleGetJob))
	mux.Handle("POST /jobs/{id}/cancel", protected(h.handleCancelJob))
	mux.Handle("GET /jobs/{id}/progress", protected(h.handleGetJobProgress))
	mux.Handle("GET /jobs/{id}/midi", protected(h.handleDownloadMidi))
	mux.Handle("OPTIONS /{path...}", h.cors(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
}

func (h *Handler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			allowedOrigin, ok := h.allowedOrigin(origin)
			if !ok {
				writeJSON(w, http.StatusForbidden, ErrorResponse{ErrorCode: "CORS_ORIGIN_DENIED", Message: "Origin is not allowed"})
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) allowedOrigin(origin string) (string, bool) {
	for _, allowed := range h.cfg.Server.AllowedOrigins {
		if allowed == "*" {
			return "*", true
		}
		if origin == allowed {
			return origin, true
		}
	}
	return "", false
}

func (h *Handler) jwtAuth(next http.Handler) http.Handler {
	if !h.cfg.IsPWAMode() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{ErrorCode: "UNAUTHORIZED", Message: "Missing bearer token"})
			return
		}
		if err := validateHS256JWT(token, h.cfg.Server.JWTSecret, time.Now().UTC()); err != nil {
			writeJSON(w, http.StatusUnauthorized, ErrorResponse{ErrorCode: "UNAUTHORIZED", Message: "Invalid or expired token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if len(header) < 8 {
		return "", false
	}
	if !strings.EqualFold(header[:7], "Bearer ") {
		return "", false
	}
	tok := strings.TrimSpace(header[7:])
	if tok == "" {
		return "", false
	}
	return tok, true
}

func validateHS256JWT(token, secret string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errJWT("invalid token segments")
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errJWT("invalid header")
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return errJWT("invalid header json")
	}
	if header.Alg != "HS256" {
		return errJWT("unsupported algorithm")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errJWT("invalid payload")
	}
	var payload struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return errJWT("invalid payload json")
	}
	if payload.Exp == 0 {
		return errJWT("missing exp")
	}
	if now.Unix() >= payload.Exp {
		return errJWT("token expired")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errJWT("invalid signature")
	}
	if !hmac.Equal(signature, expected) {
		return errJWT("signature mismatch")
	}
	return nil
}

type errJWT string

func (e errJWT) Error() string { return string(e) }

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ok", Checks: map[string]string{"api": "ok"}})
}

func (h *Handler) handleListJobs(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, domain.ErrUnknown, "limit must be a positive integer")
			return
		}
		if parsed > 200 {
			parsed = 200
		}
		limit = parsed
	}

	var before *time.Time
	if rawBefore := strings.TrimSpace(r.URL.Query().Get("before")); rawBefore != "" {
		parsed, err := time.Parse(time.RFC3339Nano, rawBefore)
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, rawBefore)
			if err != nil {
				writeError(w, http.StatusBadRequest, domain.ErrUnknown, "before must be RFC3339 timestamp")
				return
			}
		}
		parsed = parsed.UTC()
		before = &parsed
	}

	jobs, err := h.svc.ListJobsPage(r.Context(), limit, before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (h *Handler) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, domain.ErrUnknown, "invalid JSON request body")
		return
	}
	if strings.TrimSpace(req.YoutubeURL) == "" {
		writeError(w, http.StatusBadRequest, domain.ErrUnknown, "youtube_url is required")
		return
	}
	if req.StartSec < 0 {
		writeError(w, http.StatusBadRequest, domain.ErrUnknown, "start_sec must be >= 0")
		return
	}

	job, err := h.svc.SubmitYouTube(r.Context(), req.YoutubeURL, domain.TranscribeOptions{
		Device:   req.Device,
		StartSec: req.StartSec,
	})
	if err != nil {
		if errors.Is(err, service.ErrQueueFull) {
			writeError(w, http.StatusServiceUnavailable, domain.ErrUnknown, "queue is full, retry shortly")
			return
		}
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, job)
}

func (h *Handler) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.svc.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, service.ErrJobMissing) {
			writeError(w, http.StatusNotFound, domain.ErrUnknown, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.CancelJob(r.Context(), id); err != nil {
		if errors.Is(err, service.ErrJobMissing) {
			writeError(w, http.StatusNotFound, domain.ErrUnknown, "job not found")
			return
		}
		writeError(w, http.StatusConflict, domain.ErrCancelled, err.Error())
		return
	}
	job, err := h.svc.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) handleGetJobProgress(w http.ResponseWriter, r *http.Request) {
	job, err := h.svc.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, service.ErrJobMissing) {
			writeError(w, http.StatusNotFound, domain.ErrUnknown, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toJobProgress(job))
}

func (h *Handler) handleDownloadMidi(w http.ResponseWriter, r *http.Request) {
	job, err := h.svc.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, service.ErrJobMissing) {
			writeError(w, http.StatusNotFound, domain.ErrUnknown, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, err.Error())
		return
	}
	if job.State != domain.JobStateCompleted || job.OutputPath == "" {
		writeError(w, http.StatusConflict, domain.ErrUnknown, "MIDI not yet available")
		return
	}
	if err := runner.ValidateAllowedExtension(job.OutputPath, ".mid"); err != nil {
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, "invalid MIDI output path")
		return
	}
	if !runner.IsWithinRoot(job.OutputPath, h.cfg.Workspace.Root) {
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, "MIDI output path escaped workspace root")
		return
	}

	f, err := os.Open(job.OutputPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, domain.ErrUnknown, "failed to open MIDI file")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "audio/midi")
	w.Header().Set("Content-Disposition", `attachment; filename="output.mid"`)
	_, _ = io.Copy(w, f)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code domain.ErrorCode, message string) {
	writeJSON(w, status, ErrorResponse{ErrorCode: string(code), Message: message})
}

func toJobProgress(job *domain.Job) JobProgress {
	stage, pct := mapStageAndProgress(job)
	return JobProgress{
		JobID:        job.ID,
		State:        JobState(job.State),
		Stage:        stage,
		ProgressPct:  pct,
		Attempt:      job.Attempt,
		MaxAttempts:  job.MaxAttempts,
		Cancelable:   !job.IsTerminal(),
		ErrorCode:    job.ErrorCode,
		ErrorMessage: job.ErrorMessage,
		DownloadMs:   job.DownloadMs,
		InferenceMs:  job.InferenceMs,
		TotalMs:      job.TotalMs,
		UpdatedAt:    job.UpdatedAt,
	}
}

func mapStageAndProgress(job *domain.Job) (JobStage, int) {
	switch job.State {
	case domain.JobStateQueued:
		return JobStageQueued, 0
	case domain.JobStateRetrying:
		return JobStageRetrying, 5
	case domain.JobStateRunning:
		if job.InferenceMs > 0 {
			return JobStageFinalizing, 90
		}
		if job.DownloadMs > 0 {
			return JobStageTranscribing, 55
		}
		return JobStageDownloading, 20
	case domain.JobStateCompleted:
		return JobStageCompleted, 100
	case domain.JobStateFailed:
		return JobStageFailed, 100
	case domain.JobStateCancelled:
		return JobStageCancelled, 100
	default:
		return JobStageQueued, 0
	}
}

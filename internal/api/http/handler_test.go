package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlitestore "you2midi/internal/adapter/storage/sqlite"
	"you2midi/internal/config"
	"you2midi/internal/domain"
	"you2midi/internal/runner"
	"you2midi/internal/service"
)

type dummyEngine struct{}

func (e *dummyEngine) Name() string                              { return "dummy" }
func (e *dummyEngine) Version(_ context.Context) (string, error) { return "1.0", nil }
func (e *dummyEngine) HealthCheck(_ context.Context) error       { return nil }
func (e *dummyEngine) Transcribe(_ context.Context, _ io.Reader, _ domain.TranscribeOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("MThd")), nil
}

type noopDownloader struct{}

func (d *noopDownloader) Download(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}

func newHandlerForTest(t *testing.T, pwa bool, queueSize int) (*Handler, *config.Config, *sqlitestore.DB) {
	t.Helper()

	cfg := &config.Config{}
	cfg.Engine.MaxAttempts = 2
	cfg.Engine.MaxConcurrentJobs = 1
	cfg.Engine.QueueSize = queueSize
	cfg.ResolvedDevice = "cpu"
	cfg.Workspace.Root = t.TempDir()
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 8080
	cfg.Server.AllowedOrigins = []string{"http://localhost:5173"}
	if pwa {
		cfg.Server.JWTSecret = "test-secret"
	}

	dbPath := filepath.Join(cfg.Workspace.Root, "test.db")
	db, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	svc := service.New(
		cfg,
		sqlitestore.NewJobRepo(db),
		sqlitestore.NewCacheRepo(db),
		&dummyEngine{},
		nil,
		&noopDownloader{},
		runner.NewFakeRunner(),
	)

	return NewHandler(cfg, svc), cfg, db
}

func makeJWT(secret string, exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	data := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(data))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return data + "." + sig
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var er ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&er); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return er
}

func TestCORS_DeniesUnknownOrigin(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 8)
	defer db.Close()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	er := decodeError(t, rr)
	if er.ErrorCode != "CORS_ORIGIN_DENIED" {
		t.Fatalf("expected CORS_ORIGIN_DENIED, got %s", er.ErrorCode)
	}
}

func TestJWT_MissingBearerUnauthorized(t *testing.T) {
	h, _, db := newHandlerForTest(t, true, 8)
	defer db.Close()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/jobs", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestCreateJob_WithValidJWT_Created(t *testing.T) {
	h, cfg, db := newHandlerForTest(t, true, 8)
	defer db.Close()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"youtube_url":"https://youtube.com/watch?v=test","device":"cpu","start_sec":12}`
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+makeJWT(cfg.Server.JWTSecret, time.Now().Add(5*time.Minute)))
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", rr.Code, rr.Body.String())
	}
	var job domain.Job
	if err := json.NewDecoder(rr.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.ID == "" {
		t.Fatal("expected non-empty job id")
	}
	if job.StartSec != 12 {
		t.Fatalf("expected start_sec=12, got %d", job.StartSec)
	}
}

func TestCreateJob_NegativeStartSecReturns400(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 8)
	defer db.Close()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"youtube_url":"https://youtube.com/watch?v=test","device":"cpu","start_sec":-1}`
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestCreateJob_QueueFullReturns503(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 1)
	defer db.Close()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"youtube_url":"https://youtube.com/watch?v=test","device":"cpu"}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if i == 0 && rr.Code != http.StatusCreated {
			t.Fatalf("first request expected 201, got %d", rr.Code)
		}
		if i == 1 && rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("second request expected 503, got %d", rr.Code)
		}
	}
}

func TestGetAndCancel_NotFound(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 8)
	defer db.Close()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	getReq := httptest.NewRequest(http.MethodGet, "/jobs/not-exists", nil)
	getRR := httptest.NewRecorder()
	mux.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusNotFound {
		t.Fatalf("GET expected 404, got %d", getRR.Code)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/jobs/not-exists/cancel", nil)
	cancelRR := httptest.NewRecorder()
	mux.ServeHTTP(cancelRR, cancelReq)
	if cancelRR.Code != http.StatusNotFound {
		t.Fatalf("cancel expected 404, got %d", cancelRR.Code)
	}
}

func TestDownloadMidi_ConflictWhenNotCompleted(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 8)
	defer db.Close()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	createReq := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{"youtube_url":"https://youtube.com/watch?v=test"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	mux.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create expected 201, got %d", createRR.Code)
	}
	var job domain.Job
	if err := json.NewDecoder(createRR.Body).Decode(&job); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	dlReq := httptest.NewRequest(http.MethodGet, "/jobs/"+job.ID+"/midi", nil)
	dlRR := httptest.NewRecorder()
	mux.ServeHTTP(dlRR, dlReq)

	if dlRR.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", dlRR.Code)
	}
}

func TestListJobs_PaginationLimit(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 8)
	defer db.Close()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"youtube_url":"https://youtube.com/watch?v=test"}`
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d expected 201, got %d", i, rr.Code)
		}
		time.Sleep(2 * time.Millisecond)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/jobs?limit=1", nil)
	listRR := httptest.NewRecorder()
	mux.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d", listRR.Code)
	}
	var jobs []domain.Job
	if err := json.NewDecoder(listRR.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job with limit=1, got %d", len(jobs))
	}
}

func TestListJobs_InvalidBeforeReturns400(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 8)
	defer db.Close()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/jobs?before=not-a-time", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestGetJobProgress_QueuedSnapshot(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 8)
	defer db.Close()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	createReq := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{"youtube_url":"https://youtube.com/watch?v=test"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	mux.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create expected 201, got %d", createRR.Code)
	}
	var job domain.Job
	if err := json.NewDecoder(createRR.Body).Decode(&job); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	progressReq := httptest.NewRequest(http.MethodGet, "/jobs/"+job.ID+"/progress", nil)
	progressRR := httptest.NewRecorder()
	mux.ServeHTTP(progressRR, progressReq)
	if progressRR.Code != http.StatusOK {
		t.Fatalf("progress expected 200, got %d", progressRR.Code)
	}

	var progress JobProgress
	if err := json.NewDecoder(progressRR.Body).Decode(&progress); err != nil {
		t.Fatalf("decode progress response: %v", err)
	}
	if progress.JobID != job.ID {
		t.Fatalf("unexpected job_id: got %s want %s", progress.JobID, job.ID)
	}
	if progress.Stage != JobStageQueued {
		t.Fatalf("unexpected stage: got %s want %s", progress.Stage, JobStageQueued)
	}
	if progress.ProgressPct != 0 {
		t.Fatalf("unexpected progress_pct: got %d want 0", progress.ProgressPct)
	}
	if !progress.Cancelable {
		t.Fatal("expected queued job to be cancelable")
	}
}

func TestGetJobProgress_NotFound(t *testing.T) {
	h, _, db := newHandlerForTest(t, false, 8)
	defer db.Close()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/jobs/not-exists/progress", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

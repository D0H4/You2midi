// Package sqlite implements domain.JobRepository and domain.CacheRepository
// using a local SQLite database via modernc.org/sqlite (CGO-free).
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver

	"you2midi/internal/domain"
)

// DB wraps a sql.DB and provides job and cache repository implementations.
type DB struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at the given file path and
// runs all schema migrations. It also applies WAL mode for write concurrency.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite allows only one write connection
	s := &DB{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database connection.
func (s *DB) Close() error { return s.db.Close() }

// ---- schema -----------------------------------------------------------------

const currentSchemaVersion = 2

var jobColumnDDL = map[string]string{
	"youtube_url":    "TEXT",
	"audio_path":     "TEXT",
	"workspace_path": "TEXT NOT NULL DEFAULT ''",
	"audio_hash":     "TEXT NOT NULL DEFAULT ''",
	"engine":         "TEXT NOT NULL DEFAULT ''",
	"device":         "TEXT NOT NULL DEFAULT 'cpu'",
	"start_sec":      "INTEGER NOT NULL DEFAULT 0",
	"output_path":    "TEXT",
	"error_code":     "TEXT",
	"error_message":  "TEXT",
	"attempt":        "INTEGER NOT NULL DEFAULT 0",
	"max_attempts":   "INTEGER NOT NULL DEFAULT 3",
	"options_json":   "TEXT NOT NULL DEFAULT '{}'",
	"created_at":     "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP",
	"updated_at":     "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP",
	"completed_at":   "DATETIME",
	"download_ms":    "INTEGER NOT NULL DEFAULT 0",
	"inference_ms":   "INTEGER NOT NULL DEFAULT 0",
	"total_ms":       "INTEGER NOT NULL DEFAULT 0",
}

var cacheColumnDDL = map[string]string{
	"midi_path":  "TEXT NOT NULL DEFAULT ''",
	"created_at": "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP",
	"hit_count":  "INTEGER NOT NULL DEFAULT 0",
}

func (s *DB) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: begin migration tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureSchemaMeta(tx); err != nil {
		return fmt.Errorf("sqlite: migrate: ensure schema meta: %w", err)
	}

	version, err := schemaVersion(tx)
	if err != nil {
		return fmt.Errorf("sqlite: migrate: read schema version: %w", err)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("sqlite: migrate: database schema version %d is newer than supported %d", version, currentSchemaVersion)
	}

	for version < currentSchemaVersion {
		next := version + 1
		if err := applyMigration(tx, next); err != nil {
			return fmt.Errorf("sqlite: migrate to v%d: %w", next, err)
		}
		if err := setSchemaVersion(tx, next); err != nil {
			return fmt.Errorf("sqlite: set schema version v%d: %w", next, err)
		}
		version = next
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit migration tx: %w", err)
	}
	return nil
}

func ensureSchemaMeta(tx *sql.Tx) error {
	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS schema_meta (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    version     INTEGER NOT NULL,
    updated_at  DATETIME NOT NULL
);
INSERT INTO schema_meta (id, version, updated_at)
SELECT 1, 0, CURRENT_TIMESTAMP
WHERE NOT EXISTS (SELECT 1 FROM schema_meta WHERE id = 1);
`); err != nil {
		return err
	}
	return nil
}

func schemaVersion(tx *sql.Tx) (int, error) {
	var v int
	if err := tx.QueryRow(`SELECT version FROM schema_meta WHERE id = 1`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

func setSchemaVersion(tx *sql.Tx, version int) error {
	_, err := tx.Exec(
		`UPDATE schema_meta SET version = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`,
		version,
	)
	return err
}

func applyMigration(tx *sql.Tx, version int) error {
	switch version {
	case 1:
		return migrateToV1(tx)
	case 2:
		return migrateToV2(tx)
	default:
		return fmt.Errorf("unknown migration version %d", version)
	}
}

func migrateToV1(tx *sql.Tx) error {
	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS jobs (
    id              TEXT PRIMARY KEY,
    state           TEXT NOT NULL DEFAULT 'queued',
    youtube_url     TEXT,
    audio_path      TEXT,
    workspace_path  TEXT NOT NULL DEFAULT '',
    audio_hash      TEXT NOT NULL DEFAULT '',
    engine          TEXT NOT NULL DEFAULT '',
    device          TEXT NOT NULL DEFAULT 'cpu',
    start_sec       INTEGER NOT NULL DEFAULT 0,
    output_path     TEXT,
    error_code      TEXT,
    error_message   TEXT,
    attempt         INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 3,
    options_json    TEXT NOT NULL DEFAULT '{}',
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,
    completed_at    DATETIME,
    download_ms     INTEGER NOT NULL DEFAULT 0,
    inference_ms    INTEGER NOT NULL DEFAULT 0,
    total_ms        INTEGER NOT NULL DEFAULT 0
);
`); err != nil {
		return err
	}

	if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS cache (
    cache_key   TEXT PRIMARY KEY,
    midi_path   TEXT NOT NULL,
    created_at  DATETIME NOT NULL,
    hit_count   INTEGER NOT NULL DEFAULT 0
);
`); err != nil {
		return err
	}
	return ensureCurrentSchema(tx)
}

// migrateToV2 repairs legacy v1 databases that are missing columns despite schema_version=1.
func migrateToV2(tx *sql.Tx) error {
	return ensureCurrentSchema(tx)
}

func ensureCurrentSchema(tx *sql.Tx) error {
	for col, ddl := range jobColumnDDL {
		if err := ensureColumn(tx, "jobs", col, ddl); err != nil {
			return fmt.Errorf("ensure jobs.%s: %w", col, err)
		}
	}
	for col, ddl := range cacheColumnDDL {
		if err := ensureColumn(tx, "cache", col, ddl); err != nil {
			return fmt.Errorf("ensure cache.%s: %w", col, err)
		}
	}
	if _, err := tx.Exec(`
CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_jobs_state_created_at ON jobs(state, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cache_created_at ON cache(created_at DESC);
`); err != nil {
		return err
	}
	return nil
}

func ensureColumn(tx *sql.Tx, table, column, ddl string) error {
	exists, err := columnExists(tx, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = tx.Exec(fmt.Sprintf(`ALTER TABLE "%s" ADD COLUMN "%s" %s`, table, column, ddl))
	return err
}

func columnExists(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info("%s")`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			colType    string
			notNull    int
			defaultVal interface{}
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ---- JobRepository ----------------------------------------------------------

// JobRepo implements domain.JobRepository on top of *DB.
type JobRepo struct{ db *DB }

// NewJobRepo returns a JobRepository backed by the given DB.
func NewJobRepo(db *DB) domain.JobRepository { return &JobRepo{db: db} }

func (r *JobRepo) Create(ctx context.Context, job *domain.Job) (*domain.Job, error) {
	if job.ID == "" {
		job.ID = newID()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now

	opts, _ := json.Marshal(struct{}{}) // placeholder for TranscribeOptions
	_, err := r.db.db.ExecContext(ctx, `
INSERT INTO jobs
  (id, state, youtube_url, audio_path, workspace_path, audio_hash, engine, device,
   start_sec, output_path, error_code, error_message, attempt, max_attempts, options_json,
   created_at, updated_at, completed_at, download_ms, inference_ms, total_ms)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		job.ID, job.State, nullStr(job.YoutubeURL), nullStr(job.AudioPath),
		job.WorkspacePath, job.AudioHash, job.Engine, job.Device,
		job.StartSec,
		nullStr(job.OutputPath), nullStr(job.ErrorCode), nullStr(job.ErrorMessage),
		job.Attempt, job.MaxAttempts, string(opts),
		job.CreatedAt, job.UpdatedAt, nullTime(job.CompletedAt),
		job.DownloadMs, job.InferenceMs, job.TotalMs,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: create job: %w", err)
	}
	return job, nil
}

func (r *JobRepo) Get(ctx context.Context, id string) (*domain.Job, error) {
	row := r.db.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id)
	return scanJob(row)
}

func (r *JobRepo) Update(ctx context.Context, job *domain.Job) error {
	job.UpdatedAt = time.Now().UTC()
	_, err := r.db.db.ExecContext(ctx, `
UPDATE jobs SET
  state=?, output_path=?, error_code=?, error_message=?, attempt=?,
  updated_at=?, completed_at=?, download_ms=?, inference_ms=?, total_ms=?,
  workspace_path=?, audio_hash=?, audio_path=?, engine=?, device=?, start_sec=?
WHERE id=?`,
		job.State, nullStr(job.OutputPath), nullStr(job.ErrorCode), nullStr(job.ErrorMessage),
		job.Attempt, job.UpdatedAt, nullTime(job.CompletedAt),
		job.DownloadMs, job.InferenceMs, job.TotalMs,
		job.WorkspacePath, job.AudioHash, nullStr(job.AudioPath), job.Engine, job.Device, job.StartSec,
		job.ID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: update job %q: %w", job.ID, err)
	}
	return nil
}

func (r *JobRepo) List(ctx context.Context) ([]*domain.Job, error) {
	rows, err := r.db.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list jobs: %w", err)
	}
	defer rows.Close()
	jobs, err := scanJobs(rows)
	if jobs == nil {
		jobs = []*domain.Job{} // always return [] not null
	}
	return jobs, err
}

func (r *JobRepo) ListPage(ctx context.Context, limit int, before *time.Time) ([]*domain.Job, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var (
		rows *sql.Rows
		err  error
	)
	if before != nil {
		rows, err = r.db.db.QueryContext(
			ctx,
			`SELECT `+jobColumns+` FROM jobs WHERE created_at < ? ORDER BY created_at DESC LIMIT ?`,
			before.UTC(),
			limit,
		)
	} else {
		rows, err = r.db.db.QueryContext(
			ctx,
			`SELECT `+jobColumns+` FROM jobs ORDER BY created_at DESC LIMIT ?`,
			limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: list jobs page: %w", err)
	}
	defer rows.Close()

	jobs, err := scanJobs(rows)
	if jobs == nil {
		jobs = []*domain.Job{}
	}
	return jobs, err
}

func (r *JobRepo) ListByState(ctx context.Context, state domain.JobState) ([]*domain.Job, error) {
	rows, err := r.db.db.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE state = ? ORDER BY created_at ASC`, state)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list jobs by state %q: %w", state, err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (r *JobRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete job %q: %w", id, err)
	}
	return nil
}

// ---- CacheRepository --------------------------------------------------------

// CacheRepo implements domain.CacheRepository on top of *DB.
type CacheRepo struct{ db *DB }

// NewCacheRepo returns a CacheRepository backed by the given DB.
func NewCacheRepo(db *DB) domain.CacheRepository { return &CacheRepo{db: db} }

func (c *CacheRepo) Lookup(ctx context.Context, key string) (string, bool, error) {
	var midiPath string
	err := c.db.db.QueryRowContext(ctx,
		`SELECT midi_path FROM cache WHERE cache_key = ?`, key,
	).Scan(&midiPath)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite: cache lookup: %w", err)
	}
	// Increment hit count asynchronously (best-effort).
	_, _ = c.db.db.ExecContext(ctx,
		`UPDATE cache SET hit_count = hit_count + 1 WHERE cache_key = ?`, key)
	return midiPath, true, nil
}

func (c *CacheRepo) Store(ctx context.Context, key, midiPath string) error {
	_, err := c.db.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO cache (cache_key, midi_path, created_at) VALUES (?, ?, ?)`,
		key, midiPath, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("sqlite: cache store: %w", err)
	}
	return nil
}

func (c *CacheRepo) Invalidate(ctx context.Context, key string) error {
	_, err := c.db.db.ExecContext(ctx, `DELETE FROM cache WHERE cache_key = ?`, key)
	if err != nil {
		return fmt.Errorf("sqlite: cache invalidate: %w", err)
	}
	return nil
}

// ---- scan helpers -----------------------------------------------------------

const jobColumns = `id, state, youtube_url, audio_path, workspace_path, audio_hash,
  engine, device, start_sec, output_path, error_code, error_message, attempt, max_attempts,
  created_at, updated_at, completed_at, download_ms, inference_ms, total_ms`

func scanJob(row *sql.Row) (*domain.Job, error) {
	j := &domain.Job{}
	var youtubeURL, audioPath, outputPath, errorCode, errorMessage sql.NullString
	var completedAt sql.NullTime
	err := row.Scan(
		&j.ID, &j.State, &youtubeURL, &audioPath, &j.WorkspacePath, &j.AudioHash,
		&j.Engine, &j.Device, &j.StartSec, &outputPath, &errorCode, &errorMessage,
		&j.Attempt, &j.MaxAttempts,
		&j.CreatedAt, &j.UpdatedAt, &completedAt,
		&j.DownloadMs, &j.InferenceMs, &j.TotalMs,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found")
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: scan job: %w", err)
	}
	j.YoutubeURL = youtubeURL.String
	j.AudioPath = audioPath.String
	j.OutputPath = outputPath.String
	j.ErrorCode = errorCode.String
	j.ErrorMessage = errorMessage.String
	if completedAt.Valid {
		t := completedAt.Time
		j.CompletedAt = &t
	}
	return j, nil
}

func scanJobs(rows *sql.Rows) ([]*domain.Job, error) {
	var jobs []*domain.Job
	for rows.Next() {
		j := &domain.Job{}
		var youtubeURL, audioPath, outputPath, errorCode, errorMessage sql.NullString
		var completedAt sql.NullTime
		if err := rows.Scan(
			&j.ID, &j.State, &youtubeURL, &audioPath, &j.WorkspacePath, &j.AudioHash,
			&j.Engine, &j.Device, &j.StartSec, &outputPath, &errorCode, &errorMessage,
			&j.Attempt, &j.MaxAttempts,
			&j.CreatedAt, &j.UpdatedAt, &completedAt,
			&j.DownloadMs, &j.InferenceMs, &j.TotalMs,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan jobs row: %w", err)
		}
		j.YoutubeURL = youtubeURL.String
		j.AudioPath = audioPath.String
		j.OutputPath = outputPath.String
		j.ErrorCode = errorCode.String
		j.ErrorMessage = errorMessage.String
		if completedAt.Valid {
			t := completedAt.Time
			j.CompletedAt = &t
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// ---- utilities --------------------------------------------------------------

func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func newID() string {
	// Use a timestamp + random suffix for human-readable, sortable IDs.
	return fmt.Sprintf("%d-%s", time.Now().UnixMilli(), randomHex(4))
}

func randomHex(n int) string {
	b := make([]byte, n)
	// Non-security ID generation: use timestamp bytes as a simple suffix.
	ns := time.Now().UnixNano()
	for i := range b {
		b[i] = byte(ns >> uint(i*8))
	}
	return fmt.Sprintf("%x", b)
}

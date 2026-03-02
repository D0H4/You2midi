package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"you2midi/internal/domain"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "repo-test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestJobRepo_CRUDAndListByState(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	repo := NewJobRepo(db)
	ctx := context.Background()

	created, err := repo.Create(ctx, &domain.Job{
		State:       domain.JobStateQueued,
		YoutubeURL:  "https://youtube.com/watch?v=test",
		Engine:      "transkun",
		Device:      "cpu",
		StartSec:    15,
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty id")
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.YoutubeURL != created.YoutubeURL {
		t.Fatalf("youtube_url mismatch: %q vs %q", got.YoutubeURL, created.YoutubeURL)
	}
	if got.StartSec != 15 {
		t.Fatalf("start_sec mismatch: %d", got.StartSec)
	}

	now := time.Now().UTC()
	got.State = domain.JobStateCompleted
	got.OutputPath = "/tmp/out.mid"
	got.AudioPath = "/tmp/audio.wav"
	got.AudioHash = "abc123"
	got.Engine = "neuralnote"
	got.CompletedAt = &now
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	completed, err := repo.ListByState(ctx, domain.JobStateCompleted)
	if err != nil {
		t.Fatalf("ListByState: %v", err)
	}
	if len(completed) != 1 {
		t.Fatalf("expected 1 completed job, got %d", len(completed))
	}
	if completed[0].OutputPath != "/tmp/out.mid" {
		t.Fatalf("unexpected output path: %s", completed[0].OutputPath)
	}
	if completed[0].AudioPath != "/tmp/audio.wav" {
		t.Fatalf("unexpected audio path: %s", completed[0].AudioPath)
	}
	if completed[0].AudioHash != "abc123" {
		t.Fatalf("unexpected audio hash: %s", completed[0].AudioHash)
	}
	if completed[0].Engine != "neuralnote" {
		t.Fatalf("unexpected engine: %s", completed[0].Engine)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 job in list, got %d", len(all))
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, created.ID); err == nil {
		t.Fatal("expected Get error after delete")
	}
}

func TestJobRepo_ListPage(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	repo := NewJobRepo(db).(*JobRepo)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := repo.Create(ctx, &domain.Job{
			State:       domain.JobStateQueued,
			YoutubeURL:  "https://youtube.com/watch?v=test",
			Engine:      "transkun",
			Device:      "cpu",
			MaxAttempts: 3,
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	page1, err := repo.ListPage(ctx, 2, nil)
	if err != nil {
		t.Fatalf("ListPage page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 rows in page1, got %d", len(page1))
	}
	if !page1[0].CreatedAt.After(page1[1].CreatedAt) && !page1[0].CreatedAt.Equal(page1[1].CreatedAt) {
		t.Fatalf("expected newest-first ordering")
	}

	before := page1[len(page1)-1].CreatedAt
	page2, err := repo.ListPage(ctx, 2, &before)
	if err != nil {
		t.Fatalf("ListPage page2: %v", err)
	}
	if len(page2) == 0 {
		t.Fatal("expected older rows in page2")
	}
	for _, job := range page2 {
		if !job.CreatedAt.Before(before) {
			t.Fatalf("expected page2 created_at < before, got %s >= %s", job.CreatedAt, before)
		}
	}
}

func TestCacheRepo_StoreLookupInvalidate(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	repo := NewCacheRepo(db)
	ctx := context.Background()

	key := "cache-key-1"
	path := "/tmp/cache.mid"
	if err := repo.Store(ctx, key, path); err != nil {
		t.Fatalf("Store: %v", err)
	}

	gotPath, hit, err := repo.Lookup(ctx, key)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !hit {
		t.Fatal("expected cache hit")
	}
	if gotPath != path {
		t.Fatalf("expected midi_path %q, got %q", path, gotPath)
	}

	var hitCount int
	if err := db.db.QueryRowContext(ctx, `SELECT hit_count FROM cache WHERE cache_key = ?`, key).Scan(&hitCount); err != nil {
		t.Fatalf("query hit_count: %v", err)
	}
	if hitCount < 1 {
		t.Fatalf("expected hit_count >= 1, got %d", hitCount)
	}

	if err := repo.Invalidate(ctx, key); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	_, hit, err = repo.Lookup(ctx, key)
	if err != nil {
		t.Fatalf("Lookup after invalidate: %v", err)
	}
	if hit {
		t.Fatal("expected cache miss after invalidate")
	}
}

func TestOpen_MigratesLegacySchema(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`
CREATE TABLE jobs (
	id TEXT PRIMARY KEY,
	state TEXT NOT NULL DEFAULT 'queued',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);

CREATE TABLE cache (
	cache_key TEXT PRIMARY KEY
);
`); err != nil {
		_ = raw.Close()
		t.Fatalf("seed legacy schema: %v", err)
	}
	_ = raw.Close()

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("unexpected schema version: got=%d want=%d", version, currentSchemaVersion)
	}

	tx, err := db.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	for _, col := range []string{
		"workspace_path", "audio_hash", "engine", "device",
		"start_sec",
		"max_attempts", "options_json", "download_ms", "inference_ms", "total_ms",
	} {
		ok, err := columnExists(tx, "jobs", col)
		if err != nil {
			t.Fatalf("columnExists jobs.%s: %v", col, err)
		}
		if !ok {
			t.Fatalf("expected migrated column jobs.%s", col)
		}
	}
}

func TestOpen_FailsOnFutureSchemaVersion(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "future.db")

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`
CREATE TABLE schema_meta (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL,
	updated_at DATETIME NOT NULL
);
INSERT INTO schema_meta (id, version, updated_at) VALUES (1, 999, CURRENT_TIMESTAMP);
`); err != nil {
		_ = raw.Close()
		t.Fatalf("seed future schema version: %v", err)
	}
	_ = raw.Close()

	db, err := Open(dbPath)
	if err == nil {
		_ = db.Close()
		t.Fatal("expected Open to fail for future schema version")
	}
}

func TestOpen_RepairsBrokenV1SchemaWithMissingColumns(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "broken-v1.db")

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`
CREATE TABLE schema_meta (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL,
	updated_at DATETIME NOT NULL
);
INSERT INTO schema_meta (id, version, updated_at) VALUES (1, 1, CURRENT_TIMESTAMP);

CREATE TABLE jobs (
	id TEXT PRIMARY KEY,
	state TEXT NOT NULL DEFAULT 'queued',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);

CREATE TABLE cache (
	cache_key TEXT PRIMARY KEY
);
`); err != nil {
		_ = raw.Close()
		t.Fatalf("seed broken v1 schema: %v", err)
	}
	_ = raw.Close()

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open broken v1 db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&version); err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("unexpected schema version: got=%d want=%d", version, currentSchemaVersion)
	}

	tx, err := db.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	for _, col := range []string{"start_sec", "workspace_path", "audio_hash", "engine", "device"} {
		ok, err := columnExists(tx, "jobs", col)
		if err != nil {
			t.Fatalf("columnExists jobs.%s: %v", col, err)
		}
		if !ok {
			t.Fatalf("expected repaired column jobs.%s", col)
		}
	}
}

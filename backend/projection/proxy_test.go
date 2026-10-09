package projection

import (
	"context"
	"database/sql"
	"testing"
)

func openProxyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db
}

func TestProxySchemaCreatesJobsTable(t *testing.T) {
	db := openProxyTestDB(t)
	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='proxy_jobs'`).Scan(&name); err != nil {
		t.Fatalf("proxy_jobs table missing: %v", err)
	}
	if _, err := ProxyJobForFile(context.Background(), db, 42, 7); err != sql.ErrNoRows {
		t.Fatalf("empty lookup err = %v, want sql.ErrNoRows", err)
	}
}

func TestProxyJobLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openProxyTestDB(t)
	source := DownloadFile{Revision: 3, ContentMsgID: 11, StoredSize: 9000}

	job, started, err := StartProxyJob(ctx, db, 42, 7, source)
	if err != nil || !started {
		t.Fatalf("start = %+v, %v, want started", job, err)
	}
	if job.State != ProxyStateReady && job.State != ProxyStateRunning {
		t.Fatalf("state = %q", job.State)
	}
	if job.SourceRevision != 3 || job.SourceContentMsg != 11 || job.SourceSize != 9000 {
		t.Fatalf("source pin = %+v", job)
	}

	// A second trigger while running must not start a duplicate attempt.
	if _, restated, err := StartProxyJob(ctx, db, 42, 7, source); err != nil || restated {
		t.Fatalf("retrigger running = %v, %v, want existing", restated, err)
	}

	if err := UpdateProxyProgress(ctx, db, 42, 7, 42.5); err != nil {
		t.Fatalf("progress: %v", err)
	}
	job, err = ProxyJobForFile(ctx, db, 42, 7)
	if err != nil || job.Progress != 42.5 {
		t.Fatalf("progress = %+v, %v", job, err)
	}

	parts := []ProxyPart{{MsgID: 77, Size: 5000}}
	if err := CompleteProxyJob(ctx, db, 42, 7, source, parts, 5000, 4800); err != nil {
		t.Fatalf("complete: %v", err)
	}
	job, err = ProxyJobForFile(ctx, db, 42, 7)
	if err != nil || job.State != ProxyStateReady || job.Progress != 100 {
		t.Fatalf("completed = %+v, %v", job, err)
	}
	if len(job.Parts) != 1 || job.Parts[0].MsgID != 77 || job.StoredSize != 5000 || job.PlaintextSize != 4800 {
		t.Fatalf("body = %+v", job)
	}

	// A ready job is returned untouched: re-triggering is a status check.
	if _, restated, err := StartProxyJob(ctx, db, 42, 7, source); err != nil || restated {
		t.Fatalf("retrigger ready = %v, %v, want existing", restated, err)
	}
}

func TestProxyJobCompletionRejectsReplacedSource(t *testing.T) {
	ctx := context.Background()
	db := openProxyTestDB(t)
	source := DownloadFile{Revision: 1, ContentMsgID: 11, StoredSize: 9000}
	if _, _, err := StartProxyJob(ctx, db, 42, 7, source); err != nil {
		t.Fatalf("start: %v", err)
	}
	replaced := DownloadFile{Revision: 2, ContentMsgID: 12, StoredSize: 9100}
	if err := CompleteProxyJob(ctx, db, 42, 7, replaced, []ProxyPart{{MsgID: 77, Size: 1}}, 5000, 4800); err == nil {
		t.Fatal("completed against a replaced source, want an error")
	}
	job, err := ProxyJobForFile(ctx, db, 42, 7)
	if err != nil || job.State != ProxyStateRunning {
		t.Fatalf("job = %+v, %v, want still running", job, err)
	}
}

func TestProxyJobFailureAndRetry(t *testing.T) {
	ctx := context.Background()
	db := openProxyTestDB(t)
	source := DownloadFile{Revision: 1, ContentMsgID: 11, StoredSize: 9000}
	if _, _, err := StartProxyJob(ctx, db, 42, 7, source); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := FailProxyJob(ctx, db, 42, 7, context.DeadlineExceeded); err != nil {
		t.Fatalf("fail: %v", err)
	}
	job, err := ProxyJobForFile(ctx, db, 42, 7)
	if err != nil || job.State != ProxyStateFailed || job.Error == "" {
		t.Fatalf("failed = %+v, %v", job, err)
	}
	// A failed job may be restarted by triggering again.
	if _, restated, err := StartProxyJob(ctx, db, 42, 7, source); err != nil || !restated {
		t.Fatalf("retry = %v, %v, want a new attempt", restated, err)
	}
	job, err = ProxyJobForFile(ctx, db, 42, 7)
	if err != nil || job.State != ProxyStateRunning || job.Error != "" {
		t.Fatalf("retried = %+v, %v", job, err)
	}
}

func readyProxyJobFixture(t *testing.T, ctx context.Context, db *sql.DB, source DownloadFile) {
	t.Helper()
	if _, _, err := StartProxyJob(ctx, db, 42, 7, source); err != nil {
		t.Fatalf("start: %v", err)
	}
	parts := []ProxyPart{{MsgID: 77, Size: 5000}}
	if err := CompleteProxyJob(ctx, db, 42, 7, source, parts, 5000, 4800); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func testProxyHLS() ProxyHLS {
	return ProxyHLS{
		Playlist: ProxyPart{MsgID: 80, Size: 300},
		Init:     ProxyPart{MsgID: 81, Size: 700},
		Segments: []ProxyPart{{MsgID: 82, Size: 1000}, {MsgID: 83, Size: 900}},
	}
}

func TestProxyHLSRecordAndLookup(t *testing.T) {
	ctx := context.Background()
	db := openProxyTestDB(t)
	source := DownloadFile{Revision: 3, ContentMsgID: 11, StoredSize: 9000}
	readyProxyJobFixture(t, ctx, db, source)

	if job, err := ProxyJobForFile(ctx, db, 42, 7); err != nil || job.HasHLS() {
		t.Fatalf("job = %+v, %v, want no HLS before packaging", job, err)
	}
	if err := RecordProxyHLS(ctx, db, 42, 7, source, testProxyHLS()); err != nil {
		t.Fatalf("record HLS: %v", err)
	}
	job, err := ProxyJobForFile(ctx, db, 42, 7)
	if err != nil || !job.HasHLS() {
		t.Fatalf("job = %+v, %v, want HLS", job, err)
	}
	if len(job.HLS.Segments) != 2 || job.HLS.Playlist.MsgID != 80 || job.HLS.Init.MsgID != 81 {
		t.Fatalf("HLS = %+v", job.HLS)
	}
	if job.State != ProxyStateReady {
		t.Fatalf("state = %q, want ready", job.State)
	}
}

func TestProxyHLSRejectsReplacedSource(t *testing.T) {
	ctx := context.Background()
	db := openProxyTestDB(t)
	source := DownloadFile{Revision: 1, ContentMsgID: 11, StoredSize: 9000}
	readyProxyJobFixture(t, ctx, db, source)

	replaced := DownloadFile{Revision: 2, ContentMsgID: 12, StoredSize: 9100}
	if err := RecordProxyHLS(ctx, db, 42, 7, replaced, testProxyHLS()); err == nil {
		t.Fatal("recorded HLS against a replaced source, want an error")
	}
	if job, err := ProxyJobForFile(ctx, db, 42, 7); err != nil || job.HasHLS() {
		t.Fatalf("job = %+v, %v, want still no HLS", job, err)
	}
}

func TestProxySchemaTopUpAddsHLSParts(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	// Simulate a database from before the HLS rendition: the table without
	// hls_parts. EnsureProxySchema must add the column, not rebuild.
	if _, err := db.Exec(`CREATE TABLE proxy_jobs (
		channel_id INTEGER NOT NULL, file_id INTEGER NOT NULL, state TEXT NOT NULL,
		progress REAL NOT NULL DEFAULT 0, proxy_parts TEXT NOT NULL DEFAULT '',
		proxy_stored_size INTEGER NOT NULL DEFAULT 0, proxy_plaintext_size INTEGER NOT NULL DEFAULT 0,
		source_revision INTEGER NOT NULL DEFAULT 0, source_content_msg INTEGER NOT NULL DEFAULT 0,
		source_upload_uuid TEXT NOT NULL DEFAULT '', source_size INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL,
		PRIMARY KEY (channel_id, file_id))`); err != nil {
		t.Fatalf("legacy table: %v", err)
	}
	if err := EnsureProxySchema(db); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !dbTableHasColumns(db, "proxy_jobs", "hls_parts") {
		t.Fatal("hls_parts column missing after EnsureProxySchema")
	}
}

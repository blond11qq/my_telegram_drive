package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Streaming-proxy job states. A row is created running by the manual
// transcode trigger and ends ready or failed; failed rows may be restarted
// by triggering again.
const (
	ProxyStateRunning = "running"
	ProxyStateReady   = "ready"
	ProxyStateFailed  = "failed"
)

// ProxyPart is one stored Telegram document body of a transcoded proxy file.
// Single-part proxies (the common case) carry exactly one entry.
type ProxyPart struct {
	MsgID int64 `json:"msg_id"`
	Size  int64 `json:"size"`
}

// ProxyHLS is the packaged HTTP Live Streaming rendition of a proxy: a VOD
// playlist with fMP4 init + media segments. Every file is stored (and
// encrypted) independently so players fetch them as ordinary small parts.
type ProxyHLS struct {
	Playlist ProxyPart   `json:"playlist"`
	Init     ProxyPart   `json:"init"`
	Segments []ProxyPart `json:"segments"`
}

// ProxyJob is the durable record of one file's streaming proxy: the
// transcode state machine plus, once ready, the body reference the
// playback path serves instead of the original bytes.
type ProxyJob struct {
	ChannelID int64
	FileID    int64
	State     string
	Progress  float64
	Parts     []ProxyPart
	// HLS, when present, is the fMP4 VOD rendition packaged alongside the
	// progressive MP4. Players that can take HLS use it; everything else
	// keeps serving the MP4 untouched.
	HLS *ProxyHLS
	// StoredSize is the total stored (possibly ciphertext) proxy bytes;
	// PlaintextSize is the transcoded container size exposed to players.
	StoredSize    int64
	PlaintextSize int64
	// Source pins the original revision the proxy was built from, so a
	// replacement mid-transcode is detected before the mapping is recorded.
	SourceRevision   int64
	SourceContentMsg int64
	SourceUploadUUID string
	SourceSize       int64
	Error            string
	UpdatedAt        int64
}

// EnsureProxySchema adds the streaming-proxy job table. It follows the
// file_renditions pattern: CREATE IF NOT EXISTS, safe on every startup,
// with the version bump recorded in schema.go (v15) rather than a reshape.
func EnsureProxySchema(db *sql.DB) error {
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS proxy_jobs (
			channel_id INTEGER NOT NULL,
			file_id INTEGER NOT NULL,
			state TEXT NOT NULL,
			progress REAL NOT NULL DEFAULT 0,
			proxy_parts TEXT NOT NULL DEFAULT '',
			proxy_stored_size INTEGER NOT NULL DEFAULT 0,
			proxy_plaintext_size INTEGER NOT NULL DEFAULT 0,
			hls_parts TEXT NOT NULL DEFAULT '',
			source_revision INTEGER NOT NULL DEFAULT 0,
			source_content_msg INTEGER NOT NULL DEFAULT 0,
			source_upload_uuid TEXT NOT NULL DEFAULT '',
			source_size INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (channel_id, file_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_proxy_jobs_state
			ON proxy_jobs(channel_id, state)`,
	} {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("projection: ensure proxy jobs: %w", err)
		}
	}
	// Databases created before the HLS rendition have the table without
	// hls_parts; top it up in place. Fresh installs already include it.
	if !dbTableHasColumns(db, "proxy_jobs", "hls_parts") {
		if _, err := db.Exec(`ALTER TABLE proxy_jobs ADD COLUMN hls_parts TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("projection: add proxy_jobs.hls_parts: %w", err)
		}
	}
	return nil
}

func encodeProxyParts(parts []ProxyPart) (string, error) {
	if len(parts) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeProxyParts(encoded string) ([]ProxyPart, error) {
	if encoded == "" {
		return nil, nil
	}
	var parts []ProxyPart
	if err := json.Unmarshal([]byte(encoded), &parts); err != nil {
		return nil, err
	}
	return parts, nil
}

func scanProxyJob(row *sql.Row) (ProxyJob, error) {
	var job ProxyJob
	var parts, hls string
	err := row.Scan(
		&job.ChannelID, &job.FileID, &job.State, &job.Progress, &parts,
		&job.StoredSize, &job.PlaintextSize, &hls,
		&job.SourceRevision, &job.SourceContentMsg, &job.SourceUploadUUID, &job.SourceSize,
		&job.Error, &job.UpdatedAt,
	)
	if err != nil {
		return ProxyJob{}, err
	}
	job.Parts, err = decodeProxyParts(parts)
	if err != nil {
		return ProxyJob{}, fmt.Errorf("projection: decode proxy parts: %w", err)
	}
	// A corrupt HLS envelope degrades to "no HLS" rather than failing the
	// row: the progressive MP4 keeps serving regardless.
	if decoded, err := decodeProxyHLS(hls); err == nil {
		job.HLS = decoded
	}
	return job, nil
}

const proxyJobColumns = `channel_id, file_id, state, progress, proxy_parts,
	proxy_stored_size, proxy_plaintext_size, hls_parts,
	source_revision, source_content_msg, source_upload_uuid, source_size,
	error, updated_at`

// HasHLS reports whether a packaged fMP4 VOD rendition is recorded and
// structurally valid: a playlist, an init segment, and at least one media
// segment, all with positive message and size references.
func (job ProxyJob) HasHLS() bool {
	if job.HLS == nil {
		return false
	}
	if job.HLS.Playlist.MsgID <= 0 || job.HLS.Playlist.Size <= 0 ||
		job.HLS.Init.MsgID <= 0 || job.HLS.Init.Size <= 0 ||
		len(job.HLS.Segments) == 0 {
		return false
	}
	for _, seg := range job.HLS.Segments {
		if seg.MsgID <= 0 || seg.Size <= 0 {
			return false
		}
	}
	return true
}

func decodeProxyHLS(encoded string) (*ProxyHLS, error) {
	if encoded == "" {
		return nil, nil
	}
	var hls ProxyHLS
	if err := json.Unmarshal([]byte(encoded), &hls); err != nil {
		return nil, err
	}
	return &hls, nil
}

// RecordProxyHLS attaches a packaged HLS rendition to a ready proxy. Like
// CompleteProxyJob it enforces the trigger-time source binding, so an HLS
// built from a replaced original is never recorded.
func RecordProxyHLS(ctx context.Context, db *sql.DB, channelID, fileID int64, source DownloadFile, hls ProxyHLS) error {
	if err := validateContext(ctx, "proxy job hls"); err != nil {
		return err
	}
	if hls.Playlist.MsgID <= 0 || hls.Init.MsgID <= 0 || len(hls.Segments) == 0 {
		return fmt.Errorf("projection: invalid HLS rendition")
	}
	encoded, err := json.Marshal(hls)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `UPDATE proxy_jobs SET hls_parts=?, updated_at=?
		WHERE channel_id=? AND file_id=? AND state=?
		AND source_revision=? AND source_content_msg=? AND source_upload_uuid=? AND source_size=?`,
		string(encoded), time.Now().Unix(),
		channelID, fileID, ProxyStateReady,
		source.Revision, source.ContentMsgID, source.UploadUUID, source.StoredSize)
	if err != nil {
		return fmt.Errorf("projection: record proxy HLS: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("projection: proxy source changed during transcode")
	}
	return nil
}

// ProxyJobForFile returns the proxy job for one file. sql.ErrNoRows means no
// transcode was ever triggered (or the table predates the install); callers
// serve the original in both cases.
func ProxyJobForFile(ctx context.Context, db *sql.DB, channelID, fileID int64) (ProxyJob, error) {
	if err := validateContext(ctx, "proxy job lookup"); err != nil {
		return ProxyJob{}, err
	}
	if db == nil || channelID <= 0 || fileID <= 0 {
		return ProxyJob{}, fmt.Errorf("projection: invalid proxy job lookup")
	}
	return scanProxyJob(db.QueryRowContext(ctx,
		`SELECT `+proxyJobColumns+` FROM proxy_jobs WHERE channel_id=? AND file_id=?`,
		channelID, fileID))
}

// StartProxyJob records a fresh running job for one file. An already-ready
// job is returned untouched so re-triggering is a cheap status check, and an
// already-running job is returned so two triggers never transcode twice. A
// failed job is reset to running for the retry. started reports whether the
// caller owns a new attempt.
func StartProxyJob(ctx context.Context, db *sql.DB, channelID, fileID int64, source DownloadFile) (job ProxyJob, started bool, err error) {
	if err := validateContext(ctx, "proxy job start"); err != nil {
		return ProxyJob{}, false, err
	}
	if db == nil || channelID <= 0 || fileID <= 0 {
		return ProxyJob{}, false, fmt.Errorf("projection: invalid proxy job start")
	}
	existing, err := ProxyJobForFile(ctx, db, channelID, fileID)
	if err != nil && err != sql.ErrNoRows {
		return ProxyJob{}, false, err
	}
	if err == nil && (existing.State == ProxyStateReady || existing.State == ProxyStateRunning) {
		return existing, false, nil
	}
	now := time.Now().Unix()
	_, err = db.ExecContext(ctx, `INSERT INTO proxy_jobs
		(channel_id, file_id, state, progress, proxy_parts,
		proxy_stored_size, proxy_plaintext_size,
		source_revision, source_content_msg, source_upload_uuid, source_size,
		error, updated_at)
		VALUES (?, ?, ?, 0, '', 0, 0, ?, ?, ?, ?, '', ?)
		ON CONFLICT(channel_id, file_id) DO UPDATE SET
		state=excluded.state, progress=0, proxy_parts='', proxy_stored_size=0,
		proxy_plaintext_size=0, source_revision=excluded.source_revision,
		source_content_msg=excluded.source_content_msg,
		source_upload_uuid=excluded.source_upload_uuid,
		source_size=excluded.source_size, error='', updated_at=excluded.updated_at`,
		channelID, fileID, ProxyStateRunning,
		source.Revision, source.ContentMsgID, source.UploadUUID, source.StoredSize, now)
	if err != nil {
		return ProxyJob{}, false, fmt.Errorf("projection: start proxy job: %w", err)
	}
	job, err = ProxyJobForFile(ctx, db, channelID, fileID)
	if err != nil {
		return ProxyJob{}, false, err
	}
	return job, true, nil
}

// UpdateProxyProgress records stage progress (0-100) for a running job.
func UpdateProxyProgress(ctx context.Context, db *sql.DB, channelID, fileID int64, progress float64) error {
	if err := validateContext(ctx, "proxy job progress"); err != nil {
		return err
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	_, err := db.ExecContext(ctx, `UPDATE proxy_jobs SET progress=?, updated_at=?
		WHERE channel_id=? AND file_id=? AND state=?`,
		progress, time.Now().Unix(), channelID, fileID, ProxyStateRunning)
	return err
}

// CompleteProxyJob records the transcoded body for one file. The source
// binding must match the row pinned at trigger time; a mismatch means the
// original was replaced mid-transcode and the mapping must not be recorded.
func CompleteProxyJob(ctx context.Context, db *sql.DB, channelID, fileID int64, source DownloadFile, parts []ProxyPart, storedSize, plaintextSize int64) error {
	if err := validateContext(ctx, "proxy job complete"); err != nil {
		return err
	}
	if len(parts) == 0 || storedSize <= 0 || plaintextSize <= 0 {
		return fmt.Errorf("projection: invalid proxy body")
	}
	encoded, err := encodeProxyParts(parts)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `UPDATE proxy_jobs SET state=?, progress=100,
		proxy_parts=?, proxy_stored_size=?, proxy_plaintext_size=?, updated_at=?
		WHERE channel_id=? AND file_id=? AND state=?
		AND source_revision=? AND source_content_msg=? AND source_upload_uuid=? AND source_size=?`,
		ProxyStateReady, encoded, storedSize, plaintextSize, time.Now().Unix(),
		channelID, fileID, ProxyStateRunning,
		source.Revision, source.ContentMsgID, source.UploadUUID, source.StoredSize)
	if err != nil {
		return fmt.Errorf("projection: complete proxy job: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("projection: proxy source changed during transcode")
	}
	return nil
}

// FailProxyJob records a terminal transcode error. Failed jobs stay
// pollable and may be restarted by triggering again.
func FailProxyJob(ctx context.Context, db *sql.DB, channelID, fileID int64, jobErr error) error {
	if err := validateContext(ctx, "proxy job fail"); err != nil {
		return err
	}
	message := ""
	if jobErr != nil {
		message = jobErr.Error()
	}
	if len(message) > 1024 {
		message = message[:1024]
	}
	_, err := db.ExecContext(ctx, `UPDATE proxy_jobs SET state=?, error=?, updated_at=?
		WHERE channel_id=? AND file_id=? AND state=?`,
		ProxyStateFailed, message, time.Now().Unix(), channelID, fileID, ProxyStateRunning)
	return err
}

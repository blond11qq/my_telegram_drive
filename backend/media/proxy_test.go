package media

import (
	"context"
	"database/sql"
	"testing"

	"TDrive/backend/projection"
)

func TestNeedsProxyDecision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source ProxySource
		want   bool
	}{
		{"direct mp4", ProxySource{Name: "a.mp4", VideoCodec: "h264", AudioCodec: "aac", FastStart: true}, false},
		{"avc1 sample entry", ProxySource{Name: "a.mp4", VideoCodec: "avc1", AudioCodec: "mp4a", FastStart: true}, false},
		{"mov direct", ProxySource{Name: "a.mov", VideoCodec: "H264", AudioCodec: "AAC", FastStart: true}, false},
		{"m4v direct", ProxySource{Name: "a.m4v", VideoCodec: "h264", AudioCodec: "", FastStart: true}, false},
		{"silent mp4", ProxySource{Name: "a.mp4", VideoCodec: "h264", AudioCodec: "", FastStart: true}, false},
		{"matroska", ProxySource{Name: "a.mkv", VideoCodec: "h264", AudioCodec: "aac", FastStart: true}, true},
		{"avi", ProxySource{Name: "a.avi", VideoCodec: "h264", AudioCodec: "aac", FastStart: true}, true},
		{"webm", ProxySource{Name: "a.webm", VideoCodec: "h264", AudioCodec: "aac", FastStart: true}, true},
		{"hevc mp4", ProxySource{Name: "a.mp4", VideoCodec: "hevc", AudioCodec: "aac", FastStart: true}, true},
		{"dts audio", ProxySource{Name: "a.mp4", VideoCodec: "h264", AudioCodec: "dts", FastStart: true}, true},
		{"moov at end", ProxySource{Name: "a.mp4", VideoCodec: "h264", AudioCodec: "aac", FastStart: false}, true},
		{"unverified mp4", ProxySource{Name: "a.mp4"}, true},
		{"no extension", ProxySource{Name: "a", VideoCodec: "h264", AudioCodec: "aac", FastStart: true}, true},
		{"photo", ProxySource{Name: "a.jpg", VideoCodec: "", AudioCodec: "", FastStart: false}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := NeedsProxy(tc.source)
			if got != tc.want {
				t.Fatalf("NeedsProxy(%+v) = %v, want %v (%s)", tc.source, got, tc.want, reason)
			}
			if reason == "" {
				t.Fatal("empty reason")
			}
		})
	}
}

func TestFindFFmpegBinaryRejectsUnusableOverride(t *testing.T) {
	t.Setenv("TDRIVE_FFMPEG_BIN", t.TempDir()+"/no-such-ffmpeg")
	if _, err := FindFFmpegBinary(); err == nil {
		t.Fatal("missing override accepted, want an error")
	}
	// An empty PATH hides any system ffmpeg; availability must report false
	// rather than succeeding by accident.
	t.Setenv("TDRIVE_FFMPEG_BIN", "")
	t.Setenv("PATH", t.TempDir())
	if FFmpegAvailable() {
		t.Fatal("FFmpegAvailable with no binary on PATH, want false")
	}
}

func seedProxyTestFile(t *testing.T, db *sql.DB, msgID int64, name string, size int64) {
	t.Helper()
	mustApplyOp(t, db, msgID, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     name,
		FileSize: size,
	})
}

func startReadyProxyJob(t *testing.T, ctx context.Context, db *sql.DB, fileID, storedSize int64) {
	t.Helper()
	source := projection.DownloadFile{Revision: 1, ContentMsgID: fileID, StoredSize: storedSize}
	if _, _, err := projection.StartProxyJob(ctx, db, testChannelID, fileID, source); err != nil {
		t.Fatalf("start: %v", err)
	}
	parts := []projection.ProxyPart{{MsgID: 900, Size: 800}}
	if err := projection.CompleteProxyJob(ctx, db, testChannelID, fileID, source, parts, 800, 700); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestResolverServesOriginalWithoutProxyJob(t *testing.T) {
	db := newResolverTestDB(t)
	seedProxyTestFile(t, db, 10, "clip.mkv", 1234)
	got, err := NewResolver(db).Resolve(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Proxy {
		t.Fatalf("Proxy = true without a job: %+v", got)
	}
	assertSegments(t, got.Segments, []Segment{{MsgID: 10, Size: 1234}})
}

func TestResolverSubstitutesReadyProxy(t *testing.T) {
	ctx := context.Background()
	db := newResolverTestDB(t)
	seedProxyTestFile(t, db, 10, "clip.mkv", 1234)
	startReadyProxyJob(t, ctx, db, 10, 1234)
	got, err := NewResolver(db).Resolve(ctx, testChannelID, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Proxy {
		t.Fatalf("Proxy = false with a ready job: %+v", got)
	}
	if got.StoredSize != 800 || got.PlaintextSize != 700 {
		t.Fatalf("sizes stored=%d plain=%d, want 800/700", got.StoredSize, got.PlaintextSize)
	}
	assertSegments(t, got.Segments, []Segment{{MsgID: 900, Size: 800}})
	if got.Name != "clip.mkv" || got.FileID != 10 {
		t.Fatalf("identity changed: %+v", got)
	}
}

func TestResolverFallsBackWhileJobRuns(t *testing.T) {
	ctx := context.Background()
	db := newResolverTestDB(t)
	seedProxyTestFile(t, db, 10, "clip.mkv", 1234)
	source := projection.DownloadFile{Revision: 1, ContentMsgID: 10, StoredSize: 1234}
	if _, _, err := projection.StartProxyJob(ctx, db, testChannelID, 10, source); err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := NewResolver(db).Resolve(ctx, testChannelID, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Proxy {
		t.Fatalf("Proxy = true while running: %+v", got)
	}
	assertSegments(t, got.Segments, []Segment{{MsgID: 10, Size: 1234}})
}

func TestResolverOriginalOnlySkipsReadyProxy(t *testing.T) {
	ctx := context.Background()
	db := newResolverTestDB(t)
	seedProxyTestFile(t, db, 10, "clip.mkv", 1234)
	startReadyProxyJob(t, ctx, db, 10, 1234)
	got, err := NewResolver(db).Resolve(WithOriginalOnly(ctx), testChannelID, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Proxy {
		t.Fatalf("Proxy = true with original-only: %+v", got)
	}
	assertSegments(t, got.Segments, []Segment{{MsgID: 10, Size: 1234}})
}

func TestResolverFallsBackWhenProxyTableMissing(t *testing.T) {
	db := newResolverTestDB(t)
	seedProxyTestFile(t, db, 10, "clip.mkv", 1234)
	// Simulate a database that predates the proxy_jobs migration: reads must
	// fall back to the original instead of failing.
	if _, err := db.Exec(`DROP TABLE proxy_jobs`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	got, err := NewResolver(db).Resolve(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Proxy {
		t.Fatalf("Proxy = true without a table: %+v", got)
	}
	assertSegments(t, got.Segments, []Segment{{MsgID: 10, Size: 1234}})
}

func TestProxyLookupCarriesHLSMapping(t *testing.T) {
	ctx := context.Background()
	db := newResolverTestDB(t)
	seedProxyTestFile(t, db, 10, "clip.mkv", 1234)
	startReadyProxyJob(t, ctx, db, 10, 1234)
	source := projection.DownloadFile{Revision: 1, ContentMsgID: 10, StoredSize: 1234}
	hls := projection.ProxyHLS{
		Playlist: projection.ProxyPart{MsgID: 100, Size: 300},
		Init:     projection.ProxyPart{MsgID: 101, Size: 700},
		Segments: []projection.ProxyPart{{MsgID: 102, Size: 1000}},
	}
	if err := projection.RecordProxyHLS(ctx, db, testChannelID, 10, source, hls); err != nil {
		t.Fatalf("record HLS: %v", err)
	}
	mapping, err := NewResolver(db).ProxyLookup(ctx, testChannelID, 10)
	if err != nil || mapping.HLS == nil {
		t.Fatalf("lookup = %+v, %v, want HLS", mapping, err)
	}
	if mapping.HLS.Playlist.MsgID != 100 || mapping.HLS.Init.MsgID != 101 ||
		len(mapping.HLS.Segments) != 1 || mapping.HLS.Segments[0].MsgID != 102 {
		t.Fatalf("HLS = %+v", mapping.HLS)
	}
	// The MP4 mapping stays intact alongside the rendition.
	assertSegments(t, mapping.Parts, []Segment{{MsgID: 900, Size: 800}})

	// A corrupt HLS envelope drops the rendition but keeps the MP4 mapping.
	if _, err := db.Exec(`UPDATE proxy_jobs SET hls_parts='{' WHERE channel_id=? AND file_id=?`, testChannelID, 10); err != nil {
		t.Fatalf("corrupt HLS: %v", err)
	}
	mapping, err = NewResolver(db).ProxyLookup(ctx, testChannelID, 10)
	if err != nil || mapping.HLS != nil {
		t.Fatalf("lookup = %+v, %v, want MP4 without HLS", mapping, err)
	}
	assertSegments(t, mapping.Parts, []Segment{{MsgID: 900, Size: 800}})
}

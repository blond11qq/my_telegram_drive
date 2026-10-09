package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"TDrive/backend"
	"TDrive/backend/projection"
)

func seedProxyDaemonFile(t *testing.T, channelID, msgID int64, name string, size int64) {
	t.Helper()
	op := projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     name,
		FileSize: size,
	}
	header := projection.Format(op)
	if _, err := projection.ProjectFromOp(backend.DB, channelID, msgID, op, 7, header); err != nil {
		t.Fatalf("seed file: %v", err)
	}
}

func TestProxyStatusUnknownPathIsNotUnknownCommand(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	engine := newDaemonMountEngine(t, 8_810_001, 8_810_002)
	server := &Server{engine: engine, state: newState()}

	req, err := NewRequest(CommandProxyStatus, ProxyStatusRequest{Path: "/nope.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	frame := server.handleRequest(t.Context(), req)
	if frame.OK {
		t.Fatal("status for a missing path succeeded, want an error")
	}
	if strings.Contains(frame.Error, "unknown command") {
		t.Fatalf("error = %q, want handler routing, not unknown command", frame.Error)
	}
}

func TestProxyStatusReportsNoneForUntriggeredFile(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const channelID = 8_810_011
	engine := newDaemonMountEngine(t, channelID, 8_810_012)
	server := &Server{engine: engine, state: newState()}
	seedProxyDaemonFile(t, channelID, 40, "movie.mkv", 2048)

	req, err := NewRequest(CommandProxyStatus, ProxyStatusRequest{Path: "/movie.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	frame := server.handleRequest(t.Context(), req)
	if !frame.OK {
		t.Fatalf("status failed: %s", frame.Error)
	}
	var out ProxyStatusResponse
	if err := json.Unmarshal(frame.Payload, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Job.State != "none" || out.Job.FileID != 40 || out.Job.FileName != "movie.mkv" {
		t.Fatalf("job = %+v, want none/40/movie.mkv", out.Job)
	}
	if !out.Job.NeedsProxy || out.Job.NeedsProxyReason == "" {
		t.Fatalf("job = %+v, want a needs-proxy advisory", out.Job)
	}
}

func TestProxyTranscodeTriggerRecordsFailedJobWithoutFFmpeg(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const channelID = 8_810_021
	engine := newDaemonMountEngine(t, channelID, 8_810_022)
	server := &Server{engine: engine, state: newState()}
	seedProxyDaemonFile(t, channelID, 41, "movie.mkv", 2048)

	// Deterministic absence: bogus override plus an empty PATH.
	t.Setenv("TDRIVE_FFMPEG_BIN", t.TempDir()+"/no-such-ffmpeg")
	t.Setenv("PATH", t.TempDir())

	req, err := NewRequest(CommandProxyTranscode, ProxyTranscodeRequest{Path: "/movie.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	frame := server.handleRequest(t.Context(), req)
	if !frame.OK {
		t.Fatalf("trigger failed: %s", frame.Error)
	}
	var accepted ProxyTranscodeResponse
	if err := json.Unmarshal(frame.Payload, &accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.Job.State != "running" {
		t.Fatalf("trigger state = %q, want running", accepted.Job.State)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		statusReq, err := NewRequest(CommandProxyStatus, ProxyStatusRequest{Path: "/movie.mkv"})
		if err != nil {
			t.Fatal(err)
		}
		statusFrame := server.handleRequest(context.Background(), statusReq)
		if !statusFrame.OK {
			t.Fatalf("status failed: %s", statusFrame.Error)
		}
		var status ProxyStatusResponse
		if err := json.Unmarshal(statusFrame.Payload, &status); err != nil {
			t.Fatalf("decode status: %v", err)
		}
		if status.Job.State == "failed" {
			if !strings.Contains(strings.ToLower(status.Job.Error), "ffmpeg") {
				t.Fatalf("error = %q, want an ffmpeg error", status.Job.Error)
			}
			return
		}
		if status.Job.State != "running" {
			t.Fatalf("state = %q, want running then failed", status.Job.State)
		}
		if time.Now().After(deadline) {
			t.Fatal("job never reached failed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestProxyHLSWithoutMappingFallsBack(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	const channelID = 8_810_031
	engine := newDaemonMountEngine(t, channelID, 8_810_032)
	server := &Server{engine: engine, state: newState()}
	seedProxyDaemonFile(t, channelID, 42, "movie.mkv", 2048)

	// No job row at all: the MP4 path must stay untouched, so this errors
	// instead of serving anything.
	req, err := NewRequest(CommandProxyHLS, ProxyHLSRequest{Path: "/movie.mkv", File: "playlist"})
	if err != nil {
		t.Fatal(err)
	}
	frame := server.handleRequest(t.Context(), req)
	if frame.OK {
		t.Fatal("HLS without a mapping succeeded, want an error")
	}
	if strings.Contains(frame.Error, "unknown command") {
		t.Fatalf("error = %q, want handler routing", frame.Error)
	}
}

func TestProxyHLSSelectorValidation(t *testing.T) {
	job := projection.ProxyJob{
		State: projection.ProxyStateReady,
		HLS: &projection.ProxyHLS{
			Playlist: projection.ProxyPart{MsgID: 80, Size: 300},
			Init:     projection.ProxyPart{MsgID: 81, Size: 700},
			Segments: []projection.ProxyPart{{MsgID: 82, Size: 1000}, {MsgID: 83, Size: 900}},
		},
	}
	for file, want := range map[string]struct {
		msgID int64
		typ   string
	}{
		"playlist": {80, "application/vnd.apple.mpegurl"},
		"init":     {81, "video/mp4"},
		"seg0":     {82, "video/mp4"},
		"seg1":     {83, "video/mp4"},
	} {
		part, contentType, err := selectProxyHLSPart(job, file)
		if err != nil || part.MsgID != want.msgID || contentType != want.typ {
			t.Fatalf("file %q = %+v %q %v", file, part, contentType, err)
		}
	}
	for _, file := range []string{"", "seg", "seg-1", "seg2", "seg00x", "playlist.m3u8", "SEG0"} {
		if _, _, err := selectProxyHLSPart(job, file); err == nil {
			t.Fatalf("file %q accepted, want rejection", file)
		}
	}
}

// The post-unlock sweep is best-effort wiring: on an engine whose vault was
// never unlocked it must simply find no keys and change nothing.
func TestResolveLockedNamesWiringIsNoOpWhenLocked(t *testing.T) {
	configureDaemonPolicyTestHome(t)
	engine := newDaemonMountEngine(t, 8_810_051, 8_810_052)
	server := &Server{engine: engine, state: newState()}
	server.resolveLockedNames()
}

package file

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type proxyEventRecorder struct {
	mu     sync.Mutex
	events []proxyEvent
}

type proxyEvent struct {
	name string
	args []any
}

func (r *proxyEventRecorder) Emit(name string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, proxyEvent{name: name, args: args})
}

func (r *proxyEventRecorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.name)
	}
	return out
}

// seedProxySource stores body bytes in the fake Telegram channel and
// projects the matching file row, returning the file's message id and the
// expected plaintext.
func seedProxySource(t *testing.T, svc *Service, db *sql.DB, tg *tgclient.Fake, name string, encrypted bool, key []byte) (int64, []byte) {
	t.Helper()
	ctx := context.Background()
	plain := bytes.Repeat([]byte("proxy-test-video-payload-0123456789"), 64)
	stored := plain
	if encrypted {
		var cipher bytes.Buffer
		if err := tdcrypto.EncryptStream(bytes.NewReader(plain), &cipher, key, int64(len(plain))); err != nil {
			t.Fatalf("encrypt fixture: %v", err)
		}
		stored = cipher.Bytes()
	}
	peer, err := svc.Peers.ResolvePeer(ctx, personalChannelID)
	if err != nil {
		t.Fatalf("resolve peer: %v", err)
	}
	sent, err := tg.SendFile(ctx, peer, bytes.NewReader(stored), name, "", int64(len(stored)), nil)
	if err != nil {
		t.Fatalf("seed send: %v", err)
	}
	op := projection.Op{
		Type:              projection.OpFileUpload,
		Name:              name,
		FileSize:          int64(len(stored)),
		Encrypted:         encrypted,
		PlaintextSize:     int64(len(plain)),
		EncryptionVersion: 1,
	}
	if _, err := projection.ProjectFromOp(db, personalChannelID, sent.MsgID, op, 7, projection.Format(op)); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return sent.MsgID, plain
}

func waitProxyTerminal(t *testing.T, svc *Service, fileID int64) projection.ProxyJob {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		job, err := svc.ProxyJobStatus(context.Background(), personalChannelID, fileID)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if job.State == projection.ProxyStateReady || job.State == projection.ProxyStateFailed {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job stuck at %q (progress %.1f)", job.State, job.Progress)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func writeFakeFFmpeg(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
in=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-i" ]; then in="$a"; fi
  prev="$a"
done
out=""
for a in "$@"; do out="$a"; done
if [ -z "$in" ] || [ -z "$out" ]; then echo "fake-ffmpeg: missing input/output" >&2; exit 2; fi
cp "$in" "$out"
`
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

// writeFakeFFmpegHLS behaves like writeFakeFFmpeg for MP4 outputs but
// synthesizes a VOD fMP4 set (playlist + init + one segment) whenever the
// output is a playlist, exercising the HLS packaging path without real
// ffmpeg.
func writeFakeFFmpegHLS(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
in=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-i" ]; then in="$a"; fi
  prev="$a"
done
out=""
for a in "$@"; do out="$a"; done
if [ -z "$in" ] || [ -z "$out" ]; then echo "fake-ffmpeg: missing input/output" >&2; exit 2; fi
case "$out" in
*.m3u8)
  d=$(dirname "$out")
  printf '#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-MAP:URI="init.mp4"\n#EXTINF:6.0,\nseg000.m4s\n#EXT-X-ENDLIST\n' > "$out"
  cp "$in" "$d/init.mp4"
  cp "$in" "$d/seg000.m4s"
  ;;
*) cp "$in" "$out";;
esac
`
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

// writeFakeFFmpegNoHLS copies MP4 outputs but fails playlist outputs, like
// a host whose ffmpeg cannot package HLS: the MP4 proxy must still record.
func writeFakeFFmpegNoHLS(t *testing.T) string {
	t.Helper()
	script := `#!/bin/sh
out=""
for a in "$@"; do out="$a"; done
case "$out" in
*.m3u8) echo "fake-ffmpeg: HLS muxer unavailable" >&2; exit 1;;
esac
in=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-i" ]; then in="$a"; fi
  prev="$a"
done
cp "$in" "$out"
`
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

func TestProxyTranscodeFailsWithoutFFmpeg(t *testing.T) {
	svc, db, tg, _ := newTestService(t)
	key := bytes.Repeat([]byte{9}, 32)
	svc.RequireEncryptionKey = func(want bool) ([]byte, error) {
		if want {
			return append([]byte(nil), key...), nil
		}
		return nil, nil
	}
	fileID, _ := seedProxySource(t, svc, db, tg, "movie.mkv", false, key)

	// Deterministic absence: bogus override plus an empty PATH.
	t.Setenv("TDRIVE_FFMPEG_BIN", t.TempDir()+"/no-such-ffmpeg")
	t.Setenv("PATH", t.TempDir())

	if _, err := svc.StartProxyTranscode(context.Background(), personalChannelID, fileID); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	job := waitProxyTerminal(t, svc, fileID)
	if job.State != projection.ProxyStateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
	if !strings.Contains(strings.ToLower(job.Error), "ffmpeg") {
		t.Fatalf("error = %q, want an ffmpeg error", job.Error)
	}
}

func TestProxyTranscodeEndToEnd(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "encrypted"}[encrypted], func(t *testing.T) {
			svc, db, tg, _ := newTestService(t)
			key := bytes.Repeat([]byte{5}, 32)
			svc.RequireEncryptionKey = func(want bool) ([]byte, error) {
				if want {
					return append([]byte(nil), key...), nil
				}
				return nil, nil
			}
			recorder := &proxyEventRecorder{}
			svc.Events = recorder
			t.Setenv("TDRIVE_FFMPEG_BIN", writeFakeFFmpeg(t))

			fileID, plain := seedProxySource(t, svc, db, tg, "movie.mkv", encrypted, key)
			if _, err := svc.StartProxyTranscode(context.Background(), personalChannelID, fileID); err != nil {
				t.Fatalf("trigger: %v", err)
			}
			job := waitProxyTerminal(t, svc, fileID)
			if job.State != projection.ProxyStateReady {
				t.Fatalf("state = %q, error %q, want ready", job.State, job.Error)
			}
			if len(job.Parts) != 1 || job.Parts[0].MsgID <= 0 {
				t.Fatalf("parts = %+v", job.Parts)
			}
			if job.PlaintextSize != int64(len(plain)) {
				t.Fatalf("plaintext size = %d, want %d", job.PlaintextSize, len(plain))
			}
			if encrypted && job.StoredSize == job.PlaintextSize {
				t.Fatal("encrypted proxy stored size equals plaintext, want ciphertext")
			}

			// The hidden body round-trips through the same stream format as
			// the original: decrypting it yields the transcoded bytes.
			peer, _ := svc.Peers.ResolvePeer(context.Background(), personalChannelID)
			var stored bytes.Buffer
			if err := tg.DownloadFile(context.Background(), peer, job.Parts[0].MsgID, &stored, nil); err != nil {
				t.Fatalf("download proxy body: %v", err)
			}
			got := stored.Bytes()
			if encrypted {
				var out bytes.Buffer
				if _, err := tdcrypto.DecryptStream(bytes.NewReader(got), &out, key); err != nil {
					t.Fatalf("decrypt proxy: %v", err)
				}
				got = out.Bytes()
			}
			if !bytes.Equal(got, plain) {
				t.Fatal("proxy bytes do not match the transcoded output")
			}

			// Progress was reported through Emit, ending at 100.
			seenDone := false
			for _, name := range recorder.names() {
				if name == "proxy_progress" {
					seenDone = true
				}
			}
			if !seenDone {
				t.Fatalf("events = %v, want proxy_progress", recorder.names())
			}
			last := recorder.events[len(recorder.events)-1]
			if last.name != "proxy_progress" {
				t.Fatalf("last event = %q, want proxy_progress 100", last.name)
			}
		})
	}
}

func TestProxyTranscodeTriggerIsIdempotent(t *testing.T) {
	svc, db, tg, _ := newTestService(t)
	svc.RequireEncryptionKey = func(want bool) ([]byte, error) { return nil, nil }
	fileID, _ := seedProxySource(t, svc, db, tg, "movie.mkv", false, nil)
	t.Setenv("TDRIVE_FFMPEG_BIN", writeFakeFFmpeg(t))

	first, err := svc.StartProxyTranscode(context.Background(), personalChannelID, fileID)
	if err != nil {
		t.Fatalf("first trigger: %v", err)
	}
	second, err := svc.StartProxyTranscode(context.Background(), personalChannelID, fileID)
	if err != nil {
		t.Fatalf("second trigger: %v", err)
	}
	if first.State != second.State {
		t.Fatalf("states %q vs %q, want the same job", first.State, second.State)
	}
	job := waitProxyTerminal(t, svc, fileID)
	if job.State != projection.ProxyStateReady {
		t.Fatalf("state = %q, want ready", job.State)
	}
}

func TestProxyTranscodeRecordsHLSRendition(t *testing.T) {
	svc, db, tg, _ := newTestService(t)
	svc.RequireEncryptionKey = func(want bool) ([]byte, error) { return nil, nil }
	recorder := &proxyEventRecorder{}
	svc.Events = recorder
	t.Setenv("TDRIVE_FFMPEG_BIN", writeFakeFFmpegHLS(t))

	fileID, _ := seedProxySource(t, svc, db, tg, "movie.mkv", false, nil)
	if _, err := svc.StartProxyTranscode(context.Background(), personalChannelID, fileID); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	job := waitProxyTerminal(t, svc, fileID)
	if job.State != projection.ProxyStateReady {
		t.Fatalf("state = %q, error %q, want ready", job.State, job.Error)
	}
	stored, err := svc.ProxyJobStatus(context.Background(), personalChannelID, fileID)
	if err != nil || !stored.HasHLS() {
		t.Fatalf("stored = %+v, %v, want an HLS rendition", stored, err)
	}
	if len(stored.HLS.Segments) != 1 {
		t.Fatalf("segments = %+v, want one synthesized segment", stored.HLS.Segments)
	}
	// Every HLS part resolves to a hidden message holding bytes.
	peer, _ := svc.Peers.ResolvePeer(context.Background(), personalChannelID)
	for _, part := range append([]projection.ProxyPart{stored.HLS.Playlist, stored.HLS.Init}, stored.HLS.Segments...) {
		var buf bytes.Buffer
		if err := tg.DownloadFile(context.Background(), peer, part.MsgID, &buf, nil); err != nil {
			t.Fatalf("download HLS part %d: %v", part.MsgID, err)
		}
		if int64(buf.Len()) != part.Size || buf.Len() == 0 {
			t.Fatalf("part %d bytes = %d, want %d", part.MsgID, buf.Len(), part.Size)
		}
	}
}

func TestProxyTranscodeKeepsMP4WhenHLSUnavailable(t *testing.T) {
	svc, db, tg, _ := newTestService(t)
	svc.RequireEncryptionKey = func(want bool) ([]byte, error) { return nil, nil }
	t.Setenv("TDRIVE_FFMPEG_BIN", writeFakeFFmpegNoHLS(t))

	fileID, plain := seedProxySource(t, svc, db, tg, "movie.mkv", false, nil)
	if _, err := svc.StartProxyTranscode(context.Background(), personalChannelID, fileID); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	job := waitProxyTerminal(t, svc, fileID)
	if job.State != projection.ProxyStateReady {
		t.Fatalf("state = %q, error %q, want ready MP4 despite HLS failure", job.State, job.Error)
	}
	stored, err := svc.ProxyJobStatus(context.Background(), personalChannelID, fileID)
	if err != nil || stored.HasHLS() {
		t.Fatalf("stored = %+v, %v, want MP4-only mapping", stored, err)
	}
	if stored.PlaintextSize != int64(len(plain)) {
		t.Fatalf("plaintext size = %d, want %d", stored.PlaintextSize, len(plain))
	}
}

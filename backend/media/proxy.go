package media

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"TDrive/backend/media/remux"
	"TDrive/backend/projection"
)

// ProxySource describes what is known about a file without probing its
// bytes: the name (for the container) plus, when a probe already ran, the
// elementary codecs and whether the MP4 index precedes the media data.
// Empty codecs mean "unprobed", which is conservative: an unknown stream
// cannot be proven directly playable, so it needs a proxy.
type ProxySource struct {
	Name       string
	VideoCodec string
	AudioCodec string
	FastStart  bool
}

// directProxyContainers are the containers a browser video element opens
// without server help. Everything else (Matroska, AVI, WebM, ...) needs at
// least a repackage, which the proxy provides as MP4.
var directProxyContainers = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true,
}

// normalizeProxyVideo maps common H.264 spellings (including the remux
// allowlist's avc1 sample entry) to one token. Anything else, including
// empty, is not directly playable in a browser element.
func normalizeProxyVideo(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "h264", "avc", "avc1", strings.ToLower(string(remux.VideoAVC)):
		return "h264"
	default:
		return ""
	}
}

// normalizeProxyAudio maps AAC spellings (including the remux allowlist's
// mp4a sample entry) to one token. Empty means the file has no audio track,
// which direct-plays; any other codec needs a proxy transcode.
func normalizeProxyAudio(codec string) string {
	trimmed := strings.ToLower(strings.TrimSpace(codec))
	if trimmed == "" {
		return ""
	}
	if trimmed == "aac" || trimmed == "mp4a" || trimmed == strings.ToLower(string(remux.AudioAAC)) {
		return "aac"
	}
	return trimmed
}

// NeedsProxy reports whether a file needs a transcoded streaming proxy for
// browser playback. Direct play requires all of: an MP4-family container,
// H.264 video, AAC-or-absent audio, and a faststart (streaming) index.
// Anything else — HEVC, DTS audio, Matroska, moov-at-end — returns true with
// the reason, which the settings UI surfaces as the transcode hint.
func NeedsProxy(source ProxySource) (bool, string) {
	ext := strings.ToLower(filepath.Ext(source.Name))
	if !directProxyContainers[ext] {
		if ext == "" {
			return true, "file has no container extension the browser can open"
		}
		return true, fmt.Sprintf("container %s is not directly playable in a browser", ext)
	}
	if normalizeProxyVideo(source.VideoCodec) != "h264" {
		if strings.TrimSpace(source.VideoCodec) == "" {
			return true, "video codec is unverified"
		}
		return true, fmt.Sprintf("video codec %s is not browser-playable H.264", source.VideoCodec)
	}
	if audio := normalizeProxyAudio(source.AudioCodec); audio != "" && audio != "aac" {
		return true, fmt.Sprintf("audio codec %s needs transcoding to AAC", source.AudioCodec)
	}
	if !source.FastStart {
		return true, "MP4 index is not at the start of the file (no faststart)"
	}
	return false, "directly playable as H.264/AAC with faststart"
}

// FindFFmpegBinary locates the ffmpeg used for proxy transcodes: an explicit
// TDRIVE_FFMPEG_BIN override first, then PATH. It follows the mpv pattern
// (exec.LookPath, no library dependency); a missing binary is a normal
// configuration state the trigger reports as a failed job, not a build tag.
func FindFFmpegBinary() (string, error) {
	if override := os.Getenv("TDRIVE_FFMPEG_BIN"); override != "" {
		if st, err := os.Stat(override); err != nil || st.IsDir() {
			return "", fmt.Errorf("media: TDRIVE_FFMPEG_BIN is not usable")
		}
		return override, nil
	}
	return exec.LookPath("ffmpeg")
}

// FFmpegAvailable reports whether a proxy transcode can run on this host.
func FFmpegAvailable() bool {
	_, err := FindFFmpegBinary()
	return err == nil
}

// ProxyMapping is the ready transcoded body the playback path serves for
// one file: the hidden Telegram message(s) plus the sizes players need.
// Encryption matches the original (same vault key and stream format), so
// existing decryptors read it unchanged.
type ProxyMapping struct {
	Parts         []Segment
	StoredSize    int64
	PlaintextSize int64
	// HLS, when present, is the packaged fMP4 VOD rendition. Its absence
	// leaves the progressive MP4 path untouched.
	HLS *ProxyHLSMapping
}

// ProxyHLSMapping mirrors projection.ProxyHLS in media segment terms.
type ProxyHLSMapping struct {
	Playlist Segment
	Init     Segment
	Segments []Segment
}

type originalOnlyKey struct{}

// WithOriginalOnly marks a read context as wanting the pristine original
// bytes even when a streaming proxy is ready. The web layer sets it for
// ?download=1 (and ?original=1) so downloads never silently substitute the
// transcoded derivative.
func WithOriginalOnly(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, originalOnlyKey{}, true)
}

// OriginalOnly reports whether ctx wants the pristine original bytes.
func OriginalOnly(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	original, _ := ctx.Value(originalOnlyKey{}).(bool)
	return original
}

// ProxyLookup returns the ready transcoded body for one file. Any error —
// no job row, job not ready, corrupt parts, or a database that predates the
// proxy_jobs table — means "serve the original", and callers must fall back
// rather than fail the read.
func (r *Resolver) ProxyLookup(ctx context.Context, channelID, fileID int64) (ProxyMapping, error) {
	if r == nil || r.db == nil {
		return ProxyMapping{}, fmt.Errorf("media: db not ready")
	}
	if channelID <= 0 || fileID <= 0 {
		return ProxyMapping{}, fmt.Errorf("media: invalid proxy lookup")
	}
	job, err := projection.ProxyJobForFile(ctx, r.db, channelID, fileID)
	if err != nil {
		return ProxyMapping{}, err
	}
	if job.State != projection.ProxyStateReady {
		return ProxyMapping{}, sql.ErrNoRows
	}
	if len(job.Parts) == 0 || job.StoredSize <= 0 || job.PlaintextSize <= 0 {
		return ProxyMapping{}, sql.ErrNoRows
	}
	mapping := ProxyMapping{StoredSize: job.StoredSize, PlaintextSize: job.PlaintextSize}
	var total int64
	for _, part := range job.Parts {
		if part.MsgID <= 0 || part.Size <= 0 {
			return ProxyMapping{}, sql.ErrNoRows
		}
		mapping.Parts = append(mapping.Parts, Segment{MsgID: part.MsgID, Size: part.Size})
		total += part.Size
	}
	if total != job.StoredSize {
		return ProxyMapping{}, sql.ErrNoRows
	}
	// HLS decodes best-effort: a corrupt envelope drops the rendition while
	// the MP4 mapping above keeps serving.
	if job.HasHLS() {
		hls := &ProxyHLSMapping{
			Playlist: Segment{MsgID: job.HLS.Playlist.MsgID, Size: job.HLS.Playlist.Size},
			Init:     Segment{MsgID: job.HLS.Init.MsgID, Size: job.HLS.Init.Size},
		}
		for _, seg := range job.HLS.Segments {
			hls.Segments = append(hls.Segments, Segment{MsgID: seg.MsgID, Size: seg.Size})
		}
		mapping.HLS = hls
	}
	return mapping, nil
}

// applyProxy substitutes the ready transcoded body into a resolved file. It
// never fails resolution: any lookup problem keeps the original segments.
func (r *Resolver) applyProxy(ctx context.Context, file *LogicalFile) {
	if file == nil || OriginalOnly(ctx) {
		return
	}
	mapping, err := r.ProxyLookup(ctx, file.ChannelID, file.FileID)
	if err != nil {
		return
	}
	file.Proxy = true
	file.StoredSize = mapping.StoredSize
	file.PlaintextSize = mapping.PlaintextSize
	file.Multipart = len(mapping.Parts) > 1
	file.Segments = mapping.Parts
}

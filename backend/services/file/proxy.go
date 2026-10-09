package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"TDrive/backend/datadir"
	"TDrive/backend/media"
	"TDrive/backend/projection"
	"TDrive/backend/services/servicecontext"
)

// Proxy transcode stages, emitted as proxy_progress percentages. The worker
// reports stage boundaries only: ffmpeg itself streams no machine-readable
// progress in this configuration, and honest coarse progress beats a smooth
// lie derived from wall-clock guesses.
const (
	proxyProgressDownloaded  = 30.0
	proxyProgressTranscoded  = 65.0
	proxyProgressEncrypted   = 75.0
	proxyProgressUploaded    = 95.0
	proxyProgressDone        = 100.0
	proxyFFmpegStderrPreview = 8192
)

// ffmpegCommandContext builds the transcode process. It is a variable (the
// mpv pattern) so tests inject a fake binary without needing real ffmpeg.
var ffmpegCommandContext = exec.CommandContext

// ProxyJobStatus reads the durable transcode record for one file.
// sql.ErrNoRows means no transcode was ever triggered.
func (s *Service) ProxyJobStatus(ctx context.Context, channelID, fileID int64) (projection.ProxyJob, error) {
	if s == nil || s.DB == nil {
		return projection.ProxyJob{}, fmt.Errorf("proxy status: DB not ready")
	}
	return projection.ProxyJobForFile(ctx, s.DB, channelID, fileID)
}

// StartProxyTranscode pins one file's current revision and starts a manual
// single-file transcode in the background, returning the job row
// immediately. Re-triggering while running or ready returns the existing row
// instead of starting a duplicate attempt; a failed job restarts.
//
// The caller owns no temp files: the worker stages 0600 scratch files and
// removes them on every path. The original Telegram message is never
// touched; the transcoded body uploads as separate hidden message(s).
func (s *Service) StartProxyTranscode(ctx context.Context, channelID, fileID int64) (projection.ProxyJob, error) {
	if err := servicecontext.Check(ctx, "file: start proxy transcode"); err != nil {
		return projection.ProxyJob{}, err
	}
	if err := s.ready(); err != nil {
		return projection.ProxyJob{}, err
	}
	if channelID == 0 || fileID <= 0 {
		return projection.ProxyJob{}, fmt.Errorf("file: invalid proxy transcode target")
	}
	pin, found, err := projection.FileDownloadRefContext(ctx, s.DB, channelID, fileID)
	if err != nil {
		return projection.ProxyJob{}, err
	}
	if !found {
		return projection.ProxyJob{}, fmt.Errorf("file: proxy source not found: %w", os.ErrNotExist)
	}
	job, started, err := projection.StartProxyJob(ctx, s.DB, channelID, fileID, pin)
	if err != nil {
		return projection.ProxyJob{}, err
	}
	if started {
		// The request context dies with the triggering socket call while the
		// transcode runs for minutes; detach but keep the process lifetime.
		// A daemon restart abandons the attempt and the row stays running
		// (documented in ProxyJob); retriggering after that reports running
		// until heartbeats land in a later phase.
		go s.runProxyTranscode(context.Background(), channelID, fileID, pin)
	}
	return job, nil
}

// proxyScratchDir creates the worker's private multi-file scratch directory
// on disk-backed cache storage, never on /tmp: /tmp is a small tmpfs on the
// target host (1.7GB) and a gigabyte-scale transcode stages plaintext, MP4
// and ciphertext there at once. Same rationale as webTempDir. The directory
// is 0700, staged files are chmodded 0600 by the pipeline, the worker
// removes files as stages consume them plus RemoveAll on every return path,
// and engine startup sweeps tdrive-proxy-* leftovers via CleanupCacheTemps.
func proxyScratchDir() (string, error) {
	base, err := datadir.CacheDir()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(base, "tdrive-proxy-*")
}

// proxyOperationID derives the deterministic hidden-upload identity for one
// transcode attempt from the pinned source binding, so a retry of the same
// revision resends with the same Telegram random_ids (idempotent) while a
// replaced source gets a fresh identity.
func proxyOperationID(channelID, fileID int64, pin projection.DownloadFile) string {
	binding := fmt.Sprintf("rev%d", pin.Revision)
	if pin.UploadUUID != "" {
		binding = "u" + pin.UploadUUID
	} else if pin.ContentMsgID > 0 {
		binding = fmt.Sprintf("c%d", pin.ContentMsgID)
	}
	return fmt.Sprintf("proxy/v1/%d/%d/%s", channelID, fileID, binding)
}

// proxyOutputName names the hidden attachment after the source with a proxy
// marker, so the body is recognizable when the channel is browsed directly.
// On encrypted drives the stem would leak the source name, so the attachment
// goes out opaque (f-<uuid8>.bin derived from the transcode operation),
// exactly like other encrypted hidden uploads.
func proxyOutputName(source string, encrypted bool, operationID string) string {
	if encrypted {
		return opaqueAttachmentName(operationID, 0, 1)
	}
	stem := strings.TrimSpace(source)
	if stem == "" {
		stem = "video"
	}
	if ext := filepath.Ext(stem); ext != "" {
		stem = strings.TrimSuffix(stem, ext)
	}
	return stem + ".proxy.mp4"
}

func (s *Service) runProxyTranscode(ctx context.Context, channelID, fileID int64, pin projection.DownloadFile) {
	fail := func(err error) {
		_ = projection.FailProxyJob(context.Background(), s.DB, channelID, fileID, err)
		s.emitEvent("proxy_error", fileID, err.Error())
	}
	progress := func(percent float64) {
		_ = projection.UpdateProxyProgress(context.Background(), s.DB, channelID, fileID, percent)
		s.emitEvent("proxy_progress", fileID, percent)
	}

	// Fail fast before any bytes move: without ffmpeg there is no transcode.
	bin, err := media.FindFFmpegBinary()
	if err != nil {
		fail(fmt.Errorf("proxy transcode: ffmpeg not found (install ffmpeg or set TDRIVE_FFMPEG_BIN): %w", err))
		return
	}

	workDir, err := proxyScratchDir()
	if err != nil {
		fail(fmt.Errorf("proxy transcode: stage scratch: %w", err))
		return
	}
	// Scratch holds plaintext and ciphertext; remove it on every path.
	defer func() { _ = os.RemoveAll(workDir) }()
	srcPath := filepath.Join(workDir, "source")
	outPath := filepath.Join(workDir, "proxy.mp4")
	if err := os.Chmod(workDir, 0o700); err != nil {
		fail(fmt.Errorf("proxy transcode: secure scratch: %w", err))
		return
	}

	// Stage 1: fetch the pinned original's plaintext. Service.Download
	// decrypts with the vault key when the source is encrypted.
	result := s.Download(ctx, channelID, int(fileID), int(fileID), func(string) (string, error) {
		return srcPath, nil
	})
	if result.Status != "success" {
		message := result.Message
		if message == "" {
			message = result.Status
		}
		if result.Err != nil {
			message = result.Err.Error()
		}
		fail(fmt.Errorf("proxy transcode: download original: %s", message))
		return
	}
	// Service.Download writes plaintext (decrypting with the vault key when
	// the source is encrypted), so the staged size is the output size.
	_ = os.Chmod(srcPath, 0o600)
	if stat, statErr := os.Stat(srcPath); statErr != nil || stat.Size() != pin.OutputSize {
		fail(fmt.Errorf("proxy transcode: downloaded size mismatch"))
		return
	}
	progress(proxyProgressDownloaded)

	// Stage 2: transcode to faststart H.264 + AAC.
	if err := runFFmpegTranscode(ctx, bin, srcPath, outPath); err != nil {
		fail(fmt.Errorf("proxy transcode: %w", err))
		return
	}
	_ = os.Remove(srcPath)
	outStat, err := os.Stat(outPath)
	if err != nil || outStat.Size() <= 0 {
		fail(fmt.Errorf("proxy transcode: ffmpeg produced no output"))
		return
	}
	_ = os.Chmod(outPath, 0o600)
	progress(proxyProgressTranscoded)

	// Stage 2b: package an HLS VOD rendition (fMP4 segments) from the same
	// transcode. Best-effort by design: any failure only skips the
	// rendition and the MP4 proxy below is recorded unaffected, so a host
	// whose ffmpeg lacks HLS keeps the small-file fast path working.
	var hlsParts *projection.ProxyHLS
	if packaged, err := s.packageProxyHLS(ctx, channelID, fileID, pin, bin, outPath, workDir); err != nil {
		s.warnf("proxy: HLS packaging skipped for file %d: %v", fileID, err)
	} else {
		hlsParts = packaged
	}

	// Stage 3: re-encrypt with the same vault key when the source is
	// encrypted. The stream format is identical, so existing decryptors
	// read the proxy unchanged.
	plaintextSize := outStat.Size()
	storedSize := plaintextSize
	stagedPath := outPath
	var staged *os.File
	if pin.Encrypted {
		masterKey, err := s.requireEncryptionKey(true)
		if err != nil {
			fail(fmt.Errorf("proxy transcode: vault key: %w", err))
			return
		}
		plain, err := os.Open(outPath)
		if err != nil {
			clearOwnedKey(masterKey)
			fail(fmt.Errorf("proxy transcode: open transcoded file: %w", err))
			return
		}
		staged, err = s.writeCiphertextTemp(plain, plaintextSize, masterKey)
		_ = plain.Close()
		clearOwnedKey(masterKey)
		if err != nil {
			fail(fmt.Errorf("proxy transcode: encrypt proxy: %w", err))
			return
		}
		_ = os.Remove(outPath)
		stagedPath = staged.Name()
		_ = os.Chmod(stagedPath, 0o600)
		storedSize = stagedStatSize(staged)
	}
	progress(proxyProgressEncrypted)

	// Stage 4: upload the staged body as hidden message(s). The original
	// message is never touched; the mapping below is the only link.
	stored, err := os.Open(stagedPath)
	if err != nil {
		if staged != nil {
			_ = staged.Close()
		}
		fail(fmt.Errorf("proxy transcode: open staged body: %w", err))
		return
	}
	operationID := proxyOperationID(channelID, fileID, pin)
	body, err := s.UploadHidden(ctx, channelID, HiddenUploadRequest{
		OperationID:   operationID,
		Name:          proxyOutputName(pin.Name, pin.Encrypted, operationID),
		StoredSize:    storedSize,
		PlaintextSize: plaintextSize,
		Encrypted:     pin.Encrypted,
	}, stored)
	_ = stored.Close()
	if staged != nil {
		_ = staged.Close()
	}
	_ = os.Remove(stagedPath)
	if err != nil {
		if len(body.MessageIDs) > 0 {
			_ = s.DiscardHiddenReceipt(context.Background(), channelID, proxyOperationID(channelID, fileID, pin), body)
		}
		fail(fmt.Errorf("proxy transcode: upload proxy: %w", err))
		return
	}
	if len(body.MessageIDs) == 0 {
		fail(fmt.Errorf("proxy transcode: upload returned no message"))
		return
	}
	progress(proxyProgressUploaded)

	// Stage 5: record the mapping, but only if the source still pins the
	// same revision. CompleteProxyJob enforces the binding in SQL; a
	// replacement mid-transcode fails the row instead of pointing the
	// player at bytes built from a different file.
	parts, err := proxyPartSizes(s, storedSize, body.MessageIDs)
	if err != nil {
		_ = s.DiscardHiddenReceipt(context.Background(), channelID, proxyOperationID(channelID, fileID, pin), body)
		fail(fmt.Errorf("proxy transcode: %w", err))
		return
	}
	if err := projection.CompleteProxyJob(context.Background(), s.DB, channelID, fileID, pin, parts, storedSize, plaintextSize); err != nil {
		_ = s.DiscardHiddenReceipt(context.Background(), channelID, proxyOperationID(channelID, fileID, pin), body)
		fail(fmt.Errorf("proxy transcode: %w", err))
		return
	}
	// The HLS rendition attaches to the ready MP4 mapping. Recording it is
	// best-effort too: a failure here leaves a working MP4 proxy behind.
	if hlsParts != nil {
		if err := projection.RecordProxyHLS(context.Background(), s.DB, channelID, fileID, pin, *hlsParts); err != nil {
			s.warnf("proxy: HLS record skipped for file %d: %v", fileID, err)
		}
	}
	progress(proxyProgressDone)
}

func stagedStatSize(staged *os.File) int64 {
	if staged == nil {
		return 0
	}
	if stat, err := staged.Stat(); err == nil {
		return stat.Size()
	}
	return 0
}

// packageProxyHLS repackages a transcoded MP4 into an HLS VOD set and
// uploads every file as its own hidden message, returning the rendition
// mapping. Any packaging, encryption, or upload failure aborts the whole
// rendition (the caller keeps the MP4 proxy regardless).
func (s *Service) packageProxyHLS(ctx context.Context, channelID, fileID int64, pin projection.DownloadFile, bin, plainPath, workDir string) (*projection.ProxyHLS, error) {
	hlsDir, err := os.MkdirTemp(workDir, "hls")
	if err != nil {
		return nil, fmt.Errorf("HLS scratch: %w", err)
	}
	if err := runFFmpegHLS(ctx, bin, plainPath, hlsDir); err != nil {
		return nil, err
	}
	playlistPath, initPath, segPaths, err := collectProxyHLSFiles(hlsDir)
	if err != nil {
		return nil, err
	}
	var key []byte
	if pin.Encrypted {
		key, err = s.requireEncryptionKey(true)
		if err != nil {
			return nil, fmt.Errorf("HLS vault key: %w", err)
		}
		defer clearOwnedKey(key)
	}
	opBase := proxyOperationID(channelID, fileID, pin) + "/hls"
	upload := func(path string) (projection.ProxyPart, error) {
		return s.uploadProxyHLSFile(ctx, channelID, opBase, pin, key, path)
	}
	playlist, err := upload(playlistPath)
	if err != nil {
		return nil, fmt.Errorf("HLS playlist: %w", err)
	}
	init, err := upload(initPath)
	if err != nil {
		return nil, fmt.Errorf("HLS init: %w", err)
	}
	hls := &projection.ProxyHLS{Playlist: playlist, Init: init}
	for _, segPath := range segPaths {
		part, err := upload(segPath)
		if err != nil {
			return nil, fmt.Errorf("HLS segment: %w", err)
		}
		hls.Segments = append(hls.Segments, part)
	}
	return hls, nil
}

// collectProxyHLSFiles finds the packaged VOD set ffmpeg wrote: exactly one
// playlist and init plus the sorted media segments, all non-empty. Anything
// else means the ffmpeg on this host cannot package HLS.
func collectProxyHLSFiles(dir string) (playlist, init string, segs []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", nil, fmt.Errorf("read HLS output: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		switch name := entry.Name(); {
		case name == "playlist.m3u8":
			playlist = filepath.Join(dir, name)
		case name == "init.mp4":
			init = filepath.Join(dir, name)
		case strings.HasPrefix(name, "seg") && strings.HasSuffix(name, ".m4s"):
			segs = append(segs, filepath.Join(dir, name))
		}
	}
	sort.Strings(segs)
	for _, path := range append([]string{playlist, init}, segs...) {
		if path == "" {
			return "", "", nil, fmt.Errorf("incomplete HLS set in %s", dir)
		}
		if stat, statErr := os.Stat(path); statErr != nil || stat.Size() <= 0 {
			return "", "", nil, fmt.Errorf("empty HLS file %s", filepath.Base(path))
		}
	}
	if len(segs) == 0 {
		return "", "", nil, fmt.Errorf("no HLS segments in %s", dir)
	}
	return playlist, init, segs, nil
}

// uploadProxyHLSFile encrypts (when the source is encrypted) and hidden-
// uploads one small HLS file, returning its single body reference. HLS
// files are kilobytes to a few megabytes, so multi-part bodies never occur;
// anything else aborts the rendition rather than complicating the mapping.
func (s *Service) uploadProxyHLSFile(ctx context.Context, channelID int64, opBase string, pin projection.DownloadFile, key []byte, path string) (projection.ProxyPart, error) {
	plain, err := os.Open(path)
	if err != nil {
		return projection.ProxyPart{}, err
	}
	defer plain.Close()
	stat, err := plain.Stat()
	if err != nil || stat.Size() <= 0 {
		return projection.ProxyPart{}, fmt.Errorf("unreadable HLS file %s", filepath.Base(path))
	}
	storedSize := stat.Size()
	stored := plain
	if pin.Encrypted {
		cipher, err := s.writeCiphertextTemp(plain, stat.Size(), key)
		if err != nil {
			return projection.ProxyPart{}, fmt.Errorf("encrypt HLS file: %w", err)
		}
		defer cipher.Close()
		stored = cipher
		storedSize = stagedStatSize(cipher)
	}
	if _, err := stored.Seek(0, io.SeekStart); err != nil {
		return projection.ProxyPart{}, err
	}
	base := filepath.Base(path)
	body, err := s.UploadHidden(ctx, channelID, HiddenUploadRequest{
		OperationID:   opBase + "/" + base,
		Name:          base,
		StoredSize:    storedSize,
		PlaintextSize: stat.Size(),
		Encrypted:     pin.Encrypted,
	}, stored)
	if err != nil {
		return projection.ProxyPart{}, err
	}
	parts, err := proxyPartSizes(s, storedSize, body.MessageIDs)
	if err != nil {
		return projection.ProxyPart{}, err
	}
	if len(parts) != 1 {
		return projection.ProxyPart{}, fmt.Errorf("HLS file %s split into %d parts", base, len(parts))
	}
	return parts[0], nil
}

// proxyPartSizes rebuilds per-part stored sizes from the deterministic part
// plan, matching the windows UploadHidden sent. HiddenBody carries message
// IDs but not their sizes; the plan is a pure function of the stored size.
func proxyPartSizes(s *Service, storedSize int64, messageIDs []int64) ([]projection.ProxyPart, error) {
	plan, err := s.buildUploadPartPlan(storedSize)
	if err != nil {
		return nil, fmt.Errorf("proxy part plan: %w", err)
	}
	if len(messageIDs) != plan.partCount {
		return nil, fmt.Errorf("proxy upload returned %d messages for %d parts", len(messageIDs), plan.partCount)
	}
	parts := make([]projection.ProxyPart, 0, len(messageIDs))
	for index, msgID := range messageIDs {
		_, length, err := plan.window(storedSize, index)
		if err != nil {
			return nil, fmt.Errorf("proxy part plan: %w", err)
		}
		parts = append(parts, projection.ProxyPart{MsgID: msgID, Size: length})
	}
	return parts, nil
}

// runFFmpegTranscode converts any input into a streaming MP4: H.264
// (veryfast/CRF 23), AAC 128k, faststart index. The audio map is optional
// so silent videos transcode without a second video-only pass. Threads are
// clamped to [2,4] and the process is niced when the OS provides nice.
func runFFmpegTranscode(ctx context.Context, bin, inPath, outPath string) error {
	threads := runtime.NumCPU()
	if threads < 2 {
		threads = 2
	}
	if threads > 4 {
		threads = 4
	}
	return runFFmpeg(ctx, bin,
		"-y", "-v", "error",
		"-i", inPath,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-threads", fmt.Sprintf("%d", threads),
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		outPath,
	)
}

// runFFmpegHLS repackages an already-transcoded MP4 into an HLS VOD set
// with fMP4 segments: playlist.m3u8 plus init.mp4 and segNNN.m4s beside it.
// Streams are copied, never re-encoded, so this takes seconds. The input
// must already be H.264/AAC, which the proxy transcode guarantees.
func runFFmpegHLS(ctx context.Context, bin, inPath, dir string) error {
	return runFFmpeg(ctx, bin,
		"-y", "-v", "error",
		"-i", inPath,
		"-c", "copy",
		"-hls_time", "6",
		"-hls_playlist_type", "vod",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "seg%03d.m4s"),
		filepath.Join(dir, "playlist.m3u8"),
	)
}

// runFFmpeg runs one ffmpeg invocation, nicing the process when the OS
// provides nice. Stderr is captured (capped) for error reporting.
func runFFmpeg(ctx context.Context, bin string, args ...string) error {
	name := bin
	if nice, err := exec.LookPath("nice"); err == nil {
		args = append([]string{"-n", "10", bin}, args...)
		name = nice
	}
	var stderr bytes.Buffer
	cmd := ffmpegCommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	// Stdout stays empty under -v error; cap stderr so a pathological
	// decoder cannot wedge the error path in memory.
	if err := cmd.Run(); err != nil {
		preview := strings.TrimSpace(stderr.String())
		if len(preview) > proxyFFmpegStderrPreview {
			preview = preview[:proxyFFmpegStderrPreview] + "…"
		}
		if preview == "" {
			preview = err.Error()
		}
		return fmt.Errorf("ffmpeg failed: %s", preview)
	}
	return nil
}

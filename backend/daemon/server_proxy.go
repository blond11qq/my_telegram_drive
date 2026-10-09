package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/media"
	"TDrive/backend/projection"
)

// proxyTranscode triggers a manual single-file streaming-proxy transcode for
// the file at remotePath. It records a running job and returns immediately;
// the worker updates the row and emits proxy_progress/proxy_error events,
// and proxyStatus polls the row. Re-triggering while running or ready
// returns the existing job instead of starting duplicate work.
func (s *Server) proxyTranscode(ctx context.Context, remotePath string, driveID int64) (ProxyTranscodeResponse, error) {
	scope, err := s.scopeForRequest(driveID, remotePath)
	if err != nil {
		return ProxyTranscodeResponse{}, err
	}
	drive := scope.drive
	resolved, err := s.engine.ResolveEntryPath(drive.ID, scope.cwd, remotePath)
	if err != nil {
		return ProxyTranscodeResponse{}, err
	}
	if resolved.Type != "file" {
		return ProxyTranscodeResponse{}, fmt.Errorf("%s is not a file", resolved.Path)
	}
	job, err := s.engine.FileService().StartProxyTranscode(ctx, drive.ID, resolved.MsgID)
	if err != nil {
		return ProxyTranscodeResponse{}, err
	}
	return ProxyTranscodeResponse{Drive: drive, Job: describeProxyJob(resolved.Name, resolved.MsgID, job)}, nil
}

// proxyStatus reports the pollable transcode record for the file at
// remotePath. Files never triggered report state "none".
func (s *Server) proxyStatus(ctx context.Context, remotePath string, driveID int64) (ProxyStatusResponse, error) {
	scope, err := s.scopeForRequest(driveID, remotePath)
	if err != nil {
		return ProxyStatusResponse{}, err
	}
	drive := scope.drive
	resolved, err := s.engine.ResolveEntryPath(drive.ID, scope.cwd, remotePath)
	if err != nil {
		return ProxyStatusResponse{}, err
	}
	if resolved.Type != "file" {
		return ProxyStatusResponse{}, fmt.Errorf("%s is not a file", resolved.Path)
	}
	job, err := s.engine.FileService().ProxyJobStatus(ctx, drive.ID, resolved.MsgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			needs, reason := media.NeedsProxy(media.ProxySource{Name: resolved.Name})
			return ProxyStatusResponse{Drive: drive, Job: ProxyJobStatus{
				FileID:           resolved.MsgID,
				FileName:         resolved.Name,
				State:            "none",
				NeedsProxy:       needs,
				NeedsProxyReason: reason,
			}}, nil
		}
		return ProxyStatusResponse{}, err
	}
	return ProxyStatusResponse{Drive: drive, Job: describeProxyJob(resolved.Name, resolved.MsgID, job)}, nil
}

func describeProxyJob(name string, msgID int64, job projection.ProxyJob) ProxyJobStatus {
	needs, reason := media.NeedsProxy(media.ProxySource{Name: name})
	return ProxyJobStatus{
		FileID:           msgID,
		FileName:         name,
		State:            job.State,
		Progress:         job.Progress,
		NeedsProxy:       needs,
		NeedsProxyReason: reason,
		HasHLS:           job.HasHLS(),
		Error:            job.Error,
		UpdatedAt:        job.UpdatedAt,
	}
}

// proxyHLSMaxBytes caps one served HLS file. Segments are seconds of video;
// anything larger is a corrupt mapping, and the daemon must not buffer it.
const proxyHLSMaxBytes = 256 << 20

// proxyHLS serves one packaged HLS file (playlist, init, or media segment)
// for a file whose proxy mapping carries the rendition. Without an HLS
// mapping it errors and callers fall back to the progressive MP4 path,
// which this handler never touches.
func (s *Server) proxyHLS(ctx context.Context, remotePath, file string, driveID int64) (ProxyHLSResponse, error) {
	scope, err := s.scopeForRequest(driveID, remotePath)
	if err != nil {
		return ProxyHLSResponse{}, err
	}
	drive := scope.drive
	resolved, err := s.engine.ResolveEntryPath(drive.ID, scope.cwd, remotePath)
	if err != nil {
		return ProxyHLSResponse{}, err
	}
	if resolved.Type != "file" {
		return ProxyHLSResponse{}, fmt.Errorf("%s is not a file", resolved.Path)
	}
	job, err := s.engine.FileService().ProxyJobStatus(ctx, drive.ID, resolved.MsgID)
	if err != nil {
		return ProxyHLSResponse{}, err
	}
	if job.State != projection.ProxyStateReady || !job.HasHLS() {
		return ProxyHLSResponse{}, fmt.Errorf("no HLS rendition for %s", resolved.Path)
	}
	part, contentType, err := selectProxyHLSPart(job, file)
	if err != nil {
		return ProxyHLSResponse{}, err
	}
	if part.Size <= 0 || part.Size > proxyHLSMaxBytes {
		return ProxyHLSResponse{}, fmt.Errorf("proxy HLS file %q has an invalid size", file)
	}
	peer, err := s.engine.ResolvePeer(ctx, drive.ID)
	if err != nil {
		return ProxyHLSResponse{}, err
	}
	var stored bytes.Buffer
	if err := s.engine.Telegram().DownloadFile(ctx, peer, part.MsgID, &stored, nil); err != nil {
		return ProxyHLSResponse{}, err
	}
	if int64(stored.Len()) != part.Size {
		return ProxyHLSResponse{}, fmt.Errorf("proxy HLS file %q size mismatch", file)
	}
	plain := stored.Bytes()
	if resolved.Encrypted {
		key, err := s.engine.EncryptionService().RequireMasterKeyForFile(true)
		if err != nil {
			return ProxyHLSResponse{}, err
		}
		var out bytes.Buffer
		if _, err := tdcrypto.DecryptStream(bytes.NewReader(plain), &out, key); err != nil {
			clear(key)
			return ProxyHLSResponse{}, fmt.Errorf("proxy HLS decrypt: %w", err)
		}
		clear(key)
		plain = out.Bytes()
	}
	return ProxyHLSResponse{
		Drive:       drive,
		File:        file,
		ContentType: contentType,
		Size:        int64(len(plain)),
		DataBase64:  base64.StdEncoding.EncodeToString(plain),
	}, nil
}

// selectProxyHLSPart resolves an HLS file selector to its stored body:
// "playlist" and "init" by name, "segN" by zero-based media segment index.
func selectProxyHLSPart(job projection.ProxyJob, file string) (projection.ProxyPart, string, error) {
	switch file {
	case "playlist":
		return job.HLS.Playlist, "application/vnd.apple.mpegurl", nil
	case "init":
		return job.HLS.Init, "video/mp4", nil
	}
	index, ok := strings.CutPrefix(file, "seg")
	if !ok {
		return projection.ProxyPart{}, "", fmt.Errorf("unknown proxy HLS file %q", file)
	}
	n, err := strconv.Atoi(index)
	if err != nil || n < 0 || n >= len(job.HLS.Segments) {
		return projection.ProxyPart{}, "", fmt.Errorf("unknown proxy HLS file %q", file)
	}
	return job.HLS.Segments[n], "video/mp4", nil
}

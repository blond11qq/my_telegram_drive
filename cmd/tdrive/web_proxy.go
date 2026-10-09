package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Streaming-proxy web surface: a default-off feature flag plus a manual
// single-file transcode trigger and its pollable status. The daemon owns the
// transcode worker and the durable proxy_jobs row; these handlers are thin
// JSON adapters following the handleTokenRotate (settings) and jobSnapshot
// (pollable job) patterns.

// webProxySettingsFileName persists the proxy flag beside the web token.
const webProxySettingsFileName = "proxy.json"

func (s *webServer) getProxyEnabled() bool {
	s.proxyMu.RLock()
	defer s.proxyMu.RUnlock()
	return s.proxyEnabled
}

func (s *webServer) setProxyEnabled(enabled bool) {
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	s.proxyEnabled = enabled
}

func webProxySettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "TDrive", webProxySettingsFileName), nil
}

func loadWebProxyEnabled() bool {
	path, err := webProxySettingsPath()
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var settings struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return false
	}
	return settings.Enabled
}

func saveWebProxyEnabled(enabled bool) error {
	path, err := webProxySettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"enabled": enabled})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func (s *webServer) handleProxySettings(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "enabled": s.getProxyEnabled()})
	case http.MethodPost:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		defer request.Body.Close()
		if err := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20)).Decode(&body); err != nil {
			writeWebError(response, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.Enabled == nil {
			writeWebError(response, http.StatusBadRequest, "enabled required")
			return
		}
		if err := saveWebProxyEnabled(*body.Enabled); err != nil {
			writeWebError(response, http.StatusInternalServerError, err.Error())
			return
		}
		s.setProxyEnabled(*body.Enabled)
		writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "enabled": *body.Enabled})
	default:
		writeWebError(response, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

// handleProxyTranscode starts a manual single-file streaming-proxy
// transcode. It returns the job row immediately; progress arrives via daemon
// proxy_progress events and GET /api/proxy/status polls the durable record.
func (s *webServer) handleProxyTranscode(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeWebError(response, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !s.getProxyEnabled() {
		writeWebError(response, http.StatusForbidden, "streaming proxy is disabled (enable it under settings first)")
		return
	}
	var body webPathRequest
	if err := decodeWebPathRequest(request, &body); err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.client.ProxyTranscodeInDrive(s.driveID, body.Path)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "job": out.Job})
}

// handleProxyStatus reports the pollable transcode record for one file.
// Files never triggered report state "none" alongside the direct-play
// assessment, so the UI can offer the manual trigger.
func (s *webServer) handleProxyStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeWebError(response, http.StatusMethodNotAllowed, "GET only")
		return
	}
	remotePath, err := cleanWebPath(request.URL.Query().Get("path"))
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.client.ProxyStatusInDrive(s.driveID, remotePath)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "job": out.Job})
}

// handleProxyHLS serves one packaged HLS file (playlist, init, or media
// segment) for a file whose proxy mapping carries the rendition. It sits
// behind the same token gate as every other route. The playlist's relative
// segment references are rewritten to absolute endpoint URLs (relative
// resolution would drop the query the lookup needs); unknown files and
// files without an HLS mapping answer 404 so players fall back to the
// progressive MP4 proxy, which this endpoint never touches.
func (s *webServer) handleProxyHLS(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeWebError(response, http.StatusMethodNotAllowed, "GET only")
		return
	}
	remotePath, err := cleanWebPath(request.URL.Query().Get("path"))
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	file := request.URL.Query().Get("f")
	if !validProxyHLSFile(file) {
		writeWebError(response, http.StatusNotFound, "unknown HLS file")
		return
	}
	out, err := s.client.ProxyHLSInDrive(s.driveID, remotePath, file)
	if err != nil {
		if isProxyHLSMissing(err) {
			writeWebError(response, http.StatusNotFound, "no HLS rendition for this file")
			return
		}
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	raw, err := base64.StdEncoding.DecodeString(out.DataBase64)
	if err != nil || int64(len(raw)) != out.Size {
		writeWebError(response, http.StatusBadGateway, "invalid HLS payload")
		return
	}
	contentType := out.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if file == "playlist" {
		raw = rewriteProxyPlaylist(raw, func(segment string) string {
			return s.proxyHLSURL(request, remotePath, segment)
		})
		response.Header().Set("Content-Type", contentType)
		response.Header().Set("Cache-Control", "no-store")
		_, _ = response.Write(raw)
		return
	}
	// ServeContent keeps an explicitly set type and adds Range support, so
	// seeking inside init and media segments works like the MP4 path.
	if response.Header().Get("Content-Type") == "" {
		response.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(response, request, file, time.Time{}, bytes.NewReader(raw))
}

func validProxyHLSFile(file string) bool {
	switch file {
	case "playlist", "init":
		return true
	}
	index, ok := strings.CutPrefix(file, "seg")
	if !ok || index == "" {
		return false
	}
	_, err := strconv.Atoi(index)
	return err == nil
}

func isProxyHLSMissing(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "unknown proxy HLS file") ||
		strings.Contains(message, "no HLS rendition")
}

// proxyHLSURL builds the absolute endpoint URL for one HLS file. Absolute
// URLs are required because relative references resolve against the playlist
// path and would drop the query carrying the file lookup. The request token
// rides along when the playlist fetch used one; otherwise the cookie the
// gate set carries same-origin segment fetches.
func (s *webServer) proxyHLSURL(request *http.Request, remotePath, file string) string {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	target := scheme + "://" + request.Host + "/proxy-hls?path=" +
		url.QueryEscape(remotePath) + "&f=" + url.QueryEscape(file)
	if token := request.URL.Query().Get("token"); token != "" {
		target += "&token=" + url.QueryEscape(token)
	}
	return target
}

// rewriteProxyPlaylist maps ffmpeg's relative segment references to endpoint
// URLs: the EXT-X-MAP init to the init endpoint and each media URI, in
// order, to segN. Every other line passes through untouched.
func rewriteProxyPlaylist(raw []byte, fileURL func(file string) string) []byte {
	var out strings.Builder
	segment := 0
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "#EXT-X-MAP:"):
			out.WriteString(replaceProxyMapURI(line, fileURL("init")))
			out.WriteString("\n")
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			out.WriteString(line)
			out.WriteString("\n")
		default:
			out.WriteString(fileURL(fmt.Sprintf("seg%d", segment)))
			out.WriteString("\n")
			segment++
		}
	}
	return []byte(out.String())
}

func replaceProxyMapURI(line, uri string) string {
	start := strings.Index(line, `URI="`)
	if start < 0 {
		return line
	}
	rest := line[start+5:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return line
	}
	return line[:start+5] + uri + `"` + rest[end+1:]
}

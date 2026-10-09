package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"TDrive/backend/daemon"
)

var (
	testProxyTranscodeOut daemon.ProxyTranscodeResponse
	testProxyTranscodeErr error
	testProxyStatusOut    daemon.ProxyStatusResponse
	testProxyStatusErr    error
	testProxyHLSOut       daemon.ProxyHLSResponse
	testProxyHLSErr       error
)

func (f *fakeWebClient) ProxyTranscodeInDrive(_ int64, _ string) (daemon.ProxyTranscodeResponse, error) {
	return testProxyTranscodeOut, testProxyTranscodeErr
}

func (f *fakeWebClient) ProxyStatusInDrive(_ int64, _ string) (daemon.ProxyStatusResponse, error) {
	return testProxyStatusOut, testProxyStatusErr
}

func (f *fakeWebClient) ProxyHLSInDrive(_ int64, _ string, _ string) (daemon.ProxyHLSResponse, error) {
	return testProxyHLSOut, testProxyHLSErr
}

func newProxyTestServer() *webServer {
	return &webServer{client: &fakeWebClient{}, token: "test-token", resolve: func() (string, string, error) {
		return "", "", nil
	}, ui: "UI"}
}

func TestProxySettingsDefaultOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	server := newProxyTestServer()
	handler := server.routes()

	request := httptest.NewRequest(http.MethodGet, webauthed("/api/settings/proxy"), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("settings GET = %d, want 200", recorder.Code)
	}
	var body struct {
		OK      bool `json:"ok"`
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || !body.OK || body.Enabled {
		t.Fatalf("settings body = %s, want ok with enabled=false", recorder.Body.String())
	}
}

func TestProxySettingsTogglePersists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := newProxyTestServer()
	handler := server.routes()

	for _, enabled := range []bool{true, false} {
		payload := `{"enabled":false}`
		if enabled {
			payload = `{"enabled":true}`
		}
		request := httptest.NewRequest(http.MethodPost, webauthed("/api/settings/proxy"), strings.NewReader(payload))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("settings POST %v = %d, body %s", enabled, recorder.Code, recorder.Body.String())
		}
		if got := server.getProxyEnabled(); got != enabled {
			t.Fatalf("flag = %v, want %v", got, enabled)
		}
		raw, err := os.ReadFile(filepath.Join(home, ".config", "TDrive", "proxy.json"))
		if err != nil {
			t.Fatalf("settings not persisted: %v", err)
		}
		var saved struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &saved); err != nil || saved.Enabled != enabled {
			t.Fatalf("saved = %s, want enabled=%v", raw, enabled)
		}
	}
	if loadWebProxyEnabled() != false {
		t.Fatal("load after disable, want false")
	}
}

func TestProxyTranscodeRequiresFlag(t *testing.T) {
	server := newProxyTestServer()
	handler := server.routes()

	request := httptest.NewRequest(http.MethodPost, webauthed("/api/proxy/transcode"), strings.NewReader(`{"path":"/movie.mkv"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("transcode while disabled = %d, want 403", recorder.Code)
	}
}

func TestProxyTranscodeStartsJob(t *testing.T) {
	testProxyTranscodeOut = daemon.ProxyTranscodeResponse{Job: daemon.ProxyJobStatus{
		FileID: 12, FileName: "movie.mkv", State: "running", Progress: 0,
		NeedsProxy: true, NeedsProxyReason: "container mkv is not directly playable in a browser",
	}}
	testProxyTranscodeErr = nil
	t.Cleanup(func() { testProxyTranscodeOut = daemon.ProxyTranscodeResponse{} })

	server := newProxyTestServer()
	server.setProxyEnabled(true)
	handler := server.routes()

	request := httptest.NewRequest(http.MethodPost, webauthed("/api/proxy/transcode"), strings.NewReader(`{"path":"/movie.mkv"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("transcode = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		OK  bool `json:"ok"`
		Job struct {
			State      string  `json:"state"`
			NeedsProxy bool    `json:"needs_proxy"`
			Reason     string  `json:"needs_proxy_reason"`
			Progress   float64 `json:"progress"`
		} `json:"job"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || !body.OK {
		t.Fatalf("transcode body = %s", recorder.Body.String())
	}
	if body.Job.State != "running" || !body.Job.NeedsProxy || body.Job.Reason == "" {
		t.Fatalf("job = %+v, want running with a needs-proxy reason", body.Job)
	}
}

func TestProxyStatusReportsJob(t *testing.T) {
	testProxyStatusOut = daemon.ProxyStatusResponse{Job: daemon.ProxyJobStatus{
		FileID: 12, FileName: "movie.mkv", State: "ready", Progress: 100,
		NeedsProxy: true, NeedsProxyReason: "container mkv is not directly playable in a browser",
	}}
	t.Cleanup(func() { testProxyStatusOut = daemon.ProxyStatusResponse{} })

	handler := newProxyTestServer().routes()
	request := httptest.NewRequest(http.MethodGet, webauthed("/api/proxy/status?path=/movie.mkv"), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		OK  bool `json:"ok"`
		Job struct {
			State    string  `json:"state"`
			Progress float64 `json:"progress"`
		} `json:"job"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || !body.OK {
		t.Fatalf("status body = %s", recorder.Body.String())
	}
	if body.Job.State != "ready" || body.Job.Progress != 100 {
		t.Fatalf("job = %+v, want ready/100", body.Job)
	}
}

func TestWebFileDownloadRequestsOriginalUpstream(t *testing.T) {
	var sawOriginal string
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		sawOriginal = request.URL.Query().Get("original")
		_, _ = response.Write([]byte("bytes"))
	}))
	defer upstream.Close()
	hostPort := strings.TrimPrefix(upstream.URL, "http://")
	resolve := func() (string, string, error) { return hostPort, "/cap", nil }
	handler := newWebTestServer(&fakeWebClient{}, resolve).routes()

	// Streaming playback: no original bypass, proxy may serve.
	stream := httptest.NewRequest(http.MethodGet, webauthed("/file?path=/a.mp4"), nil)
	streamRecorder := httptest.NewRecorder()
	handler.ServeHTTP(streamRecorder, stream)
	if streamRecorder.Code != http.StatusOK {
		t.Fatalf("stream status = %d", streamRecorder.Code)
	}
	if sawOriginal != "" {
		t.Fatalf("streaming sent ?original=%q, want none", sawOriginal)
	}

	// Explicit download: pristine original bytes only.
	download := httptest.NewRequest(http.MethodGet, webauthed("/file?path=/a.mp4&download=1"), nil)
	downloadRecorder := httptest.NewRecorder()
	handler.ServeHTTP(downloadRecorder, download)
	if downloadRecorder.Code != http.StatusOK {
		t.Fatalf("download status = %d", downloadRecorder.Code)
	}
	if sawOriginal != "1" {
		t.Fatalf("download sent ?original=%q, want 1", sawOriginal)
	}
	if got := downloadRecorder.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("Content-Disposition = %q, want attachment", got)
	}
}

func TestProxyHLSPlaylistRewritesSegmentURLs(t *testing.T) {
	playlist := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n" +
		"#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n" +
		"#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.0,\nseg000.m4s\n#EXTINF:4.2,\nseg001.m4s\n#EXT-X-ENDLIST\n"
	testProxyHLSOut = daemon.ProxyHLSResponse{
		File: "playlist", ContentType: "application/vnd.apple.mpegurl",
		Size: int64(len(playlist)), DataBase64: base64.StdEncoding.EncodeToString([]byte(playlist)),
	}
	t.Cleanup(func() { testProxyHLSOut = daemon.ProxyHLSResponse{} })

	handler := newProxyTestServer().routes()
	request := httptest.NewRequest(http.MethodGet, webauthed("/proxy-hls?path=/a.mp4&f=playlist"), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("playlist status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "seg000.m4s") || strings.Contains(body, `URI="init.mp4"`) {
		t.Fatalf("relative references survived the rewrite:\n%s", body)
	}
	for _, want := range []string{"/proxy-hls?path=%2Fa.mp4&f=init", "/proxy-hls?path=%2Fa.mp4&f=seg0", "/proxy-hls?path=%2Fa.mp4&f=seg1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing rewritten URL %q in:\n%s", want, body)
		}
	}
	// The request token rides along so segment fetches stay authorized.
	if !strings.Contains(body, "token=test-token") {
		t.Fatalf("token missing from rewritten URLs:\n%s", body)
	}
}

func TestProxyHLSServesSegmentsWithRanges(t *testing.T) {
	payload := bytes.Repeat([]byte{1}, 4096)
	testProxyHLSOut = daemon.ProxyHLSResponse{
		File: "seg0", ContentType: "video/mp4",
		Size: int64(len(payload)), DataBase64: base64.StdEncoding.EncodeToString(payload),
	}
	t.Cleanup(func() { testProxyHLSOut = daemon.ProxyHLSResponse{} })

	handler := newProxyTestServer().routes()
	request := httptest.NewRequest(http.MethodGet, webauthed("/proxy-hls?path=/a.mp4&f=seg0"), nil)
	request.Header.Set("Range", "bytes=0-99")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("segment range status = %d, want 206", recorder.Code)
	}
	if recorder.Body.Len() != 100 {
		t.Fatalf("segment range body = %d bytes, want 100", recorder.Body.Len())
	}
}

func TestProxyHLSFallsBackWithoutMapping(t *testing.T) {
	testProxyHLSErr = errors.New("no HLS rendition for /a.mp4")
	t.Cleanup(func() { testProxyHLSErr = nil })

	handler := newProxyTestServer().routes()
	for _, target := range []string{
		webauthed("/proxy-hls?path=/a.mp4&f=playlist"),
		webauthed("/proxy-hls?path=/a.mp4&f=bogus"),
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 so players fall back to MP4", target, recorder.Code)
		}
	}
}

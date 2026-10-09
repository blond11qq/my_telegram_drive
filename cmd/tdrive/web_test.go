package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"TDrive/backend/daemon"
)

func testWebModTime() time.Time { return time.Unix(1700000000, 0) }

type fakeWebClient struct {
	listOut       daemon.ListResponse
	findOut       daemon.FindResponse
	mkdirOut      daemon.EntryResponse
	removeOut     daemon.EntryResponse
	moveOut       daemon.EntryResponse
	uploadOut     daemon.UploadResponse
	uploadTemp    []string
	progressSeen  []float64
	uploadEncrypt []bool
	downloadData  []byte
	downloadErr   error
	statusOut     daemon.Status
	drivesOut     daemon.DriveListResponse
	mountOut      daemon.MountResponse
	mountErr      error
}

func (f *fakeWebClient) MountStart(string, string, string) (daemon.MountResponse, error) {
	if f.mountErr != nil {
		return daemon.MountResponse{}, f.mountErr
	}
	return f.mountOut, nil
}

func (f *fakeWebClient) MountStatus() (daemon.MountResponse, error) {
	return f.mountOut, nil
}

func (f *fakeWebClient) Status() (daemon.Status, error) {
	return f.statusOut, nil
}

func (f *fakeWebClient) ListDrives() (daemon.DriveListResponse, error) {
	return f.drivesOut, nil
}

func (f *fakeWebClient) ListInDrive(int64, string) (daemon.ListResponse, error) {
	return f.listOut, nil
}

func (f *fakeWebClient) FindInDrive(int64, string, int) (daemon.FindResponse, error) {
	return f.findOut, nil
}

func (f *fakeWebClient) MkdirInDrive(int64, string, bool) (daemon.EntryResponse, error) {
	return f.mkdirOut, nil
}

func (f *fakeWebClient) RemoveInDrive(int64, string, bool) (daemon.EntryResponse, error) {
	return f.removeOut, nil
}

func (f *fakeWebClient) MoveInDrive(int64, string, string) (daemon.EntryResponse, error) {
	return f.moveOut, nil
}

func (f *fakeWebClient) DownloadInDrive(_ int64, remotePath, localPath string, _ daemon.EventHandler) (daemon.DownloadResponse, error) {
	if f.downloadErr != nil {
		return daemon.DownloadResponse{}, f.downloadErr
	}
	content := f.downloadData
	if content == nil {
		content = []byte("copied-bytes")
	}
	if err := os.WriteFile(localPath, content, 0o600); err != nil {
		return daemon.DownloadResponse{}, err
	}
	return daemon.DownloadResponse{Entry: daemon.Entry{Type: "file", Name: remotePath, Path: remotePath, Size: int64(len(content))}, SavedPath: localPath}, nil
}

func (f *fakeWebClient) UploadInDrive(_ int64, localPath, remotePath string, encrypt, _ bool, onEvent daemon.EventHandler) (daemon.UploadResponse, error) {
	f.uploadTemp = append(f.uploadTemp, localPath)
	f.uploadEncrypt = append(f.uploadEncrypt, encrypt)
	data, err := os.ReadFile(localPath)
	if err != nil {
		return daemon.UploadResponse{}, err
	}
	if onEvent != nil {
		onEvent(daemon.Event{Name: "upload_progress", Args: []any{"id", 50.0}})
		f.progressSeen = append(f.progressSeen, 50.0)
	}
	out := f.uploadOut
	out.Entry.Path = remotePath
	out.Entry.Size = int64(len(data))
	return out, nil
}

func newWebTestServer(client webDaemonClient, resolve func() (string, string, error)) *webServer {
	if resolve == nil {
		resolve = func() (string, string, error) { return "", "", nil }
	}
	return &webServer{client: client, token: "test-token", resolve: resolve, ui: "UI"}
}

func webauthed(target string) string {
	if strings.Contains(target, "?") {
		return target + "&token=test-token"
	}
	return target + "?token=test-token"
}

func TestWebRequiresToken(t *testing.T) {
	server := newWebTestServer(&fakeWebClient{}, nil)
	handler := server.routes()

	for _, target := range []string{"/", "/api/list?path=/", "/file?path=/a.txt"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("GET %s without token = %d, want 403", target, recorder.Code)
		}
	}
}

func TestWebNoAuthDisablesTokenGate(t *testing.T) {
	server := newWebTestServer(&fakeWebClient{}, nil)
	server.setToken("")
	handler := server.routes()

	for _, target := range []string{"/api/list?path=/", "/api/status"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusForbidden {
			t.Fatalf("GET %s with auth disabled = 403, want open", target)
		}
	}
}

func TestParseWebArgsNoAuth(t *testing.T) {
	options, err := parseWebArgs([]string{"--no-auth", "--port", "8080"})
	if err != nil {
		t.Fatalf("parseWebArgs --no-auth: %v", err)
	}
	if !options.noAuth || options.port != 8080 {
		t.Fatalf("options = %+v", options)
	}
}

func TestWebListServesDaemonEntries(t *testing.T) {
	client := &fakeWebClient{
		listOut: daemon.ListResponse{
			Path: "/",
			Entries: []daemon.Entry{
				{Type: "folder", Name: "Docs", Path: "/Docs"},
				{Type: "file", Name: "a.txt", Path: "/a.txt", Size: 3},
			},
		},
	}
	handler := newWebTestServer(client, nil).routes()
	request := httptest.NewRequest(http.MethodGet, webauthed("/api/list?path=/"), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		OK      bool           `json:"ok"`
		Path    string         `json:"path"`
		Entries []daemon.Entry `json:"entries"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("list body is not JSON: %v", err)
	}
	if !body.OK || len(body.Entries) != 2 || body.Entries[1].Name != "a.txt" {
		t.Fatalf("unexpected list body: %s", recorder.Body.String())
	}
}

func TestWebRejectsPathTraversal(t *testing.T) {
	handler := newWebTestServer(&fakeWebClient{}, nil).routes()
	request := httptest.NewRequest(http.MethodGet, webauthed("/api/list?path=../secret"), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("relative path status = %d, want 400", recorder.Code)
	}
}

func TestWebFileProxiesRangeThrough(t *testing.T) {
	content := []byte("0123456789abcdef")
	var hostPort string
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Host != hostPort {
			t.Errorf("upstream Host = %q, want loopback host %q", request.Host, hostPort)
		}
		http.ServeContent(response, request, "a.txt", testWebModTime(), bytes.NewReader(content))
	}))
	defer upstream.Close()
	hostPort = strings.TrimPrefix(upstream.URL, "http://")
	resolve := func() (string, string, error) { return hostPort, "/cap", nil }
	handler := newWebTestServer(&fakeWebClient{}, resolve).routes()

	request := httptest.NewRequest(http.MethodGet, webauthed("/file?path=/a.txt"), nil)
	request.Header.Set("Range", "bytes=4-7")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206 (body %q)", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "4567" {
		t.Fatalf("range body = %q, want %q", recorder.Body.String(), "4567")
	}
	if got := recorder.Header().Get("Content-Range"); got != "bytes 4-7/16" {
		t.Fatalf("Content-Range = %q, want bytes 4-7/16", got)
	}
}

func TestWebFileRetriesMountCatchUp(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		if calls <= 2 {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write([]byte("fresh"))
	}))
	defer upstream.Close()
	hostPort := strings.TrimPrefix(upstream.URL, "http://")
	client := &fakeWebClient{
		listOut: daemon.ListResponse{
			Path:    "/",
			Entries: []daemon.Entry{{Type: "file", Name: "fresh.txt", Path: "/fresh.txt", Size: 5}},
		},
	}
	handler := newWebTestServer(client, func() (string, string, error) {
		return hostPort, "/cap", nil
	}).routes()
	request := httptest.NewRequest(http.MethodGet, webauthed("/file?path=/fresh.txt"), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("catch-up status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "fresh" {
		t.Fatalf("catch-up body = %q, want fresh", recorder.Body.String())
	}
}

func TestWebTempDirLivesOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	got := webTempDir()
	want := filepath.Join(dir, ".cache", "TDrive", "web-tmp")
	if got != want {
		t.Fatalf("webTempDir = %q, want %q", got, want)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("webTempDir not created: %v", err)
	}
}

func TestWebUploadRunsTwoStages(t *testing.T) {
	client := &fakeWebClient{}
	server := newWebTestServer(client, nil)
	handler := server.routes()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "hello.txt")
	if err != nil {
		t.Fatalf("multipart: %v", err)
	}
	if _, err := io.WriteString(part, "hello world"); err != nil {
		t.Fatalf("multipart write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, webauthed("/api/upload?path=/Docs"), &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var accepted struct {
		OK  bool `json:"ok"`
		Job struct {
			ID    string `json:"id"`
			Files []struct {
				Name  string `json:"name"`
				Stage string `json:"stage"`
			} `json:"files"`
		} `json:"job"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil || !accepted.OK {
		t.Fatalf("unexpected upload accept body: %s", recorder.Body.String())
	}
	if accepted.Job.ID == "" || len(accepted.Job.Files) != 1 || accepted.Job.Files[0].Stage != "queued" {
		t.Fatalf("unexpected job after stage 1: %s", recorder.Body.String())
	}

	// Stage 2 runs in the background worker: poll until the Telegram upload
	// completes through the fake daemon.
	deadline := time.Now().Add(5 * time.Second)
	for {
		statusRequest := httptest.NewRequest(http.MethodGet, webauthed("/api/upload/status?id="+accepted.Job.ID), nil)
		statusRecorder := httptest.NewRecorder()
		handler.ServeHTTP(statusRecorder, statusRequest)
		if statusRecorder.Code != http.StatusOK {
			t.Fatalf("upload status = %d, body %s", statusRecorder.Code, statusRecorder.Body.String())
		}
		var result struct {
			Job struct {
				Files []struct {
					Stage    string        `json:"stage"`
					Progress float64       `json:"progress"`
					Entry    *daemon.Entry `json:"entry"`
				} `json:"files"`
			} `json:"job"`
		}
		if err := json.Unmarshal(statusRecorder.Body.Bytes(), &result); err != nil {
			t.Fatalf("status body is not JSON: %v", err)
		}
		file := result.Job.Files[0]
		if file.Stage == "done" {
			if file.Entry == nil || file.Entry.Size != 11 {
				t.Fatalf("done file = %+v, want entry with size 11", file)
			}
			break
		}
		if file.Stage == "error" {
			t.Fatalf("stage 2 failed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("stage 2 did not finish in time (stage %q)", file.Stage)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(client.progressSeen) != 1 || client.progressSeen[0] != 50 {
		t.Fatalf("daemon progress events seen = %v, want [50]", client.progressSeen)
	}
	if len(client.uploadTemp) != 1 {
		t.Fatalf("daemon upload calls = %d, want 1", len(client.uploadTemp))
	}
	if _, err := os.Stat(client.uploadTemp[0]); !os.IsNotExist(err) {
		t.Fatalf("temp upload file was not removed: %s", client.uploadTemp[0])
	}

	unknown := httptest.NewRequest(http.MethodGet, webauthed("/api/upload/status?id=nope"), nil)
	unknownRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unknownRecorder, unknown)
	if unknownRecorder.Code != http.StatusNotFound {
		t.Fatalf("unknown job status = %d, want 404", unknownRecorder.Code)
	}
}

func TestWebCopyDuplicatesFileServerSide(t *testing.T) {
	client := &fakeWebClient{
		downloadData: []byte("copy-me"),
		listOut: daemon.ListResponse{
			Path:    "/",
			Entries: []daemon.Entry{{Type: "file", Name: "a.txt", Path: "/a.txt", Size: 7}},
		},
	}
	server := newWebTestServer(client, nil)
	handler := server.routes()
	request := httptest.NewRequest(http.MethodPost, webauthed("/api/cp"), strings.NewReader(`{"src":"/a.txt","dst":"/b.txt"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("copy status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	if len(client.uploadTemp) != 1 {
		t.Fatalf("uploads after copy = %d, want 1", len(client.uploadTemp))
	}
	if len(client.uploadEncrypt) != 1 || !client.uploadEncrypt[0] {
		t.Fatalf("copy encrypt flags = %v, want [true]", client.uploadEncrypt)
	}
	if _, err := os.Stat(client.uploadTemp[0]); !os.IsNotExist(err) {
		t.Fatalf("copy temp was not removed: %s", client.uploadTemp[0])
	}
}

func TestWebCopyRejectsFolderIntoItself(t *testing.T) {
	handler := newWebTestServer(&fakeWebClient{}, nil).routes()
	request := httptest.NewRequest(http.MethodPost, webauthed("/api/cp"), strings.NewReader(`{"src":"/docs","dst":"/docs/sub"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("self-copy status = %d, want 400", recorder.Code)
	}
}

func TestWebUploadEncryptFlag(t *testing.T) {
	postUpload := func(t *testing.T, query string) *fakeWebClient {
		t.Helper()
		client := &fakeWebClient{}
		server := newWebTestServer(client, nil)
		handler := server.routes()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("files", "secret.txt")
		_, _ = io.WriteString(part, "x")
		_ = writer.Close()
		request := httptest.NewRequest(http.MethodPost, webauthed("/api/upload?path=/"+query), &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("upload %q = %d", query, recorder.Code)
		}
		deadline := time.Now().Add(5 * time.Second)
		for len(client.uploadEncrypt) == 0 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		return client
	}
	if got := postUpload(t, "").uploadEncrypt; len(got) != 1 || !got[0] {
		t.Fatalf("default encrypt flags = %v, want [true]", got)
	}
	if got := postUpload(t, "&encrypt=1").uploadEncrypt; len(got) != 1 || !got[0] {
		t.Fatalf("encrypt=1 flags = %v, want [true]", got)
	}
	if got := postUpload(t, "&encrypt=0").uploadEncrypt; len(got) != 1 || got[0] {
		t.Fatalf("encrypt=0 flags = %v, want [false]", got)
	}
}

func TestWebStatusReportsDriveAndVault(t *testing.T) {
	client := &fakeWebClient{
		statusOut: daemon.Status{ActiveChannelID: 7, CurrentPath: "/", VaultConfigured: true, VaultUnlocked: false},
		drivesOut: daemon.DriveListResponse{Drives: []daemon.Drive{{ID: 7, Title: "TDrive", Kind: "personal"}}},
	}
	handler := newWebTestServer(client, nil).routes()
	request := httptest.NewRequest(http.MethodGet, webauthed("/api/status"), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Status struct {
			DriveID         int64  `json:"drive_id"`
			DriveTitle      string `json:"drive_title"`
			VaultConfigured bool   `json:"vault_configured"`
		} `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("status body: %v", err)
	}
	if body.Status.DriveID != 7 || body.Status.DriveTitle != "TDrive" || !body.Status.VaultConfigured {
		t.Fatalf("unexpected status: %s", recorder.Body.String())
	}
}

func TestWebTokenRotate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	server := newWebTestServer(&fakeWebClient{}, nil)
	handler := server.routes()
	request := httptest.NewRequest(http.MethodPost, webauthed("/api/settings/token"), strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("rotate = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Token == "" || body.Token == "test-token" {
		t.Fatalf("unexpected rotate body: %s", recorder.Body.String())
	}
	old := httptest.NewRequest(http.MethodGet, webauthed("/api/list?path=/"), nil)
	oldRecorder := httptest.NewRecorder()
	handler.ServeHTTP(oldRecorder, old)
	if oldRecorder.Code != http.StatusForbidden {
		t.Fatalf("old token status = %d, want 403", oldRecorder.Code)
	}
	rotated := httptest.NewRequest(http.MethodGet, "/api/list?path=/"+"&token="+body.Token, nil)
	rotatedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(rotatedRecorder, rotated)
	if rotatedRecorder.Code != http.StatusOK {
		t.Fatalf("new token status = %d, want 200", rotatedRecorder.Code)
	}
}

func TestParseWebArgs(t *testing.T) {
	options, err := parseWebArgs([]string{"--port", "9090", "--listen", "10.0.0.1"})
	if err != nil {
		t.Fatalf("parseWebArgs: %v", err)
	}
	if options.port != 9090 || options.listen != "10.0.0.1" {
		t.Fatalf("options = %+v", options)
	}
	if _, err := parseWebArgs([]string{"--port", "abc"}); err == nil {
		t.Fatal("invalid port accepted")
	}
}

func TestResolveWebListenFailsWithoutTailscale(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := resolveWebListen("", 8080); err == nil {
		t.Fatal("resolveWebListen without tailscale accepted, want error")
	} else if !strings.Contains(err.Error(), "tailscale IPv4 not available") {
		t.Fatalf("resolveWebListen error = %q, want tailscale IPv4 not available", err)
	}
}

func TestResolveWebListenExplicitFlagOverrides(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if got, err := resolveWebListen("127.0.0.1:9090", 8080); err != nil || got != "127.0.0.1:9090" {
		t.Fatalf("explicit host:port = %q, %v; want 127.0.0.1:9090", got, err)
	}
	if got, err := resolveWebListen("127.0.0.1", 8080); err != nil || got != "127.0.0.1:8080" {
		t.Fatalf("explicit host = %q, %v; want 127.0.0.1:8080", got, err)
	}
}

func TestEscapeWebDAVPath(t *testing.T) {
	if got := escapeWebDAVPath("/a b/한글.mp4"); got != "/a%20b/%ED%95%9C%EA%B8%80.mp4" {
		t.Fatalf("escaped = %q", got)
	}
	if got := escapeWebDAVPath("/"); got != "/" {
		t.Fatalf("root escaped = %q", got)
	}
}

func TestEnsureWebMountReusesRunningMount(t *testing.T) {
	client := &fakeWebClient{
		mountErr: errFakeWebMount,
		mountOut: daemon.MountResponse{Mounted: true},
	}
	if err := ensureWebMount(client); err != nil {
		t.Fatalf("ensureWebMount with running mount: %v", err)
	}
}

var errFakeWebMount = errWebTestSentinel()

func errWebTestSentinel() error { return os.ErrInvalid }

func TestResolveMountDAVEndpointSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := "dav:host=127.0.0.1,port=41234,ssl=false,prefix=%2Ftdrive-abc123"
	if err := os.Symlink(target, filepath.Join(os.Getenv("HOME"), "TDrive")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	hostPort, prefix, err := resolveMountDAVEndpoint()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if hostPort != "127.0.0.1:41234" || prefix != "/tdrive-abc123" {
		t.Fatalf("endpoint = %q %q", hostPort, prefix)
	}
}

func TestResolveMountDAVEndpointAbsoluteSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := "/run/user/1000/gvfs/dav:host=127.0.0.1,port=41234,ssl=false,prefix=%2Ftdrive-abc123"
	if err := os.Symlink(target, filepath.Join(os.Getenv("HOME"), "TDrive")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	hostPort, prefix, err := resolveMountDAVEndpoint()
	if err != nil {
		t.Fatalf("resolve absolute target: %v", err)
	}
	if hostPort != "127.0.0.1:41234" || prefix != "/tdrive-abc123" {
		t.Fatalf("endpoint = %q %q", hostPort, prefix)
	}
}

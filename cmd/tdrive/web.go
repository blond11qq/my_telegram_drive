package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"TDrive/backend/daemon"
)

// webDaemonClient is the daemon surface the web server needs. daemon.Client
// satisfies it; tests substitute a fake.
type webDaemonClient interface {
	MountStart(selector string, windowsDrive string, mode string) (daemon.MountResponse, error)
	MountStatus() (daemon.MountResponse, error)
	ListInDrive(driveID int64, path string) (daemon.ListResponse, error)
	FindInDrive(driveID int64, query string, limit int) (daemon.FindResponse, error)
	MkdirInDrive(driveID int64, path string, parents bool) (daemon.EntryResponse, error)
	RemoveInDrive(driveID int64, path string, recursive bool) (daemon.EntryResponse, error)
	MoveInDrive(driveID int64, source string, destination string) (daemon.EntryResponse, error)
	Status() (daemon.Status, error)
	ListDrives() (daemon.DriveListResponse, error)
	DownloadInDrive(driveID int64, remotePath string, localPath string, onEvent daemon.EventHandler) (daemon.DownloadResponse, error)
	UploadInDrive(driveID int64, localPath string, remotePath string, encrypt bool, extract bool, onEvent daemon.EventHandler) (daemon.UploadResponse, error)
}

type webOptions struct {
	port   int
	listen string
	token  string
	noAuth bool
}

type webServer struct {
	client  webDaemonClient
	resolve func() (hostPort string, capabilityPrefix string, err error)
	driveID int64
	ui      string

	tokenMu sync.RWMutex
	token   string

	uploadMu   sync.Mutex
	uploadJobs map[string]*webUploadJob
	uploadWork chan *webUploadJob
	uploadStop chan struct{}
	uploadOnce sync.Once
}

// webUploadQueueCap bounds queued stage-2 jobs. Stage 1 (browser -> server)
// is fast on a local tailnet while Telegram uploads drain one at a time, so
// a big multi-file drop needs headroom; payloads live in webTempDir (real
// disk), not in memory, so this is cheap.
const webUploadQueueCap = 256

// webUploadFile tracks one file through the two upload stages:
//  1. browser -> server (multipart receive into a temp file; the browser
//     reports this stage's progress itself via XHR upload events)
//  2. server -> Telegram (daemon UploadInDrive; progress arrives as daemon
//     upload_progress events and is polled by the browser)
type webUploadFile struct {
	Name     string        `json:"name"`
	Size     int64         `json:"size"`
	Stage    string        `json:"stage"`
	Progress float64       `json:"progress"`
	Error    string        `json:"error,omitempty"`
	Entry    *daemon.Entry `json:"entry,omitempty"`

	tempPath   string
	remotePath string
}

// webUploadJob is one POST /api/upload batch: files already received from the
// browser, waiting for (or undergoing) the server -> Telegram stage.
type webUploadJob struct {
	ID        string           `json:"id"`
	Dir       string           `json:"dir"`
	Encrypt   bool             `json:"-"`
	CreatedAt time.Time        `json:"-"`
	Files     []*webUploadFile `json:"files"`

	done chan struct{}
}

func runWeb(args []string) error {
	options, err := parseWebArgs(args)
	if err != nil {
		return err
	}
	client, err := newDaemonClient()
	if err != nil {
		return err
	}
	if err := ensureWebMount(client); err != nil {
		return err
	}
	token := ""
	if !options.noAuth {
		var err error
		token, err = resolveWebToken(options.token)
		if err != nil {
			return err
		}
	}
	listenAddr, err := resolveWebListen(options.listen, options.port)
	if err != nil {
		return err
	}
	server := &webServer{
		client:     client,
		token:      token,
		resolve:    resolveMountDAVEndpoint,
		ui:         webUIHTML,
		uploadJobs: make(map[string]*webUploadJob),
		uploadWork: make(chan *webUploadJob, webUploadQueueCap),
		uploadStop: make(chan struct{}),
	}
	cleanupWebUploadTemps()
	server.ensureUploadWorker()
	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           server.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", listenAddr, err)
	}
	fmt.Printf("TDrive web: %s\n", server.publicURL(listenAddr, token))
	if token == "" {
		fmt.Fprintln(os.Stderr, "WARNING: auth disabled (--no-auth). Anyone able to reach this address can browse, download, upload, and delete.")
	} else {
		fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop. Only share this URL with people you trust.")
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		close(server.uploadStop)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web: serve: %w", err)
	}
	return nil
}

func parseWebArgs(args []string) (webOptions, error) {
	const usage = "usage: tdrive web [--port N] [--listen ADDR] [--token TOKEN] [--no-auth]"
	options := webOptions{port: 8080}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--port":
			index++
			if index >= len(args) {
				return webOptions{}, fmt.Errorf("%s", usage)
			}
			port, err := strconv.Atoi(args[index])
			if err != nil || port <= 0 || port > 65535 {
				return webOptions{}, fmt.Errorf("invalid --port %q", args[index])
			}
			options.port = port
		case "--listen":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return webOptions{}, fmt.Errorf("%s", usage)
			}
			options.listen = strings.TrimSpace(args[index])
		case "--token":
			index++
			if index >= len(args) || args[index] == "" {
				return webOptions{}, fmt.Errorf("%s", usage)
			}
			options.token = args[index]
		case "--no-auth":
			options.noAuth = true
		default:
			return webOptions{}, fmt.Errorf("unknown web option %q\n\n%s", args[index], usage)
		}
	}
	return options, nil
}

// ensureWebMount starts the WebDAV mount the file proxy reads through. It is
// idempotent: an already-running mount is reused.
func ensureWebMount(client webDaemonClient) error {
	if out, err := client.MountStart("", "", ""); err == nil {
		if out.Mounted {
			return nil
		}
		return fmt.Errorf("web: mount did not start (phase %s)", out.Phase)
	} else if status, statusErr := client.MountStatus(); statusErr == nil && status.Mounted {
		return nil
	} else {
		return fmt.Errorf("web: mount start: %w", err)
	}
}

// resolveMountDAVEndpoint parses the ~/TDrive gvfs symlink target:
// dav:host=127.0.0.1,port=NNNN,ssl=false,prefix=%2Ftdrive-<hex>
func resolveMountDAVEndpoint() (hostPort string, capabilityPrefix string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	target, err := os.Readlink(filepath.Join(home, "TDrive"))
	if err != nil {
		return "", "", fmt.Errorf("web: ~/TDrive symlink not found (is the mount running?): %w", err)
	}
	rest, ok := strings.CutPrefix(target, "dav:")
	if !ok {
		// gvfs exposes the mount as an absolute path whose final component
		// is the dav:... spec, e.g.
		// /run/user/1000/gvfs/dav:host=127.0.0.1,port=NNNN,...,prefix=%2Ftdrive-<hex>
		if index := strings.Index(target, "dav:"); index >= 0 {
			rest, ok = strings.CutPrefix(target[index:], "dav:")
		}
	}
	if !ok {
		return "", "", fmt.Errorf("web: unexpected ~/TDrive target %q", target)
	}
	fields := strings.Split(rest, ",")
	var host, port, prefix string
	for _, field := range fields {
		key, value, _ := strings.Cut(field, "=")
		switch key {
		case "host":
			host = value
		case "port":
			port = value
		case "prefix":
			prefix = value
		}
	}
	if host == "" || port == "" || prefix == "" {
		return "", "", fmt.Errorf("web: cannot parse ~/TDrive target %q", target)
	}
	decoded, err := url.PathUnescape(prefix)
	if err != nil {
		return "", "", fmt.Errorf("web: cannot decode mount prefix: %w", err)
	}
	return net.JoinHostPort(host, port), decoded, nil
}

func resolveWebToken(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := strings.TrimSpace(os.Getenv("TDRIVE_WEB_TOKEN")); env != "" {
		return env, nil
	}
	path, err := webTokenPath()
	if err != nil {
		return "", err
	}
	if raw, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(raw)); token != "" {
			return token, nil
		}
	}
	token, err := randomWebToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func webTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "TDrive", "web.token"), nil
}

func randomWebToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("web: random token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func resolveWebListen(flag string, port int) (string, error) {
	if flag != "" {
		if _, _, err := net.SplitHostPort(flag); err == nil {
			return flag, nil
		}
		return net.JoinHostPort(flag, strconv.Itoa(port)), nil
	}
	if ip := tailscaleIPv4(); ip != "" {
		return net.JoinHostPort(ip, strconv.Itoa(port)), nil
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
}

func tailscaleIPv4() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", "ip", "-4").Output()
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if parsed := net.ParseIP(ip); parsed == nil || parsed.To4() == nil {
		return ""
	}
	return ip
}

// publicURL is the URL printed at startup and opened by the user.
func (s *webServer) publicURL(listenAddr, token string) string {
	if token == "" {
		return "http://" + listenAddr + "/"
	}
	return "http://" + listenAddr + "/?token=" + token
}

func (s *webServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleUI)
	mux.HandleFunc("/api/list", s.handleList)
	mux.HandleFunc("/api/find", s.handleFind)
	mux.HandleFunc("/api/mkdir", s.handleMkdir)
	mux.HandleFunc("/api/rm", s.handleRemove)
	mux.HandleFunc("/api/mv", s.handleMove)
	mux.HandleFunc("/api/cp", s.handleCopy)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/settings/token", s.handleTokenRotate)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/upload/status", s.handleUploadStatus)
	mux.HandleFunc("/file", s.handleFile)
	return s.requireToken(mux)
}

func (s *webServer) currentToken() string {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.token
}

func (s *webServer) setToken(token string) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	s.token = token
}

func (s *webServer) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := s.currentToken()
		if current == "" {
			next.ServeHTTP(response, request)
			return
		}
		if token := request.URL.Query().Get("token"); token != "" {
			if subtle.ConstantTimeCompare([]byte(token), []byte(current)) == 1 {
				http.SetCookie(response, &http.Cookie{
					Name:     "tdrive_web",
					Value:    current,
					Path:     "/",
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				})
			}
		}
		cookie, _ := request.Cookie("tdrive_web")
		authorized := (cookie != nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(current)) == 1) ||
			subtle.ConstantTimeCompare([]byte(request.URL.Query().Get("token")), []byte(current)) == 1
		if !authorized {
			writeWebError(response, http.StatusForbidden, "missing or invalid token")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *webServer) handleUI(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(response, s.ui)
}

func cleanWebPath(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("path must be absolute")
	}
	cleaned := path.Clean(raw)
	if cleaned == "." {
		return "/", nil
	}
	return cleaned, nil
}

func writeWebJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeWebError(response http.ResponseWriter, status int, message string) {
	writeWebJSON(response, status, map[string]any{"ok": false, "error": message})
}

func (s *webServer) handleList(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeWebError(response, http.StatusMethodNotAllowed, "GET only")
		return
	}
	remotePath, err := cleanWebPath(request.URL.Query().Get("path"))
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.client.ListInDrive(s.driveID, remotePath)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "path": out.Path, "entries": out.Entries})
}

func (s *webServer) handleFind(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeWebError(response, http.StatusMethodNotAllowed, "GET only")
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	if query == "" {
		writeWebError(response, http.StatusBadRequest, "query required")
		return
	}
	out, err := s.client.FindInDrive(s.driveID, query, 50)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "results": out.Results})
}

type webPathRequest struct {
	Path string `json:"path"`
}

func decodeWebPathRequest(request *http.Request, value *webPathRequest) error {
	defer request.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20)).Decode(value); err != nil {
		return fmt.Errorf("invalid JSON body")
	}
	cleaned, err := cleanWebPath(value.Path)
	if err != nil {
		return err
	}
	value.Path = cleaned
	return nil
}

func (s *webServer) handleMkdir(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeWebError(response, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body webPathRequest
	if err := decodeWebPathRequest(request, &body); err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.client.MkdirInDrive(s.driveID, body.Path, true)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "entry": out.Entry})
}

func (s *webServer) handleRemove(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeWebError(response, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	defer request.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20)).Decode(&body); err != nil {
		writeWebError(response, http.StatusBadRequest, "invalid JSON body")
		return
	}
	cleaned, err := cleanWebPath(body.Path)
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	if cleaned == "/" {
		writeWebError(response, http.StatusBadRequest, "refusing to remove root")
		return
	}
	out, err := s.client.RemoveInDrive(s.driveID, cleaned, body.Recursive)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "entry": out.Entry})
}

func (s *webServer) handleMove(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeWebError(response, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Source      string `json:"src"`
		Destination string `json:"dst"`
	}
	defer request.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20)).Decode(&body); err != nil {
		writeWebError(response, http.StatusBadRequest, "invalid JSON body")
		return
	}
	src, err := cleanWebPath(body.Source)
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	dst, err := cleanWebPath(body.Destination)
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.client.MoveInDrive(s.driveID, src, dst)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "entry": out.Entry})
}

func (s *webServer) handleCopy(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeWebError(response, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Source      string `json:"src"`
		Destination string `json:"dst"`
	}
	defer request.Body.Close()
	if err := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20)).Decode(&body); err != nil {
		writeWebError(response, http.StatusBadRequest, "invalid JSON body")
		return
	}
	src, err := cleanWebPath(body.Source)
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	dst, err := cleanWebPath(body.Destination)
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	if src == "/" || dst == "/" || src == dst {
		writeWebError(response, http.StatusBadRequest, "invalid copy source or destination")
		return
	}
	if strings.HasPrefix(dst, strings.TrimSuffix(src, "/")+"/") {
		writeWebError(response, http.StatusBadRequest, "cannot copy a folder into itself")
		return
	}
	entry, err := s.copyEntry(src, dst)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "entry": entry})
}

// copyEntry duplicates one entry server-side: files stream through a temp
// file (browser is not involved), folders recurse.
func (s *webServer) copyEntry(src, dst string) (daemon.Entry, error) {
	entry, err := s.webStat(src)
	if err != nil {
		return daemon.Entry{}, err
	}
	if entry.Type == "folder" {
		mkdirOut, err := s.client.MkdirInDrive(s.driveID, dst, false)
		if err != nil {
			return daemon.Entry{}, fmt.Errorf("%s: %w", path.Base(dst), err)
		}
		listed, err := s.client.ListInDrive(s.driveID, src)
		if err != nil {
			return daemon.Entry{}, err
		}
		for _, child := range listed.Entries {
			if _, err := s.copyEntry(child.Path, path.Join(dst, child.Name)); err != nil {
				return daemon.Entry{}, err
			}
		}
		return mkdirOut.Entry, nil
	}
	temp, err := os.CreateTemp(webTempDir(), "tdrive-web-copy-*")
	if err != nil {
		return daemon.Entry{}, err
	}
	tempPath := temp.Name()
	_ = temp.Close()
	_ = os.Remove(tempPath)
	if _, err := s.client.DownloadInDrive(s.driveID, src, tempPath, nil); err != nil {
		_ = os.Remove(tempPath)
		return daemon.Entry{}, err
	}
	out, err := s.client.UploadInDrive(s.driveID, tempPath, dst, true, false, nil)
	_ = os.Remove(tempPath)
	if err != nil {
		return daemon.Entry{}, fmt.Errorf("%s: %w", path.Base(dst), err)
	}
	return out.Entry, nil
}

// webStat resolves one remote path to its daemon entry via the parent listing.
func (s *webServer) webStat(remotePath string) (daemon.Entry, error) {
	parent := path.Dir(remotePath)
	want := path.Base(remotePath)
	listed, err := s.client.ListInDrive(s.driveID, parent)
	if err != nil {
		return daemon.Entry{}, err
	}
	for _, entry := range listed.Entries {
		if entry.Name == want {
			return entry, nil
		}
	}
	return daemon.Entry{}, fmt.Errorf("%s: not found", remotePath)
}

func (s *webServer) handleStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeWebError(response, http.StatusMethodNotAllowed, "GET only")
		return
	}
	status, err := s.client.Status()
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	driveTitle := ""
	if drives, err := s.client.ListDrives(); err == nil {
		for _, drive := range drives.Drives {
			if drive.ID == status.ActiveChannelID {
				driveTitle = drive.Title
				break
			}
		}
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "status": map[string]any{
		"drive_id":         status.ActiveChannelID,
		"drive_title":      driveTitle,
		"current_path":     status.CurrentPath,
		"vault_available":  status.VaultAvailable,
		"vault_configured": status.VaultConfigured,
		"vault_unlocked":   status.VaultUnlocked,
		"vault_hint":       status.VaultHint,
	}})
}

func (s *webServer) handleTokenRotate(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeWebError(response, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	defer request.Body.Close()
	_ = json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20)).Decode(&body)
	token := strings.TrimSpace(body.Token)
	if token == "" {
		var err error
		token, err = randomWebToken()
		if err != nil {
			writeWebError(response, http.StatusInternalServerError, err.Error())
			return
		}
	}
	tokenPath, err := webTokenPath()
	if err != nil {
		writeWebError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		writeWebError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		writeWebError(response, http.StatusInternalServerError, err.Error())
		return
	}
	s.setToken(token)
	http.SetCookie(response, &http.Cookie{
		Name:     "tdrive_web",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "token": token})
}

func (s *webServer) handleUpload(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeWebError(response, http.StatusMethodNotAllowed, "POST only")
		return
	}
	s.ensureUploadWorker()
	remoteDir, err := cleanWebPath(request.URL.Query().Get("path"))
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	reader, err := request.MultipartReader()
	if err != nil {
		writeWebError(response, http.StatusBadRequest, "multipart body required")
		return
	}
	// Stage 1 (browser -> server): stream every part into a temp file first.
	// The browser already shows this stage's progress via XHR upload events;
	// stage 2 (server -> Telegram) runs in the background worker and is
	// polled through /api/upload/status.
	job := &webUploadJob{Dir: remoteDir, CreatedAt: time.Now(), done: make(chan struct{})}
	// Web uploads are encrypted by default; only an explicit encrypt=0
	// stores a file unencrypted.
	job.Encrypt = request.URL.Query().Get("encrypt") != "0"
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			job.discardTemps()
			writeWebError(response, http.StatusBadRequest, err.Error())
			return
		}
		name := path.Base(part.FileName())
		if part.FileName() == "" || name == "" || name == "." || name == "/" {
			continue
		}
		temp, err := os.CreateTemp(webTempDir(), "tdrive-web-upload-*")
		if err != nil {
			job.discardTemps()
			writeWebError(response, http.StatusInternalServerError, err.Error())
			return
		}
		size, copyErr := io.Copy(temp, part)
		closeErr := temp.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(temp.Name())
			job.discardTemps()
			writeWebError(response, http.StatusInternalServerError, "saving upload failed")
			return
		}
		job.Files = append(job.Files, &webUploadFile{
			Name:       name,
			Size:       size,
			Stage:      "queued",
			tempPath:   temp.Name(),
			remotePath: path.Join(remoteDir, name),
		})
	}
	if len(job.Files) == 0 {
		writeWebError(response, http.StatusBadRequest, "no files received")
		return
	}
	job.ID = newWebUploadJobID()
	s.uploadMu.Lock()
	s.pruneUploadJobsLocked()
	s.uploadJobs[job.ID] = job
	s.uploadMu.Unlock()
	select {
	case s.uploadWork <- job:
	default:
		s.uploadMu.Lock()
		delete(s.uploadJobs, job.ID)
		s.uploadMu.Unlock()
		job.discardTemps()
		writeWebError(response, http.StatusServiceUnavailable, "upload queue is full, try again")
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "job": jobSnapshot(job)})
}

func (s *webServer) handleUploadStatus(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeWebError(response, http.StatusMethodNotAllowed, "GET only")
		return
	}
	id := strings.TrimSpace(request.URL.Query().Get("id"))
	if id == "" {
		writeWebError(response, http.StatusBadRequest, "job id required")
		return
	}
	s.uploadMu.Lock()
	job, ok := s.uploadJobs[id]
	s.uploadMu.Unlock()
	if !ok {
		writeWebError(response, http.StatusNotFound, "unknown upload job")
		return
	}
	writeWebJSON(response, http.StatusOK, map[string]any{"ok": true, "job": jobSnapshot(job)})
}

func newWebUploadJobID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(raw[:])
}

// pruneUploadJobsLocked drops finished jobs older than 30 minutes. Callers
// must hold uploadMu.
func (s *webServer) pruneUploadJobsLocked() {
	for id, job := range s.uploadJobs {
		select {
		case <-job.done:
			if time.Since(job.CreatedAt) > 30*time.Minute {
				delete(s.uploadJobs, id)
			}
		default:
		}
	}
}

func jobSnapshot(job *webUploadJob) map[string]any {
	files := make([]map[string]any, 0, len(job.Files))
	for _, file := range job.Files {
		files = append(files, map[string]any{
			"name":     file.Name,
			"size":     file.Size,
			"stage":    file.Stage,
			"progress": file.Progress,
			"error":    file.Error,
			"entry":    file.Entry,
		})
	}
	return map[string]any{"id": job.ID, "dir": job.Dir, "encrypt": job.Encrypt, "files": files}
}

func (job *webUploadJob) discardTemps() {
	for _, file := range job.Files {
		if file.tempPath != "" {
			_ = os.Remove(file.tempPath)
			file.tempPath = ""
		}
	}
}

// ensureUploadWorker starts the background stage-2 worker exactly once, so
// handlers (and tests, which never call runWeb) get one without ceremony.
func (s *webServer) ensureUploadWorker() {
	s.uploadOnce.Do(func() {
		if s.uploadJobs == nil {
			s.uploadJobs = make(map[string]*webUploadJob)
		}
		if s.uploadWork == nil {
			s.uploadWork = make(chan *webUploadJob, webUploadQueueCap)
		}
		if s.uploadStop == nil {
			s.uploadStop = make(chan struct{})
		}
		go s.uploadWorker()
	})
}

// daemon serializes streaming transfers itself, so a single worker also keeps
// progress events attributable to the right file.
// uploadWorker runs stage 2 (server -> Telegram) for one job at a time. The
// daemon serializes streaming transfers itself, so a single worker also keeps
// progress events attributable to the right file.
func (s *webServer) uploadWorker() {
	for {
		select {
		case <-s.uploadStop:
			return
		case job := <-s.uploadWork:
			s.processUploadJob(job)
		}
	}
}

func (s *webServer) processUploadJob(job *webUploadJob) {
	defer close(job.done)
	for _, file := range job.Files {
		s.uploadMu.Lock()
		file.Stage = "telegram"
		file.Progress = 0
		s.uploadMu.Unlock()
		out, err := s.client.UploadInDrive(s.driveID, file.tempPath, file.remotePath, job.Encrypt, false, func(event daemon.Event) {
			if event.Name != "upload_progress" || len(event.Args) < 2 {
				return
			}
			s.uploadMu.Lock()
			file.Progress = webEventPercent(event.Args[1])
			s.uploadMu.Unlock()
		})
		_ = os.Remove(file.tempPath)
		file.tempPath = ""
		s.uploadMu.Lock()
		if err != nil {
			file.Stage = "error"
			file.Error = err.Error()
		} else {
			entry := out.Entry
			file.Entry = &entry
			file.Stage = "done"
			file.Progress = 100
		}
		s.uploadMu.Unlock()
	}
}

func webEventPercent(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	default:
		return 0
	}
}

// webTempDir stores in-flight upload/copy payloads on real disk instead of
// /tmp, which is a small tmpfs here: a big multi-file batch would otherwise
// fill it before the Telegram stage drains it.
func webTempDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	dir := filepath.Join(home, ".cache", "TDrive", "web-tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.TempDir()
	}
	return dir
}

// cleanupWebUploadTemps removes temp files from uploads or copies
// interrupted by a previous server run, in both the current disk-backed dir
// and the legacy /tmp location; live jobs always remove their own temps.
func cleanupWebUploadTemps() {
	dirs := []string{webTempDir(), os.TempDir()}
	for _, pattern := range []string{"tdrive-web-upload-*", "tdrive-web-copy-*"} {
		for _, dir := range dirs {
			matches, err := filepath.Glob(filepath.Join(dir, pattern))
			if err != nil {
				continue
			}
			for _, match := range matches {
				_ = os.Remove(match)
			}
		}
	}
}

// handleFile proxies file bytes through the loopback WebDAV mount so Range
// requests stream with seeks instead of downloading the whole file first.
func (s *webServer) handleFile(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeWebError(response, http.StatusMethodNotAllowed, "GET only")
		return
	}
	remotePath, err := cleanWebPath(request.URL.Query().Get("path"))
	if err != nil {
		writeWebError(response, http.StatusBadRequest, err.Error())
		return
	}
	hostPort, prefix, err := s.resolve()
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	upstream := "http://" + hostPort + prefix + escapeWebDAVPath(remotePath)
	proxy, err := http.NewRequestWithContext(request.Context(), request.Method, upstream, nil)
	if err != nil {
		writeWebError(response, http.StatusInternalServerError, err.Error())
		return
	}
	proxy.Host = hostPort
	for _, header := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := request.Header.Get(header); value != "" {
			proxy.Header.Set(header, value)
		}
	}
	upstreamResponse, err := http.DefaultClient.Do(proxy)
	if err != nil {
		writeWebError(response, http.StatusBadGateway, err.Error())
		return
	}
	defer upstreamResponse.Body.Close()
	if upstreamResponse.StatusCode == http.StatusNotFound {
		// Uploads through the daemon API (web UI, CLI) land in the fresh
		// projection immediately but reach the mount's view a few seconds
		// later. If the daemon already lists the file, the 404 is that race:
		// wait briefly for the mount to catch up instead of failing.
		if s.waitForMountCatchUp(request.Context(), remotePath) {
			_ = upstreamResponse.Body.Close()
			retry, retryErr := http.DefaultClient.Do(proxy.Clone(request.Context()))
			if retryErr != nil {
				writeWebError(response, http.StatusBadGateway, retryErr.Error())
				return
			}
			defer retry.Body.Close()
			upstreamResponse = retry
		}
	}
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := upstreamResponse.Header.Get(header); value != "" {
			response.Header().Set(header, value)
		}
	}
	if request.URL.Query().Get("download") == "1" {
		response.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(path.Base(remotePath)))
	}
	response.WriteHeader(upstreamResponse.StatusCode)
	if request.Method != http.MethodHead {
		_, _ = io.Copy(response, upstreamResponse.Body)
	}
}

// waitForMountCatchUp reports whether remotePath shows up in the daemon's
// fresh listing but needed a moment to become visible through the mount.
// Only then is a retry of a 404 worthwhile; genuine misses return false fast.
func (s *webServer) waitForMountCatchUp(ctx context.Context, remotePath string) bool {
	parent := path.Dir(remotePath)
	want := path.Base(remotePath)
	for attempt := 0; attempt < 20; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		out, err := s.client.ListInDrive(s.driveID, parent)
		if err != nil {
			return false
		}
		found := false
		for _, entry := range out.Entries {
			if entry.Name == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
		hostPort, prefix, err := s.resolve()
		if err != nil {
			return false
		}
		head, err := http.NewRequestWithContext(ctx, http.MethodHead, "http://"+hostPort+prefix+escapeWebDAVPath(remotePath), nil)
		if err != nil {
			return false
		}
		head.Host = hostPort
		headResponse, err := http.DefaultClient.Do(head)
		if err == nil {
			_ = headResponse.Body.Close()
			if headResponse.StatusCode != http.StatusNotFound {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return false
}

// escapeWebDAVPath escapes each segment so spaces and non-ASCII names survive
// the proxy hop while slashes stay separators.
func escapeWebDAVPath(remotePath string) string {
	if remotePath == "/" {
		return "/"
	}
	parts := strings.Split(strings.Trim(remotePath, "/"), "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return "/" + strings.Join(parts, "/")
}

package mountdav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"TDrive/backend/mountfs"

	"TDrive/backend/media"

	"golang.org/x/net/webdav"
)

type readApplication struct {
	capabilityPath string
	authority      string
	fs             *FileSystem
	lockSystem     webdav.LockSystem
	writer         WriteCoordinator
	resume         *resumeStore
	pendingCreates *pendingCreateStore
}

func (application *readApplication) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet, http.MethodHead:
		application.serveFile(response, request)
	case "PROPFIND":
		application.servePropfind(response, request)
	case http.MethodPut:
		application.serveWritable(response, request, application.servePut)
	case "MKCOL":
		application.serveWritable(response, request, application.serveMkdir)
	case "MOVE":
		application.serveWritable(response, request, application.serveMove)
	case http.MethodDelete:
		application.serveWritable(response, request, application.serveDelete)
	case "PROPPATCH":
		application.serveWritable(response, request, application.serveProppatch)
	case "LOCK":
		application.serveWritable(response, request, application.serveLock)
	case "UNLOCK":
		application.serveWritable(response, request, application.serveUnlock)
	case "COPY":
		if application.writer == nil {
			writeHTTPError(response, http.StatusMethodNotAllowed)
			return
		}
		// Deliberate first-release policy: never emulate COPY as GET+PUT and
		// never report success for a capability the coordinator cannot commit.
		writeHTTPError(response, http.StatusNotImplemented)
	default:
		writeHTTPError(response, http.StatusMethodNotAllowed)
	}
}

func (application *readApplication) serveWritable(
	response http.ResponseWriter,
	request *http.Request,
	next func(http.ResponseWriter, *http.Request),
) {
	if application.writer == nil || application.lockSystem == nil {
		writeHTTPError(response, http.StatusMethodNotAllowed)
		return
	}
	next(response, request)
}

func (application *readApplication) servePropfind(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, application.capabilityPath)
	snapshot, err := preflightPropfind(
		request.Context(),
		application.fs,
		name,
		request.Header.Get("Depth") == "1",
	)
	if err != nil {
		serveFileError(response, err)
		return
	}
	lockSystem := application.lockSystem
	if lockSystem == nil {
		lockSystem = webdav.NewMemLS()
	}
	handler := &webdav.Handler{
		Prefix:     application.capabilityPath,
		FileSystem: snapshot,
		LockSystem: lockSystem,
	}
	root, err := snapshot.Stat(request.Context(), name)
	if err != nil {
		serveFileError(response, err)
		return
	}
	if root.IsDir() && !strings.HasSuffix(request.URL.Path, "/") {
		request = cloneRequestWithTrailingPathSlash(request)
	}
	omitRootHref := ""
	if propfindOmitsRoot(request) {
		omitRootHref = propfindResponseHref(application.capabilityPath, name, root.IsDir())
	}
	if application.writer == nil {
		serveReadOnlyPropfind(response, request, handler, omitRootHref)
		return
	}
	serveWritablePropfind(response, request, handler, omitRootHref)
}

func cloneRequestWithTrailingPathSlash(request *http.Request) *http.Request {
	normalized := request.Clone(request.Context())
	normalized.URL.Path += "/"
	if normalized.URL.RawPath != "" && !strings.HasSuffix(normalized.URL.RawPath, "/") {
		normalized.URL.RawPath += "/"
	}
	return normalized
}

func (application *readApplication) serveFile(response http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, application.capabilityPath)
	// ?original=1 asks for the pristine original bytes even when a streaming
	// proxy is ready. The web layer sets it for ?download=1 so downloads
	// never silently substitute the transcoded derivative.
	serveCtx := request.Context()
	if request.URL.Query().Get("original") == "1" {
		serveCtx = media.WithOriginalOnly(serveCtx)
	}
	clean, entry, err := application.fs.lookup(serveCtx, "open", name)
	if err != nil {
		serveFileError(response, err)
		return
	}
	info := newFileInfo(entry)
	if info.IsDir() {
		writeHTTPError(response, http.StatusMethodNotAllowed)
		return
	}
	if request.Method == http.MethodHead {
		if err := setFileHeaders(serveCtx, response.Header(), info); err != nil {
			serveFileError(response, err)
			return
		}
		http.ServeContent(response, request, info.Name(), info.ModTime(), &metadataReadSeeker{size: info.Size()})
		return
	}
	file, err := application.fs.openEntry(serveCtx, clean, entry)
	if err != nil {
		serveFileError(response, err)
		return
	}
	defer file.Close()
	// The opened content carries proxy-aware metadata (size, MP4 name and
	// type, distinct ETag) when it is the transcoded derivative; headers
	// must match the bytes ServeContent is about to stream.
	if stat, statErr := file.Stat(); statErr == nil {
		if proxyInfo, ok := stat.(fileInfo); ok {
			info = proxyInfo
		}
	}
	if err := setFileHeaders(serveCtx, response.Header(), info); err != nil {
		serveFileError(response, err)
		return
	}
	application.warmMediaTail(serveCtx, clean, entry, info)
	http.ServeContent(response, request, info.Name(), info.ModTime(), file)
}

// mediaTailWarmExtensions covers containers whose index (moov and friends)
// players typically fetch from the tail right after probing the head.
var mediaTailWarmExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".qt": true,
	".webm": true, ".mkv": true, ".mk3d": true, ".avi": true,
	".ts": true, ".m2ts": true, ".mts": true, ".flv": true,
	".wmv": true, ".ogv": true, ".mpeg": true, ".mpg": true,
	".mp3": true, ".m4a": true, ".aac": true, ".wav": true,
	".flac": true, ".oga": true, ".ogg": true, ".opus": true,
}

// mediaTailWarmBytes bounds the background tail fetch. Most container
// indexes fit, but moov-at-end files can exceed 4 MiB; over-fetching only
// costs background Telegram range reads.
const mediaTailWarmBytes = 16 * 1024 * 1024

// warmMediaTail prefetches the tail of a media file in the background while
// ServeContent streams the head the player asked for. Video players almost
// always jump to the tail next (moov atom), and without this that jump pays
// full cold Telegram round-trips before the first frame can show.
func (application *readApplication) warmMediaTail(ctx context.Context, clean string, entry mountfs.Entry, info fileInfo) {
	size := info.Size()
	if size <= mediaTailWarmBytes || entry.Kind != mountfs.KindFile {
		return
	}
	ext := strings.ToLower(filepath.Ext(info.entry.Name))
	if !mediaTailWarmExtensions[ext] {
		return
	}
	warmCtx := context.WithoutCancel(ctx)
	go func() {
		warm, err := application.fs.openEntry(warmCtx, clean, entry)
		if err != nil {
			return
		}
		defer warm.Close()
		if _, err := warm.Seek(size-mediaTailWarmBytes, io.SeekStart); err != nil {
			return
		}
		_, _ = io.CopyN(io.Discard, warm, mediaTailWarmBytes)
	}()
}

func setFileHeaders(ctx context.Context, header http.Header, info fileInfo) error {
	contentType, err := info.ContentType(ctx)
	if err != nil {
		return err
	}
	etag, err := info.ETag(ctx)
	if err != nil {
		return err
	}
	header.Set("Content-Type", contentType)
	header.Set("ETag", etag)
	return nil
}

type metadataReadSeeker struct {
	size   int64
	offset int64
}

func (content *metadataReadSeeker) Read(buffer []byte) (int, error) {
	if content.offset >= content.size {
		return 0, io.EOF
	}
	remaining := content.size - content.offset
	n := min(int64(len(buffer)), remaining)
	clear(buffer[:n])
	content.offset += n
	if content.offset == content.size {
		return int(n), io.EOF
	}
	return int(n), nil
}

func (content *metadataReadSeeker) Seek(offset int64, whence int) (int64, error) {
	next := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		next = content.offset + offset
	case io.SeekEnd:
		next = content.size + offset
	default:
		return 0, os.ErrInvalid
	}
	if next < 0 {
		return 0, os.ErrInvalid
	}
	content.offset = next
	return next, nil
}

func serveFileError(response http.ResponseWriter, err error) {
	status := fileErrorStatus(err)
	if status == http.StatusServiceUnavailable {
		response.Header().Set("Retry-After", serverBusyRetrySeconds)
	}
	writeHTTPError(response, status)
}

func fileErrorStatus(err error) int {
	status := http.StatusInternalServerError
	switch {
	case os.IsNotExist(err):
		status = http.StatusNotFound
	case errors.Is(err, os.ErrPermission), errors.Is(err, mountfs.ErrAccessDenied):
		status = http.StatusForbidden
	case errors.Is(err, mountfs.ErrContentUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, os.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
	}
	return status
}

package mountdav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"TDrive/backend/media"
	"TDrive/backend/mountfs"
)

// proxyTestContent serves proxy-sized bytes and identifies as the transcoded
// derivative, like a mountcontent proxy reader does.
type proxyTestContent struct {
	data []byte
}

func (c *proxyTestContent) ReadAt(ctx context.Context, buffer []byte, offset int64) (int, error) {
	if offset >= int64(len(c.data)) {
		return 0, io.EOF
	}
	n := copy(buffer, c.data[offset:])
	if n < len(buffer) {
		return n, io.EOF
	}
	return n, nil
}

func (c *proxyTestContent) Close() error { return nil }

func (c *proxyTestContent) Size() int64 {
	return int64(len(c.data))
}

func (c *proxyTestContent) ProxyPlayback() bool { return true }

// originalTestContent is the same bytes without the proxy marker, like a
// reader opened under an original-only context.
type originalTestContent struct {
	data []byte
}

func (c *originalTestContent) ReadAt(ctx context.Context, buffer []byte, offset int64) (int, error) {
	if offset >= int64(len(c.data)) {
		return 0, io.EOF
	}
	n := copy(buffer, c.data[offset:])
	if n < len(buffer) {
		return n, io.EOF
	}
	return n, nil
}

func (c *originalTestContent) Close() error { return nil }

type proxyTestOpener struct {
	proxy    *proxyTestContent
	original *originalTestContent
}

func (o *proxyTestOpener) OpenContent(ctx context.Context, channelID int64, entry mountfs.SourceEntry) (mountfs.RandomAccessContent, error) {
	if media.OriginalOnly(ctx) {
		return o.original, nil
	}
	return o.proxy, nil
}

func testProxyFS(t *testing.T) *readApplication {
	t.Helper()
	proxy := make([]byte, 100)
	for i := range proxy {
		proxy[i] = byte(i + 1)
	}
	original := make([]byte, 50)
	for i := range original {
		original[i] = byte(i + 101)
	}
	content := &proxyTestOpener{
		proxy:    &proxyTestContent{data: proxy},
		original: &originalTestContent{data: original},
	}
	modTime := time.Unix(1700000000, 0)
	mfs, err := mountfs.New(42, memorySource{
		mountfs.RootID: {
			{ID: "f:movie", ParentID: mountfs.RootID, Name: "movie.mkv", Kind: mountfs.KindFile, Size: int64(len(original)), ModTime: modTime, ContentRef: "42"},
		},
	}, content)
	if err != nil {
		t.Fatalf("mountfs.New: %v", err)
	}
	return &readApplication{fs: NewFileSystem(mfs)}
}

// A ready proxy must be served coherently: proxy bytes under proxy size, an
// MP4 name and type, and an ETag that can never match the original.
func TestServeFileServesReadyProxyCoherently(t *testing.T) {
	application := testProxyFS(t)
	request := httptest.NewRequest(http.MethodGet, "/movie.mkv", nil)
	recorder := httptest.NewRecorder()
	application.serveFile(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Length"); got != "100" {
		t.Fatalf("Content-Length = %q, want 100 (proxy size)", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type = %q, want video/mp4", got)
	}
	if etag := recorder.Header().Get("ETag"); !strings.Contains(etag, ".proxy") {
		t.Fatalf("ETag = %q, want a proxy marker", etag)
	}
	if recorder.Body.Len() != 100 || recorder.Body.Bytes()[0] != 1 {
		t.Fatalf("body len = %d, want 100 proxy bytes", recorder.Body.Len())
	}
}

// ?original=1 must serve the pristine original bytes with original metadata,
// which is what the web layer requests for ?download=1.
func TestServeFileOriginalQueryServesOriginal(t *testing.T) {
	application := testProxyFS(t)
	request := httptest.NewRequest(http.MethodGet, "/movie.mkv?original=1", nil)
	recorder := httptest.NewRecorder()
	application.serveFile(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", recorder.Code)
	}
	// The ctx-aware fake serves original bytes here, but metadata still
	// follows the opened content: assert the original bytes arrived.
	if recorder.Body.Len() != 50 || recorder.Body.Bytes()[0] != 101 {
		t.Fatalf("body len = %d, want 50 original bytes", recorder.Body.Len())
	}
}

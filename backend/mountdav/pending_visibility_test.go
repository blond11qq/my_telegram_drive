package mountdav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const pendingVisibilityPropfindBody = `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:allprop/></D:propfind>`

func doPendingVisibilityPropfind(t *testing.T, handler http.Handler, target, depth string) *httptest.ResponseRecorder {
	t.Helper()
	request := trustedRequest("PROPFIND", testCapability+target, strings.NewReader(pendingVisibilityPropfindBody))
	request.Header.Set("Depth", depth)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// TestServePROPFINDSeesDeferredEmptyCreate locks in the exact Finder sequence
// that produced fnfErr (-43): macOS PUTs a zero-byte placeholder (answered 201
// without committing yet), then immediately PROPFINDs the same path to verify
// the create. The deferred create must be visible as a zero-byte file, both on
// the path itself and in its parent's listing.
func TestServePROPFINDSeesDeferredEmptyCreate(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	pendingCreates := newTestPendingCreateStore(t, time.Minute, time.Now)
	handler := newWritableTestHandlerWithPendingCreates(t, writer, pendingCreates)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty placeholder PUT status = %d, want 201", code)
	}

	recorder := doPendingVisibilityPropfind(t, handler, "/Docs/photo.png", "0")
	if recorder.Code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND of deferred create status = %d, want 207 (body %q)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "photo.png") {
		t.Fatalf("PROPFIND of deferred create does not list the placeholder: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "404") {
		t.Fatalf("PROPFIND of deferred create reported not-found: %s", recorder.Body.String())
	}

	recorder = doPendingVisibilityPropfind(t, handler, "/Docs/", "1")
	if recorder.Code != http.StatusMultiStatus {
		t.Fatalf("parent PROPFIND status = %d, want 207 (body %q)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "photo.png") {
		t.Fatalf("parent listing omits the deferred create: %s", recorder.Body.String())
	}
}

// TestServeGETReturnsEmptyPlaceholderForDeferredCreate: if the client reads
// the placeholder back during the grace window it must see the promised
// zero-byte resource, not a 404.
func TestServeGETReturnsEmptyPlaceholderForDeferredCreate(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	pendingCreates := newTestPendingCreateStore(t, time.Minute, time.Now)
	handler := newWritableTestHandlerWithPendingCreates(t, writer, pendingCreates)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty placeholder PUT status = %d, want 201", code)
	}

	request := trustedRequest(http.MethodGet, testCapability+"/Docs/photo.png", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET of deferred create status = %d, want 200 (body %q)", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("GET of deferred create body = %q, want empty", recorder.Body.String())
	}
}

// TestServeDELETEOfDeferredCreateSucceedsWithoutCoordinator: cancelling the
// deferred create fully satisfies the delete, so the coordinator (which would
// resolve a not-found and 404) must never run. The client saw a successful
// create for this path, so its cleanup delete must succeed with 204.
func TestServeDELETEOfDeferredCreateSucceedsWithoutCoordinator(t *testing.T) {
	writer := &recordingWriteCoordinator{}
	pendingCreates := newTestPendingCreateStore(t, time.Minute, time.Now)
	handler := newWritableTestHandlerWithPendingCreates(t, writer, pendingCreates)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty placeholder PUT status = %d, want 201", code)
	}

	request := trustedRequest(http.MethodDelete, testCapability+"/Docs/photo.png", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DELETE of deferred create status = %d, want 204", recorder.Code)
	}
	if writer.deleteRequest.Path != "" {
		t.Fatalf("coordinator DELETE invoked for a deferred create: %+v", writer.deleteRequest)
	}

	pendingCreates.reapDue(context.Background())
	if writer.putRequest.Path != "" {
		t.Fatalf("deferred empty create resurrected after DELETE: %+v", writer.putRequest)
	}
}

// TestServePUTContentIgnoresPlaceholderETagPrecondition: after PROPFINDing the
// placeholder, macOS may echo its synthetic ETag back as If-Match on the real
// content PUT. The durable resource still does not exist at that point, so the
// coordinator would reject with 412 -- mountdav must reconcile the condition
// (the ETag could only ever refer to the placeholder it just advertised).
func TestServePUTContentIgnoresPlaceholderETagPrecondition(t *testing.T) {
	writer := &recordingWriteCoordinator{putResult: MutationResult{Created: true}}
	pendingCreates := newTestPendingCreateStore(t, time.Minute, time.Now)
	handler := newWritableTestHandlerWithPendingCreates(t, writer, pendingCreates)

	if code := putEmpty(t, handler, "/Docs/photo.png"); code != http.StatusCreated {
		t.Fatalf("empty placeholder PUT status = %d, want 201", code)
	}

	etag, err := ResourceETag(context.Background(), 0, pendingEntryID("/Docs/photo.png"), 0, "")
	if err != nil {
		t.Fatalf("ResourceETag for placeholder: %v", err)
	}
	request := trustedRequest(http.MethodPut, testCapability+"/Docs/photo.png", strings.NewReader("real content"))
	request.Header.Set("If-Match", etag)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("content PUT status = %d, want 201 (body %q)", recorder.Code, recorder.Body.String())
	}
	if writer.putRequest.Conditions.IfMatch.Present {
		t.Fatalf("If-Match on a deferred-create target reached the coordinator: %+v", writer.putRequest.Conditions)
	}
	if string(writer.putBody) != "real content" {
		t.Fatalf("coordinator received %q, want the real content", writer.putBody)
	}
}

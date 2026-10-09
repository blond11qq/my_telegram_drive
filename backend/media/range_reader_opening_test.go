package media

import (
	"bytes"
	"context"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

// A player reads a small header before it can show anything. Making that read
// wait for a whole megabyte block is most of a video's startup delay on a slow
// link, so the opening of a file is served from a short prefix instead.
func TestRangeReaderServesOpeningPrefixBeforeFullBlock(t *testing.T) {
	data := testBytes(tgclient.RangeReadMaxBytes * 2)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()
	ref := fake.ref()

	buf := make([]byte, 64)
	if _, err := reader.ReadStoredAt(context.Background(), ref, buf, 128); err != nil {
		t.Fatalf("ReadStoredAt: %v", err)
	}
	if !bytes.Equal(buf, data[128:192]) {
		t.Fatal("opening read returned the wrong bytes")
	}

	calls := fake.calls()
	if len(calls) == 0 {
		t.Fatal("no range call was issued")
	}
	if calls[0].offset != 0 || int64(calls[0].length) != openingChunkBytes {
		t.Fatalf("first call = %+v, want the %d-byte opening prefix", calls[0], openingChunkBytes)
	}

	// The rest of the block follows behind, so the next read is already warm.
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, call := range fake.calls() {
			if call.offset == 0 && call.length == tgclient.RangeReadMaxBytes {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("calls = %+v, want the full block fetched behind the prefix", fake.calls())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The opening prefix must stay strictly inside the first 1 MiB block.
// Window, seek, and tail tests read at openingChunkBytes to mean "past the
// opening window but still in block 0", and a prefix equal to a whole block
// would duplicate that block under a second cache key instead of cutting
// startup latency.
func TestOpeningChunkStaysWithinFirstBlock(t *testing.T) {
	block := int64(tgclient.RangeReadMaxBytes)
	if openingChunkBytes <= 0 || openingChunkBytes >= block {
		t.Fatalf("openingChunkBytes = %d, want in (0, %d)", openingChunkBytes, block)
	}
	if openingChunkBytes%int64(tgclient.RangeReadAlignment) != 0 {
		t.Fatalf("openingChunkBytes = %d, want %d-byte alignment", openingChunkBytes, tgclient.RangeReadAlignment)
	}
}

// A file smaller than the opening window is already a single short block, so
// splitting it would only add a request.
func TestRangeReaderSkipsOpeningPrefixForSmallFiles(t *testing.T) {
	data := testBytes(int(openingChunkBytes) / 2)
	fake := newStrictRangeFake(data)
	reader := NewRangeReader(RangeReaderConfig{Client: fake})
	defer reader.Close()

	if _, err := reader.ReadStoredAt(context.Background(), fake.ref(), make([]byte, 64), 0); err != nil {
		t.Fatalf("ReadStoredAt: %v", err)
	}
	calls := fake.calls()
	if len(calls) != 1 || calls[0].length != len(data) {
		t.Fatalf("calls = %+v, want one fetch of the whole file", calls)
	}
}

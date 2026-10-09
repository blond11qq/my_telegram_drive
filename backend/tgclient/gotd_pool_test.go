package tgclient

import (
	"errors"
	"testing"

	"github.com/gotd/td/tgerr"
)

func TestIsFileMigrateRecognizesOnlyTheRedirect(t *testing.T) {
	t.Parallel()

	if !isFileMigrate(tgerr.New(303, "FILE_MIGRATE_4")) {
		t.Fatal("FILE_MIGRATE_4 was not recognized as a redirect")
	}
	for _, err := range []error{
		tgerr.New(420, "FLOOD_WAIT_30"),
		tgerr.New(400, "FILE_REFERENCE_EXPIRED"),
		errors.New("engine forcibly closed"),
		nil,
	} {
		if isFileMigrate(err) {
			t.Fatalf("%v was mistaken for a redirect", err)
		}
	}
}

// The limiter budget partitions Telegram pressure: sixteen global getFile
// slots over eight pooled connections, twelve for background work (exactly
// two downloads wide) and four reserved for foreground playback, with total
// pooled connections well under twenty.
func TestGetFileBudgetTracksPoolSize(t *testing.T) {
	t.Parallel()

	if MediaPoolSize != 8 || MediaPoolSize >= 20 {
		t.Fatalf("pool = %d, want 8 connections and fewer than 20", MediaPoolSize)
	}
	if MaxConcurrentGetFile != 16 || PlaybackGetFileReserve != 4 {
		t.Fatalf("budget = %d/%d, want 16 total with a 4-slot playback reserve",
			MaxConcurrentGetFile, PlaybackGetFileReserve)
	}
	if DefaultDownloadThreads != 6 {
		t.Fatalf("download threads = %d, want 6 so two downloads fill the background pool",
			DefaultDownloadThreads)
	}
	if MaxConcurrentBackgroundGetFile != 2*DefaultDownloadThreads {
		t.Fatalf("background pool = %d, want two downloads of %d threads",
			MaxConcurrentBackgroundGetFile, DefaultDownloadThreads)
	}
}

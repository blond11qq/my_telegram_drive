package projection

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
)

func testNameKey(t *testing.T) []byte {
	t.Helper()
	return bytes.Repeat([]byte{21}, 32)
}

func TestSealedWireRoundTrip(t *testing.T) {
	key := testNameKey(t)
	op := Op{Type: OpFileUpload, Parent: RootParent, Name: "secret.mp4", FileSize: 10}
	if err := SealOpName(&op, "secret.mp4", key); err != nil {
		t.Fatalf("seal: %v", err)
	}
	header := Format(op)
	if strings.Contains(header, "secret.mp4") {
		t.Fatalf("plaintext name on the wire: %q", header)
	}
	for _, want := range []string{"|n=_|", "|nenc=v1:", "|enc=1|ev=1|kid=1"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header %q misses %q", header, want)
		}
	}
	parsed, err := Parse(header)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Name != "_" || parsed.NameEnc == "" || parsed.NameKeyVersion != 1 {
		t.Fatalf("parsed = %+v", parsed)
	}
	// In-memory op keeps plaintext; the envelope rides alongside.
	if op.Name != "secret.mp4" || op.NameEnc == "" {
		t.Fatalf("sealed op = %+v", op)
	}
}

func TestSealedWireRejectsBadEnvelope(t *testing.T) {
	base := "TDX1|t=f|p=|n=_|nenc=XXX|enc=1|ev=1|kid=1"
	if _, err := Parse(base); err == nil {
		t.Fatal("bad envelope version accepted")
	}
	// The kid tag is required on sealed captions.
	noKid := "TDX1|t=f|p=|n=_|nenc=v1:AAAA|enc=1|ev=1"
	if _, err := Parse(noKid); err == nil {
		t.Fatal("missing kid accepted")
	}
	badKid := "TDX1|t=f|p=|n=_|nenc=v1:AAAA|enc=1|ev=1|kid=x"
	if _, err := Parse(badKid); err == nil {
		t.Fatal("malformed kid accepted")
	}
}

func TestLegacyRealNameParsesWithoutEnvelope(t *testing.T) {
	parsed, err := Parse("TDX1|t=f|p=|n=movie.mp4|sz=10")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Name != "movie.mp4" || parsed.NameEnc != "" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestResolveOpNameUnlocked(t *testing.T) {
	key := testNameKey(t)
	NameKeyProvider = func(channelID int64) ([]byte, error) {
		return append([]byte(nil), key...), nil
	}
	defer func() { NameKeyProvider = nil }()
	op := Op{Type: OpFileUpload, Name: "_"}
	if err := SealOpName(&op, "secret.mp4", key); err != nil {
		t.Fatal(err)
	}
	op.Name = "_"
	queued, err := ResolveOpName(42, 7, &op)
	if err != nil || queued {
		t.Fatalf("queued=%v err=%v", queued, err)
	}
	if op.Name != "secret.mp4" {
		t.Fatalf("name = %q", op.Name)
	}
}

func TestResolveOpNameLockedPlaceholders(t *testing.T) {
	NameKeyProvider = nil
	defer func() { NameKeyProvider = nil }()
	op := Op{Type: OpFileUpload, Name: "_", NameEnc: "v1:AAAA"}
	queued, err := ResolveOpName(42, 7, &op)
	if err != nil || !queued {
		t.Fatalf("queued=%v err=%v, want placeholder", queued, err)
	}
	if op.Name != LockedNamePlaceholder(7) {
		t.Fatalf("name = %q", op.Name)
	}
	if _, err := CanonicalNameKey(op.Name); err != nil {
		t.Fatalf("placeholder fails validation: %v", err)
	}
	// Legacy ops pass through untouched, provider or not.
	plain := Op{Type: OpFileUpload, Name: "movie.mp4"}
	if queued, err := ResolveOpName(42, 7, &plain); err != nil || queued || plain.Name != "movie.mp4" {
		t.Fatalf("legacy op touched: %+v %v", plain, err)
	}
}

func openHiddenNameTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := MigratePersonalChannel(db, 42); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestLockedSweepResolvesAfterUnlock(t *testing.T) {
	db := openHiddenNameTestDB(t)
	key := testNameKey(t)

	// Sync while locked: placeholder projects, envelope queues.
	NameKeyProvider = nil
	sealed, err := sealedUploadHeader("hidden.mp4", key)
	if err != nil {
		t.Fatal(err)
	}
	op, err := Parse(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectFromOp(db, 42, 50, op, 7, sealed); err != nil {
		t.Fatalf("project locked: %v", err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM files WHERE channel_id=42 AND msg_id=50`).Scan(&name); err != nil {
		t.Fatalf("files row: %v", err)
	}
	if name != LockedNamePlaceholder(50) {
		t.Fatalf("name = %q, want placeholder", name)
	}
	var queued int
	if err := db.QueryRow(`SELECT COUNT(*) FROM hidden_name_queue WHERE channel_id=42 AND msg_id=50`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued = %d, %v", queued, err)
	}

	// Unlock: the sweep restores the real name everywhere.
	NameKeyProvider = func(channelID int64) ([]byte, error) {
		return append([]byte(nil), key...), nil
	}
	defer func() { NameKeyProvider = nil }()
	n, err := ResolveLockedNames(db, func(channelID int64) ([]byte, error) {
		return append([]byte(nil), key...), nil
	})
	if err != nil || n != 1 {
		t.Fatalf("resolved = %d, %v", n, err)
	}
	if err := db.QueryRow(`SELECT name FROM files WHERE channel_id=42 AND msg_id=50`).Scan(&name); err != nil || name != "hidden.mp4" {
		t.Fatalf("name = %q, %v", name, err)
	}
	if err := db.QueryRow(`SELECT display_name FROM dirents WHERE channel_id=42 AND object_id='f:50'`).Scan(&name); err != nil || name != "hidden.mp4" {
		t.Fatalf("dirent = %q, %v", name, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM hidden_name_queue`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("queue = %d, %v, want drained", queued, err)
	}
}

func sealedUploadHeader(name string, key []byte) (string, error) {
	op := Op{Type: OpFileUpload, Parent: RootParent, Name: name, FileSize: 10}
	if err := SealOpName(&op, name, key); err != nil {
		return "", err
	}
	return Format(op), nil
}

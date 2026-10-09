package crypto

import (
	"bytes"
	"strings"
	"testing"
)

func testNameKey(t *testing.T) []byte {
	t.Helper()
	return bytes.Repeat([]byte{11}, 32)
}

func TestFileNameRoundTrip(t *testing.T) {
	key := testNameKey(t)
	for _, name := range []string{"movie.mkv", "한글 파일 이름.mp4", "a", strings.Repeat("x", 200)} {
		env, err := EncryptFileName(key, name)
		if err != nil {
			t.Fatalf("seal %q: %v", name, err)
		}
		if !strings.HasPrefix(env, "v1:") {
			t.Fatalf("envelope = %q, want v1: prefix", env)
		}
		got, err := DecryptFileName(key, env)
		if err != nil || got != name {
			t.Fatalf("round trip %q = %q, %v", name, got, err)
		}
	}
}

func TestFileNameSealIsRandomized(t *testing.T) {
	key := testNameKey(t)
	first, err := EncryptFileName(key, "same.mp4")
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncryptFileName(key, "same.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two seals of one name match; nonce reuse suspected")
	}
	for _, sealed := range []string{first, second} {
		if got, err := DecryptFileName(key, sealed); err != nil || got != "same.mp4" {
			t.Fatalf("open = %q, %v", got, err)
		}
	}
}

func TestFileNameDecryptRejects(t *testing.T) {
	key := testNameKey(t)
	other := bytes.Repeat([]byte{12}, 32)
	env, err := EncryptFileName(key, "movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	otherEnv, err := EncryptFileName(other, "movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	for name, envelope := range map[string]string{
		"wrong key":     otherEnv,
		"bad version":   "v9:" + strings.TrimPrefix(env, "v1:"),
		"truncated":     env[:10],
		"not envelope":  "plain-name.mp4",
		"empty payload": "v1:",
	} {
		if _, err := DecryptFileName(key, envelope); err == nil {
			t.Fatalf("%s accepted, want rejection", name)
		}
	}
}

func TestFileNameSealValidates(t *testing.T) {
	key := testNameKey(t)
	for _, name := range []string{"", string([]byte{0xff, 0xfe})} {
		if _, err := EncryptFileName(key, name); err == nil {
			t.Fatalf("seal %q accepted, want rejection", name)
		}
	}
	if _, err := EncryptFileName([]byte("short"), "a.mp4"); err == nil {
		t.Fatal("short key accepted, want rejection")
	}
}

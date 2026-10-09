package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/chacha20poly1305"
)

// NameKeyVersion tags the vault key that sealed a hidden filename. It is
// carried on the wire as kid= so a future rotation can keep old envelopes
// readable: a decryptor holding several key generations picks by tag.
const NameKeyVersion = 1

const (
	nameEnvelopeVersion = "v1"
	nameNonceLen        = chacha20poly1305.NonceSizeX
	maxFileNameBytes    = 4096
)

// EncryptFileName seals a filename with the vault master key for encrypted
// drives, returning the wire envelope "v1:<base64(nonce||ciphertext)>".
// XChaCha20-Poly1305 takes a fresh 24-byte random nonce on every call, so
// two seals of the same name never match: identical names, renames of one
// file, and sibling files are unlinkable on the wire.
//
// Length note: the ciphertext is len(name)+16 tag bytes, so the envelope
// reveals the approximate plaintext length. That is accepted deliberately:
// fixed-block padding would bloat every caption for a marginal gain, and
// the threat model here is casual channel browsing, not traffic analysis.
// Revisit if names ever carry more than identifiers.
func EncryptFileName(masterKey []byte, name string) (string, error) {
	if len(masterKey) != chacha20poly1305.KeySize {
		return "", ErrInvalidMasterKey
	}
	if !utf8.ValidString(name) || name == "" || len(name) > maxFileNameBytes {
		return "", fmt.Errorf("crypto: invalid filename for sealing")
	}
	var nonce [nameNonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("crypto: seal filename nonce: %w", err)
	}
	subkey, err := deriveSubkey(masterKey, nameSealSalt())
	if err != nil {
		return "", err
	}
	defer clear(subkey)
	aead, err := chacha20poly1305.NewX(subkey)
	if err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce[:], nonce[:], []byte(name), []byte(nameEnvelopeVersion))
	defer clear(sealed)
	return nameEnvelopeVersion + ":" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// DecryptFileName opens a filename envelope sealed by EncryptFileName. It
// rejects unknown envelope versions (a newer writer's names stay opaque
// rather than decoding as garbage) and empty results.
func DecryptFileName(masterKey []byte, envelope string) (string, error) {
	if len(masterKey) != chacha20poly1305.KeySize {
		return "", ErrInvalidMasterKey
	}
	version, rest, ok := strings.Cut(envelope, ":")
	if !ok || version != nameEnvelopeVersion {
		return "", fmt.Errorf("crypto: unknown filename envelope version")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(sealed) <= nameNonceLen {
		return "", fmt.Errorf("crypto: malformed filename envelope")
	}
	subkey, err := deriveSubkey(masterKey, nameSealSalt())
	if err != nil {
		return "", err
	}
	defer clear(subkey)
	aead, err := chacha20poly1305.NewX(subkey)
	if err != nil {
		return "", err
	}
	plain, err := aead.Open(nil, sealed[:nameNonceLen], sealed[nameNonceLen:], []byte(nameEnvelopeVersion))
	if err != nil {
		return "", fmt.Errorf("crypto: filename envelope authentication failed")
	}
	defer clear(plain)
	if !utf8.ValidString(string(plain)) || len(plain) == 0 || len(plain) > maxFileNameBytes {
		return "", fmt.Errorf("crypto: invalid sealed filename")
	}
	return string(plain), nil
}

// nameSealSalt domain-separates filename seals from stream chunk subkeys.
// Both derive from the same master key via HKDF, so the salt is what keeps
// a stream chunk key from ever doubling as a filename key.
func nameSealSalt() []byte {
	return []byte("tdrive.name-seal.v1")
}

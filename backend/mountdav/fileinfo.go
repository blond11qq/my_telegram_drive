package mountdav

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"TDrive/backend/mountfs"
)

type fileInfo struct {
	entry mountfs.Entry
	// proxySize is non-negative when the opened content is the ready
	// transcoded streaming derivative rather than the projected original.
	// Size, name, type and ETag then describe the proxy so players receive
	// a coherent MP4 response instead of proxy bytes under original metadata.
	proxySize int64
	proxy     bool
}

const resourceETagDomain = "tdrive.mount.resource-etag.v1\x00"

func newFileInfo(entry mountfs.Entry) fileInfo {
	return fileInfo{entry: entry, proxySize: -1}
}

// asProxy re-targets metadata at the transcoded derivative: its own size, an
// MP4 name and type (the proxy is always faststart MP4), and a distinct ETag
// so caches never mix proxy and original bytes under one entity tag.
func (info fileInfo) asProxy(size int64) fileInfo {
	info.proxy = true
	info.proxySize = size
	return info
}

func (info fileInfo) Name() string {
	if info.entry.ID == mountfs.RootID && info.entry.Name == "" {
		return "."
	}
	if info.proxy {
		if ext := filepath.Ext(info.entry.Name); ext != "" {
			return strings.TrimSuffix(info.entry.Name, ext) + ".mp4"
		}
		return info.entry.Name + ".mp4"
	}
	return info.entry.Name
}

func (info fileInfo) Size() int64 {
	if info.IsDir() {
		return 0
	}
	if info.proxy {
		return info.proxySize
	}
	return info.entry.Size
}

func (info fileInfo) Mode() os.FileMode {
	if info.IsDir() {
		return os.ModeDir | 0o555
	}
	return 0o444
}

func (info fileInfo) ModTime() time.Time {
	if info.entry.ModTime.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return info.entry.ModTime
}

func (info fileInfo) IsDir() bool {
	return info.entry.Kind == mountfs.KindDirectory
}

func (info fileInfo) Sys() any {
	return nil
}

func (info fileInfo) ContentType(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if info.IsDir() {
		return "httpd/unix-directory", nil
	}
	if info.proxy {
		return "video/mp4", nil
	}
	if contentType := mime.TypeByExtension(filepath.Ext(info.entry.Name)); contentType != "" {
		return contentType, nil
	}
	return "application/octet-stream", nil
}

func (info fileInfo) ETag(ctx context.Context) (string, error) {
	etag, err := EntryETag(ctx, info.entry)
	if err != nil {
		return "", err
	}
	if info.proxy && strings.HasSuffix(etag, `"`) {
		return strings.TrimSuffix(etag, `"`) + `.proxy"`, nil
	}
	return etag, nil
}

// EntryETag returns the strong entity tag used by WebDAV responses for one
// projected mount entry. Writable adapters use the same function when checking
// OS-provided preconditions before building a mutation.
func EntryETag(ctx context.Context, entry mountfs.Entry) (string, error) {
	return ResourceETag(ctx, entry.ChannelID, entry.ID, entry.Revision, entry.ContentHash)
}

// ResourceETag returns a domain-separated strong entity tag from the stable
// projection identity of a mounted resource. It intentionally excludes private
// Telegram references and mutable display metadata; revision is authoritative.
func ResourceETag(ctx context.Context, channelID int64, objectID string, revision int64, contentHash string) (string, error) {
	if ctx == nil {
		return "", errors.New("mountdav: nil context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte(resourceETagDomain))
	writeETagInt64(digest, channelID)
	writeETagField(digest, objectID)
	writeETagInt64(digest, revision)
	writeETagField(digest, contentHash)
	return `"tdrive-` + hex.EncodeToString(digest.Sum(nil)) + `"`, nil
}

func writeETagInt64(digest hash.Hash, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = digest.Write(encoded[:])
}

func writeETagField(digest hash.Hash, value string) {
	writeETagInt64(digest, int64(len(value)))
	_, _ = digest.Write([]byte(value))
}

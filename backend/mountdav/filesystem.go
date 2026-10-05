package mountdav

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"TDrive/backend/mountfs"

	"golang.org/x/net/webdav"
)

// FileSystem adapts a protocol-neutral mountfs.FS to x/net/webdav's
// filesystem contract. Every mutating operation is denied unconditionally.
type FileSystem struct {
	fs      mountfs.ReadFilesystem
	pending *pendingCreateStore
}

func NewFileSystem(fs mountfs.ReadFilesystem) *FileSystem {
	return &FileSystem{fs: fs}
}

// SetPendingCreates wires the deferred empty-create store into the read paths.
// A deferred create answers its originating PUT with success before the file
// is durably committed, so for the remainder of the grace window readers must
// still observe it: without this overlay macOS Finder's immediate PROPFIND
// after its placeholder PUT sees a 404 and aborts the whole copy with
// fnfErr (-43). Passing nil disables the overlay (read-only servers).
func (fs *FileSystem) SetPendingCreates(store *pendingCreateStore) {
	fs.pending = store
}

// pendingEntryIDPrefix namespaces synthetic placeholder identities so they can
// never collide with durable Telegram object IDs.
const pendingEntryIDPrefix = "pending-create:"

func pendingEntryID(clean string) string {
	return pendingEntryIDPrefix + clean
}

func isPendingEntryID(id string) bool {
	return strings.HasPrefix(id, pendingEntryIDPrefix)
}

// pendingEntry projects a still-deferred empty create as a zero-byte regular
// file at clean, honouring the visibility contract of the successful PUT that
// armed it. Returns false when no pending entry covers the path.
func (fs *FileSystem) pendingEntry(clean string) (mountfs.Entry, bool) {
	if fs.pending == nil {
		return mountfs.Entry{}, false
	}
	armedAt, ok := fs.pending.lookup(clean)
	if !ok {
		return mountfs.Entry{}, false
	}
	name := clean
	if index := strings.LastIndexByte(clean, '/'); index >= 0 {
		name = clean[index+1:]
	}
	return mountfs.Entry{
		ID:       pendingEntryID(clean),
		ParentID: path.Dir(clean),
		Name:     name,
		Kind:     mountfs.KindFile,
		ModTime:  armedAt,
	}, true
}

func (*FileSystem) Mkdir(context.Context, string, os.FileMode) error {
	return os.ErrPermission
}

func (*FileSystem) RemoveAll(context.Context, string) error {
	return os.ErrPermission
}

func (*FileSystem) Rename(context.Context, string, string) error {
	return os.ErrPermission
}

func (fs *FileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	_, entry, err := fs.lookup(ctx, "stat", name)
	if err != nil {
		return nil, err
	}
	return newFileInfo(entry), nil
}

func (fs *FileSystem) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if !readOnlyFlags(flag) {
		return nil, pathError("open", name, os.ErrPermission)
	}
	clean, entry, err := fs.lookup(ctx, "open", name)
	if err != nil {
		return nil, err
	}
	return fs.openEntry(ctx, clean, entry)
}

func (fs *FileSystem) lookup(ctx context.Context, operation, name string) (string, mountfs.Entry, error) {
	if fs == nil || fs.fs == nil {
		return "", mountfs.Entry{}, fmt.Errorf("mountdav: filesystem not ready")
	}
	clean, err := cleanWebDAVName(name)
	if err != nil {
		return "", mountfs.Entry{}, pathError(operation, name, err)
	}
	entry, err := fs.fs.Stat(ctx, clean)
	if err != nil {
		if pendingEntry, ok := fs.pendingEntry(clean); ok && errors.Is(err, mountfs.ErrNotFound) {
			return clean, pendingEntry, nil
		}
		return "", mountfs.Entry{}, mapMountFSError(operation, clean, err)
	}
	return clean, entry, nil
}

// readDir lists clean's children, merging in any still-deferred empty creates
// whose parent directory is clean so directory listings match what Stat and
// GET already report during the grace window.
func (fs *FileSystem) readDir(ctx context.Context, clean string) ([]mountfs.Entry, error) {
	children, err := fs.fs.ReadDir(ctx, clean)
	if err != nil {
		return children, err
	}
	if fs.pending == nil {
		return children, nil
	}
	pending := fs.pending.childrenOf(clean)
	if len(pending) == 0 {
		return children, nil
	}
	present := make(map[string]struct{}, len(children))
	for _, child := range children {
		present[child.Name] = struct{}{}
	}
	for _, item := range pending {
		index := strings.LastIndexByte(item.path, '/')
		name := item.path
		if index >= 0 {
			name = item.path[index+1:]
		}
		if _, ok := present[name]; ok {
			continue
		}
		children = append(children, mountfs.Entry{
			ID:       pendingEntryID(item.path),
			ParentID: clean,
			Name:     name,
			Kind:     mountfs.KindFile,
			ModTime:  item.armedAt,
		})
	}
	return children, nil
}

func (fs *FileSystem) openEntry(ctx context.Context, clean string, entry mountfs.Entry) (webdav.File, error) {
	if entry.Kind == mountfs.KindDirectory {
		children, err := fs.readDir(ctx, clean)
		if err != nil {
			return nil, mapMountFSError("readdir", clean, err)
		}
		infos := make([]os.FileInfo, len(children))
		for index, child := range children {
			infos[index] = newFileInfo(child)
		}
		return newDirectoryFile(newFileInfo(entry), infos), nil
	}
	file, err := fs.fs.Open(ctx, clean)
	if err != nil {
		if isPendingEntryID(entry.ID) && errors.Is(err, mountfs.ErrNotFound) {
			// A deferred create has no committed content yet: serve the
			// zero-byte placeholder the successful PUT already promised.
			return newMetadataFile(newFileInfo(entry)), nil
		}
		return nil, mapMountFSError("open", clean, err)
	}
	return newRandomAccessFile(ctx, file, newFileInfo(entry)), nil
}

func readOnlyFlags(flag int) bool {
	const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_TRUNC | os.O_EXCL
	return flag&writeFlags == 0
}

func cleanWebDAVName(name string) (string, error) {
	if name == "" {
		return "/", nil
	}
	if !utf8.ValidString(name) || !strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) {
		return "", os.ErrInvalid
	}
	if name == "/" {
		return name, nil
	}
	name = strings.TrimSuffix(name, "/")
	for _, component := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
		if component == "" || component == "." || component == ".." {
			return "", os.ErrInvalid
		}
	}
	return name, nil
}

func mapMountFSError(operation, name string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mountfs.ErrNotFound), errors.Is(err, mountfs.ErrNotDirectory):
		return pathError(operation, name, os.ErrNotExist)
	case errors.Is(err, mountfs.ErrInvalidPath), errors.Is(err, mountfs.ErrIsDirectory):
		return pathError(operation, name, os.ErrInvalid)
	default:
		slog.Warn("mountdav: unexpected filesystem error", "operation", operation, "path", name, "error", err)
		return pathError(operation, name, err)
	}
}

func pathError(operation, name string, err error) error {
	return &os.PathError{Op: operation, Path: name, Err: err}
}

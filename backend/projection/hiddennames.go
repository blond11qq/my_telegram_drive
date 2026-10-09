package projection

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	tdcrypto "TDrive/backend/crypto"
)

// NameKeyProvider supplies a caller-owned vault master key copy for hidden
// filename resolution. It must be memory-only and never touch the database:
// it runs inside the projection write transaction, and SQLite is pinned to
// a single connection. Nil (or an error) means the vault is locked.
var NameKeyProvider func(channelID int64) ([]byte, error)

// LockedNamePlaceholder is the visible stand-in for a filename whose
// envelope cannot be opened yet (vault locked) or at all. It embeds the
// message id so placeholders never collide under sibling uniqueness, and it
// passes CanonicalNameKey so the normal apply path stores it untouched.
func LockedNamePlaceholder(msgID int64) string {
	return fmt.Sprintf("🔒 locked-%d", msgID)
}

// SealOpName encrypts name into op for an encrypted drive. op.Name stays
// plaintext in memory and in the local replay payload; only the formatted
// wire header hides it behind nenc. Call after building the op, before
// Format. The key is not retained.
func SealOpName(op *Op, name string, key []byte) error {
	if op == nil {
		return fmt.Errorf("projection: seal into nil op")
	}
	if _, err := CanonicalNameKey(name); err != nil {
		return fmt.Errorf("projection: seal invalid name: %w", err)
	}
	envelope, err := tdcrypto.EncryptFileName(key, name)
	if err != nil {
		return fmt.Errorf("projection: seal name: %w", err)
	}
	op.Name = name
	op.NameEnc = envelope
	op.NameKeyVersion = tdcrypto.NameKeyVersion
	return nil
}

// DriveEncrypted reports whether the channel has encryption enabled. Any
// lookup failure means "not encrypted": name hiding only ever engages on a
// drive that positively opted in.
func DriveEncrypted(db *sql.DB, channelID int64) bool {
	if db == nil || channelID == 0 {
		return false
	}
	config, err := GetEncryptionConfig(db, channelID)
	if err != nil {
		return false
	}
	return config.Enabled
}

// EnsureHiddenNameSchema adds the locked-name re-resolution queue. Rows that
// projected a placeholder while the vault was locked wait here until an
// unlock replays their envelopes.
func EnsureHiddenNameSchema(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS hidden_name_queue (
		channel_id INTEGER NOT NULL,
		msg_id INTEGER NOT NULL,
		PRIMARY KEY (channel_id, msg_id)
	)`); err != nil {
		return fmt.Errorf("projection: ensure hidden name queue: %w", err)
	}
	return nil
}

// ResolveOpName upgrades a sealed remote op to its plaintext name. Ops
// without an envelope (legacy history, plaintext drives, locally sealed
// commits) pass through untouched. While the vault is locked the op keeps
// a unique placeholder and the caller queues it: queued=true. Keys are
// cleared on every path.
func ResolveOpName(channelID, msgID int64, op *Op) (queued bool, err error) {
	if op == nil || op.NameEnc == "" || op.Name != "_" {
		return false, nil
	}
	key, err := nameKeyFor(channelID)
	if err != nil || key == nil {
		op.Name = LockedNamePlaceholder(msgID)
		return true, nil
	}
	defer clear(key)
	plain, err := tdcrypto.DecryptFileName(key, op.NameEnc)
	if err != nil {
		slog.Warn("projection: sealed name undecryptable, placeholder kept",
			"channel_id", channelID, "msg_id", msgID)
		op.Name = LockedNamePlaceholder(msgID)
		return true, nil
	}
	if _, err := CanonicalNameKey(plain); err != nil {
		slog.Warn("projection: sealed name invalid, placeholder kept",
			"channel_id", channelID, "msg_id", msgID)
		op.Name = LockedNamePlaceholder(msgID)
		return true, nil
	}
	op.Name = plain
	return false, nil
}

func nameKeyFor(channelID int64) ([]byte, error) {
	if NameKeyProvider == nil {
		return nil, fmt.Errorf("projection: vault locked")
	}
	return NameKeyProvider(channelID)
}

// queueLockedName records an op whose placeholder name still needs its
// envelope resolved after unlock. Idempotent: replays never duplicate it.
func queueLockedName(tx *sql.Tx, channelID, msgID int64) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO hidden_name_queue (channel_id, msg_id) VALUES (?, ?)`,
		channelID, msgID)
	return err
}

// ResolveLockedNames replays every queued envelope with now-available keys,
// updating the projected file/folder/dirents rows to their real names.
// Rows whose object is gone (deleted while locked) simply leave the queue.
// A name that still cannot be resolved — or that now collides — stays queued
// with a warning; nothing is renamed to garbage.
func ResolveLockedNames(db *sql.DB, keys func(channelID int64) ([]byte, error)) (int, error) {
	if db == nil || keys == nil {
		return 0, fmt.Errorf("projection: locked-name sweep needs a database and keys")
	}
	rows, err := db.Query(`SELECT q.channel_id, q.msg_id, l.raw_header
		FROM hidden_name_queue q JOIN replay_log l
		ON l.channel_id = q.channel_id AND l.msg_id = q.msg_id`)
	if err != nil {
		return 0, fmt.Errorf("projection: read hidden name queue: %w", err)
	}
	type queued struct {
		channelID, msgID int64
		header           string
	}
	var pending []queued
	for rows.Next() {
		var item queued
		if err := rows.Scan(&item.channelID, &item.msgID, &item.header); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	resolved := 0
	for _, item := range pending {
		op, err := Parse(item.header)
		if err != nil || op.NameEnc == "" {
			// Not a sealed op anymore (or never was): nothing to resolve.
			if err := dropQueuedName(db, item.channelID, item.msgID); err != nil {
				return resolved, err
			}
			resolved++
			continue
		}
		key, err := keys(item.channelID)
		if err != nil || key == nil {
			continue
		}
		plain, err := tdcrypto.DecryptFileName(key, op.NameEnc)
		clear(key)
		if err != nil {
			slog.Warn("projection: locked name still sealed, kept queued",
				"channel_id", item.channelID, "msg_id", item.msgID)
			continue
		}
		if _, err := CanonicalNameKey(plain); err != nil {
			slog.Warn("projection: locked name invalid, kept queued",
				"channel_id", item.channelID, "msg_id", item.msgID)
			continue
		}
		if _, err := renameResolvedName(db, item.channelID, item.msgID, op, plain); err != nil {
			slog.Warn("projection: locked name rename failed, kept queued",
				"channel_id", item.channelID, "msg_id", item.msgID, "error", err)
			continue
		}
		if err := dropQueuedName(db, item.channelID, item.msgID); err != nil {
			return resolved, err
		}
		resolved++
	}
	return resolved, nil
}

func dropQueuedName(db *sql.DB, channelID, msgID int64) error {
	_, err := db.Exec(`DELETE FROM hidden_name_queue WHERE channel_id=? AND msg_id=?`, channelID, msgID)
	return err
}

// renameResolvedName writes a resolved plaintext name to every projected row
// the op owns: the dirents namespace entry plus the legacy files/folders
// row. Missing rows are fine (the other generation owns the object, or it
// was deleted while locked). It reports whether any row changed.
func renameResolvedName(db *sql.DB, channelID, msgID int64, op Op, plain string) (bool, error) {
	key, err := CanonicalNameKey(plain)
	if err != nil {
		return false, err
	}
	var changed bool
	touch := func(result sql.Result, err error) error {
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n > 0 {
			changed = true
		}
		return nil
	}
	switch op.Type {
	case OpFileUpload, OpFileManifest, OpFileCommit:
		// File identity is the op's own message.
		if err := touch(db.Exec(`UPDATE files SET name=? WHERE channel_id=? AND msg_id=? AND tombstoned=0`,
			plain, channelID, msgID)); err != nil {
			return false, fmt.Errorf("projection: rename resolved file: %w", err)
		}
		if err := touch(db.Exec(`UPDATE dirents SET display_name=?, name_key=? WHERE channel_id=? AND object_id=? AND tombstoned=0`,
			plain, key, channelID, FileIDPrefix+itoa(msgID))); err != nil {
			return false, fmt.Errorf("projection: rename resolved dirent: %w", err)
		}
	case OpMeta, OpRename, OpRelocate, OpRestoreTree, OpMkdir, OpFolderCommit:
		if op.Obj == "" {
			return false, nil
		}
		if err := touch(db.Exec(`UPDATE dirents SET display_name=?, name_key=? WHERE channel_id=? AND object_id=? AND tombstoned=0`,
			plain, key, channelID, op.Obj)); err != nil {
			return false, fmt.Errorf("projection: rename resolved dirent: %w", err)
		}
		switch {
		case isFileObj(op.Obj):
			if err := touch(db.Exec(`UPDATE files SET name=? WHERE channel_id=? AND msg_id=? AND tombstoned=0`,
				plain, channelID, fileMsgFromObj(op.Obj))); err != nil {
				return false, fmt.Errorf("projection: rename resolved file: %w", err)
			}
		case isFolderObj(op.Obj):
			if err := touch(db.Exec(`UPDATE folders SET name=? WHERE channel_id=? AND id=? AND tombstoned=0`,
				plain, channelID, op.Obj)); err != nil {
				return false, fmt.Errorf("projection: rename resolved folder: %w", err)
			}
		}
	default:
		return false, nil
	}
	return changed, nil
}

func isFileObj(id string) bool {
	return strings.HasPrefix(id, FileIDPrefix)
}

func isFolderObj(id string) bool {
	return strings.HasPrefix(id, FolderIDPrefix)
}

func fileMsgFromObj(id string) int64 {
	n, err := strconv.ParseInt(strings.TrimPrefix(id, FileIDPrefix), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

#!/usr/bin/env bash
# tdrive-backup — upload a file to TDrive, verify it, then delete the local
# original ONLY after the remote copy is confirmed byte-for-byte.
#
# Safety chain: tdrive put (exit 0 = Telegram upload finished)
#            -> remote ls JSON (plaintext: exact size check;
#               encrypted: download + byte-compare, sizes differ by design)
#            -> rm local original
# Any failure keeps the local file and exits non-zero.
set -u

# tdrive lives in ~/.local/bin (install-cli default), which non-login shells
# often lack on PATH.
command -v tdrive >/dev/null 2>&1 || export PATH="$HOME/.local/bin:$PATH"

LOG="${TDRIVE_BACKUP_LOG:-$HOME/.config/TDrive/backup.log}"

usage() {
  cat >&2 <<'EOF'
Usage: tdrive-backup [-k|--keep] [--plaintext] <local-file...> <remote-dir>

Uploads each local file to <remote-dir> on the active TDrive personal drive
(encrypted by default), verifies the remote size, and only then deletes the
local original. On any failure the local file is kept.

  -k, --keep     verify the upload but never delete local files
  --plaintext    store uploads unencrypted (default is encrypted)
  -e, --encrypt  accepted for compatibility; encryption is already the default
  -h, --help     show this help

Examples:
  tdrive-backup ~/사진.jpg /백업/
  tdrive-backup -k a.pdf b.pdf /        # 확인만, 삭제 안 함
EOF
  exit 2
}

log() {
  mkdir -p "$(dirname "$LOG")"
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$LOG"
}

KEEP=0
PUT_OPTS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -k|--keep) KEEP=1; shift ;;
    -e|--encrypt) PUT_OPTS+=(-e); shift ;;
    --plaintext|--no-encrypt) PUT_OPTS+=(--plaintext); shift ;;
    -h|--help) usage ;;
    --) shift; break ;;
    -*) echo "tdrive-backup: unknown option $1" >&2; usage ;;
    *) break ;;
  esac
done

[[ $# -ge 2 ]] || usage
remote_dir="${!#}"
set -- "${@:1:$(($# - 1))}"

# Normalize remote dir: absolute, no trailing slash (except root).
case "$remote_dir" in
  /*) ;;
  *) remote_dir="/$remote_dir" ;;
esac
while [[ "$remote_dir" != "/" && "$remote_dir" == */ ]]; do
  remote_dir="${remote_dir%/}"
done

# Active drive id (JSON mode requires it).
drive_id=$(tdrive drives --json --non-interactive 2>/dev/null |
  python3 -c 'import json,sys
d=json.load(sys.stdin)
print(next(x["id"] for x in d["data"]["drives"] if x.get("active")))' 2>/dev/null) || true
if [[ -z "$drive_id" ]]; then
  echo "tdrive-backup: no active drive (run: tdrive drive use <id>)" >&2
  log "ERROR no-active-drive"
  exit 1
fi

fail=0
for src in "$@"; do
  if [[ ! -f "$src" ]]; then
    echo "tdrive-backup: not a regular file: $src" >&2
    log "ERROR src-missing file=$src"
    fail=1
    continue
  fi
  base=$(basename -- "$src")
  size=$(stat -c %s -- "$src")
  if [[ "$remote_dir" == "/" ]]; then dest="/$base"; else dest="$remote_dir/$base"; fi

  if ! tdrive put ${PUT_OPTS[@]+"${PUT_OPTS[@]}"} -- "$src" "$dest" >&2; then
    echo "tdrive-backup: upload FAILED, keeping: $src" >&2
    log "ERROR put-failed file=$src remote=$dest size=$size"
    fail=1
    continue
  fi

  # Verify: for plaintext uploads the remote size must match exactly.
  # Encrypted uploads store ciphertext (different size by design), so those
  # are verified by downloading to a temp file and byte-comparing instead.
  # The fresh upload can take a few seconds to appear in listings: retry.
  list_dir=$(dirname -- "$dest")
  entry_json=""
  for attempt in 1 2 3 4 5 6; do
    entry_json=$(tdrive ls "$list_dir" --drive-id "$drive_id" --json --non-interactive 2>/dev/null |
      python3 -c "
import json,sys
want='$dest'
for e in json.load(sys.stdin)['data']['entries']:
    if e['path']==want and e['type']=='file':
        print(json.dumps({'size':e['size'],'encrypted':bool(e.get('encrypted'))})); break" 2>/dev/null) || true
    [[ -n "$entry_json" ]] && break
    sleep 3
  done

  encrypted=$(python3 -c "import json,sys;print(json.loads(sys.argv[1]).get('encrypted',False))" "$entry_json" 2>/dev/null || true)
  remote_size=$(python3 -c "import json,sys;print(json.loads(sys.argv[1]).get('size',''))" "$entry_json" 2>/dev/null || true)

  verified=0
  if [[ "$encrypted" == "True" ]]; then
    tmp_verify=$(mktemp) || { echo "tdrive-backup: mktemp failed, keeping: $src" >&2; log "ERROR mktemp file=$src"; fail=1; continue; }
    if tdrive get "$dest" "$tmp_verify" --drive-id "$drive_id" --non-interactive --yes >&2 2>/dev/null && cmp -s -- "$tmp_verify" "$src"; then
      verified=1
    else
      echo "tdrive-backup: VERIFY FAILED (content mismatch) for $dest, keeping: $src" >&2
      log "ERROR verify-failed file=$src remote=$dest reason=content-mismatch"
    fi
    rm -f -- "$tmp_verify"
  elif [[ "$remote_size" == "$size" && -n "$remote_size" ]]; then
    verified=1
  else
    echo "tdrive-backup: VERIFY FAILED for $dest (want size=$size, got '${remote_size:-none}'), keeping: $src" >&2
    log "ERROR verify-failed file=$src remote=$dest want=$size got=${remote_size:-none}"
  fi
  if [[ "$verified" != 1 ]]; then
    fail=1
    continue
  fi

  if [[ "$KEEP" == 1 ]]; then
    echo "tdrive-backup: verified (kept local): $dest ($size bytes)"
    log "OK keep file=$src remote=$dest size=$size"
    continue
  fi

  if ! rm -- "$src"; then
    echo "tdrive-backup: uploaded+verified but could NOT delete local: $src" >&2
    log "ERROR local-delete-failed file=$src remote=$dest size=$size"
    fail=1
    continue
  fi
  echo "tdrive-backup: done: $src -> $dest ($size bytes), local deleted"
  log "OK file=$src remote=$dest size=$size deleted=yes"
done

exit "$fail"

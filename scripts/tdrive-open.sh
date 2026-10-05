#!/usr/bin/env bash
# Resolve the current TDrive gvfs mount and refresh the ~/TDrive symlink.
# Usage: tdrive-open          -> prints & creates/updates ~/TDrive symlink
#        tdrive-open --mount  -> runs `tdrive mount` first if not mounted
set -euo pipefail

GVFS_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/gvfs"
LINK="${HOME}/TDrive"

if [[ "${1:-}" == "--mount" ]]; then
  if ! tdrive mount status 2>/dev/null | grep -q '^mounted:'; then
    tdrive mount start
  fi
fi

mount_path="$(find "$GVFS_DIR" -maxdepth 1 -type d -name 'dav:host=127.0.0.1,port=*,ssl=false,prefix=*tdrive*' 2>/dev/null | head -1)"

if [[ -z "$mount_path" ]]; then
  echo "TDrive is not mounted. Run: tdrive mount start (or tdrive-open --mount)" >&2
  exit 1
fi

ln -sfn "$mount_path" "$LINK"
echo "$LINK"

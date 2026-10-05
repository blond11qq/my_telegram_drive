#!/bin/bash
# Mount the TDrive personal drive on macOS by tunneling its loopback-only
# WebDAV endpoint over SSH (Tailscale). Uses only macOS built-ins.
#
# Usage:
#   ./tdrive-macos-connect.sh          # ensure Linux mount + tunnel, open in Finder
#   ./tdrive-macos-connect.sh --unmount  # unmount Finder share (tunnel stays)
#
# The port and capability token change on every Linux-side remount/reboot,
# so this script asks the Linux machine for the current values each run.
set -euo pipefail

# xo-l = Tailscale hostname of the Linux box; use xo@100.74.200.101 if
# MagicDNS is unavailable.
SSH_HOST="${TDRIVE_SSH:-xo@xo-l}"

if [[ "${1:-}" == "--unmount" ]]; then
  target=$(ssh "$SSH_HOST" 'readlink "$HOME/TDrive" 2>/dev/null || true')
  port=${target##*port=}; port=${port%%,*}
  cap=${target##*prefix=%2F}
  if [[ -n "${port:-}" && "$cap" == tdrive-* ]]; then
    open "dav://127.0.0.1:$port/$cap/" 2>/dev/null || true
  fi
  echo "If Finder still shows it, press ⌘E (Eject) in Finder."
  exit 0
fi

# 1) Make sure the drive is mounted on Linux, then read port + token from
#    the ~/TDrive symlink (format: dav:host=127.0.0.1,port=NNNN,ssl=false,prefix=%2Ftdrive-<hex>)
target=$(ssh "$SSH_HOST" 'readlink "$HOME/TDrive" 2>/dev/null || tdrive-open --mount')
port=${target##*port=}; port=${port%%,*}
cap=${target##*prefix=%2F}
if [[ -z "${port:-}" || "$cap" != tdrive-* ]]; then
  echo "Could not parse TDrive mount from: $target" >&2
  exit 1
fi

# 2) Open an SSH tunnel on the SAME port (the server requires Host to be
#    exactly 127.0.0.1:<port>). Skip if the tunnel already exists.
if ! nc -z 127.0.0.1 "$port" 2>/dev/null; then
  ssh -f -N -L "$port:127.0.0.1:$port" "$SSH_HOST"
  sleep 1
  nc -z 127.0.0.1 "$port" || { echo "Tunnel failed on port $port" >&2; exit 1; }
fi

# 3) Mount it in Finder
open "dav://127.0.0.1:$port/$cap/"
echo "TDrive mounted in Finder (server: Tdrive personal)"
echo "  Local path:  Finder > Network > Tdrive personal"
echo "  To eject:    ⌘E in Finder, or run: $0 --unmount"

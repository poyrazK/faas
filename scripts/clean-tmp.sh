#!/usr/bin/env bash
# Remove go-build work directories that killed or interrupted builds left in the
# temp dir. Each holds compiler outputs for one build (often hundreds of MB), and
# parallel sessions have filled developer disks with them.
#
# A directory is removed only when nothing inside it changed for AGE_MINUTES
# (default 120), so builds still running in other sessions keep theirs. The
# shared GOCACHE is never touched: clearing it fails other sessions' builds and
# makes everyone rebuild cold.
set -euo pipefail

tmp="${TMPDIR:-/tmp}"
tmp="${tmp%/}"
age="${AGE_MINUTES:-120}"
freed=0
removed=0
kept=0
shopt -s nullglob
for dir in "$tmp"/go-build*; do
  [[ -d "$dir" ]] || continue
  if [[ -n "$(find "$dir" -mmin "-$age" -print -quit 2>/dev/null)" ]]; then
    kept=$((kept + 1))
    continue
  fi
  size=$(du -sk "$dir" 2>/dev/null | awk '{print $1}')
  rm -rf "$dir"
  freed=$((freed + ${size:-0}))
  removed=$((removed + 1))
done
awk -v r="$removed" -v k="$kept" -v f="$freed" -v t="$tmp" -v a="$age" \
  'BEGIN { printf "clean-tmp: removed %d go-build dirs idle >%d min (%.1f GB) from %s; kept %d in use\n", r, a, f / 1048576, t, k }'

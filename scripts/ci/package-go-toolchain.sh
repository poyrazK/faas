#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 2 ]]; then
  echo "usage: $0 <go-root> <output-archive>" >&2
  exit 2
fi

go_root="$1"
output_archive="$2"

if [[ ! -x "$go_root/bin/go" ]]; then
  echo "Go binary is not executable: $go_root/bin/go" >&2
  exit 1
fi

# Archive the contents of GOROOT rather than its host-specific basename.
# actions/setup-go currently installs under .../<version>/x64, while the
# remote acceptance contract deliberately uses the stable tools/go/bin/go
# path. Rooting the archive here keeps those two details independent.
tar -C "$go_root" -czf "$output_archive" .

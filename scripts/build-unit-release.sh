#!/usr/bin/env bash
# Build the unsigned part of a per-unit control-plane release
# (docs/adr/1650-per-unit-control-plane-releases.md) from an
# affected_units.py report: every deployable control-plane binary, built with
# build-release-batch's flags and stamped with its unit version, plus
# units-manifest.json describing them. release-units.yml signs the tarball.
#
#   REPORT=report.json OUT_DIR=out scripts/build-unit-release.sh
#
# Writes units.tar.gz (bin/<unit>..., units-manifest.json), a copy of
# units-manifest.json, and SHA256SUMS into OUT_DIR. The tarball's bytes
# depend only on the binaries and the head commit's timestamp.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
report=${REPORT:?set REPORT to an affected_units.py report}
out_dir=${OUT_DIR:?set OUT_DIR}
lanes=${LANES:-"$repo_root/scripts/ci/release_units.json"}
tar_bin=${TAR:-tar}

"$tar_bin" --version 2>/dev/null | grep -q 'GNU tar' || {
  echo "build-unit-release: GNU tar is required for a deterministic archive (set TAR=gtar)" >&2
  exit 2
}
[[ "$(jq -r .eligible "$report")" == true ]] || {
  echo "build-unit-release: report is not eligible for a unit release: $(jq -r '.reasons | join("; ")' "$report")" >&2
  exit 1
}
head_sha=$(jq -er .head "$report")
[[ "$(git -C "$repo_root" rev-parse HEAD)" == "$head_sha" ]] || {
  echo "build-unit-release: checkout is not at the report head $head_sha" >&2
  exit 1
}

work=$(mktemp -d "${TMPDIR:-/tmp}/gregale-units.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$out_dir"
rm -f "$out_dir/units.tar.gz" "$out_dir/units-manifest.json" "$out_dir/units.cosign.bundle" "$out_dir/SHA256SUMS"

while read -r unit version; do
  [[ "$unit" =~ ^[a-z0-9-]+$ && "$version" =~ ^unit-[0-9a-f]{40}$ ]] || {
    echo "build-unit-release: bad unit entry '$unit $version'" >&2
    exit 1
  }
  echo "building $unit ($version)"
  make -C "$repo_root" --no-print-directory build-release-unit \
    UNIT="$unit" VERSION="$version" BINDIR="$work/bin" GOOS=linux GOARCH=amd64
done < <(jq -r '.deployable[] as $u | "\($u) \(.binaries[$u].version)"' "$report")

python3 - "$report" "$lanes" "$work" <<'PY'
import hashlib
import json
import os
import sys

report_path, lanes_path, work = sys.argv[1:]
with open(report_path, encoding="utf-8") as fh:
    report = json.load(fh)
with open(lanes_path, encoding="utf-8") as fh:
    lanes = json.load(fh)["binaries"]
units = {}
for name in report["deployable"]:
    path = os.path.join(work, "bin", name)
    with open(path, "rb") as fh:
        digest = hashlib.sha256(fh.read()).hexdigest()
    entry = report["binaries"][name]
    units[name] = {
        "version": entry["version"],
        "commit": entry["commit"],
        "sha256": digest,
        "size": os.path.getsize(path),
        "services": lanes[name]["services"],
    }
manifest = {
    "format_version": 1,
    "kind": "gregale-unit-release",
    "git_sha": report["head"],
    "base_sha": report["base"],
    "base_ref": report["base_ref"],
    "target": "linux/amd64",
    "units": units,
    "pending": report["pending"],
    "flags": report["flags"],
}
with open(os.path.join(work, "units-manifest.json"), "w", encoding="utf-8") as fh:
    json.dump(manifest, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY

mtime=$(git -C "$repo_root" log -1 --format=%ct "$head_sha")
"$tar_bin" --create --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$mtime" \
  --directory "$work" bin units-manifest.json | gzip -n > "$out_dir/units.tar.gz"
install -m 0644 "$work/units-manifest.json" "$out_dir/units-manifest.json"
(cd "$out_dir" && sha256sum units.tar.gz units-manifest.json > SHA256SUMS)
echo "unit release ready in $out_dir: $(jq -r '.deployable | join(" ")' "$report")"

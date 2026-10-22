#!/usr/bin/env bash
# Build the unsigned part of a per-unit release
# (docs/adr/1650-per-unit-control-plane-releases.md) from an
# affected_units.py report: every binary of an eligible lane (control-plane,
# edge), built with build-release-batch's flags and stamped with its unit
# version, plus units-manifest.json describing them, and the deployctl the
# deploy hosts run (tools/deployctl). A report made with --code gives each
# unit its code status and the code digests at head and base, which the
# manifest carries so the deploy plan restarts only units whose code the
# host does not run yet ("Selection by code"). When the range added
# lane-eligible migrations (the report's data step), it also carries the
# migrate tool built at the head commit (tools/migrate), which the
# control-plane lane runs before it restarts anything
# (docs/adr/20261018084150-unit-lane-expand-migrations.md), and the
# manifest records every migration's verdict. When the range changed a
# control-plane host role, it carries the gregalectl built at the head
# commit (tools/gregalectl), whose converge-control-plane --roles the
# control-plane lane runs first, and the manifest records the step
# (docs/adr/20261018133100-host-configuration-lane.md). release-units.yml
# signs the tarball.
#
#   REPORT=report.json OUT_DIR=out scripts/build-unit-release.sh
#
# Writes units.tar.gz (bin/<unit>..., tools/deployctl, units-manifest.json), a copy of
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
mkdir -p "$work/bin" "$work/tools" "$out_dir"
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

# The deploy controller rides in the signed release, so deploy jobs neither
# build it nor trust a runner-built copy. Same flags as the full lane's.
echo "building deployctl"
(cd "$repo_root" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${GO:-go}" build -trimpath -buildvcs=false \
  -ldflags "-s -w -X github.com/onebox-faas/faas/pkg/wire.Version=${head_sha}" \
  -o "$work/tools/deployctl" ./cmd/deployctl)
if [[ "$(jq -r '.data.version // empty' "$report")" =~ ^data-[0-9a-f]{40}$ ]]; then
  # The full lane's controller-built bin/migrate, from the release commit.
  echo "building migrate ($(jq -r '.data.migrations | length' "$report") migrations, $(jq -r .data.version "$report"))"
  (cd "$repo_root" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${GO:-go}" build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/onebox-faas/faas/pkg/wire.Version=${head_sha}" \
    -o "$work/tools/migrate" ./cmd/migrate)
fi

if [[ "$(jq -r '.host_config.version // empty' "$report")" =~ ^host-[0-9a-f]{40}$ ]]; then
  # The ADR-580 converge command at the release commit, matching its roles.
  echo "building gregalectl (host config: $(jq -r '.host_config.roles | join(" ")' "$report"))"
  (cd "$repo_root" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${GO:-go}" build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/onebox-faas/faas/pkg/wire.Version=${head_sha}" \
    -o "$work/tools/gregalectl" ./cmd/gregalectl)
fi

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
        "lane": entry["lane"],
        "version": entry["version"],
        "commit": entry["commit"],
        "sha256": digest,
        "size": os.path.getsize(path),
        "services": lanes[name]["services"],
    }
    # Selection by code: the status against base and the digests the plan
    # compares with what each host runs (scripts/ci/binary_code.py).
    for key in ("code", "code_digest", "base_code_digest"):
        if entry.get(key):
            units[name][key] = entry[key]
tools = {}
for name in sorted(os.listdir(os.path.join(work, "tools"))):
    path = os.path.join(work, "tools", name)
    with open(path, "rb") as fh:
        tools[name] = {"sha256": hashlib.sha256(fh.read()).hexdigest(), "size": os.path.getsize(path)}
manifest = {
    "format_version": 2,
    "kind": "gregale-unit-release",
    "git_sha": report["head"],
    "base_sha": report["base"],
    "base_ref": report["base_ref"],
    "target": "linux/amd64",
    "units": units,
    "tools": tools,
    "lanes": {lane: {"eligible": v["eligible"], "units": v["carried"] if v["eligible"] else [],
                     "code_changed": v["changed"] if v["eligible"] else []}
              for lane, v in report["lanes"].items()},
    "pending": report["pending"],
    "flags": report["flags"],
}
if report.get("code_selection"):
    manifest["code_selection"] = {key: report["code_selection"][key] for key in ("neutral_version", "go")}
data = report.get("data") or {}
if data.get("version"):
    manifest["data"] = {key: data[key] for key in ("eligible", "version", "commit", "migrations", "blockers")}
host_config = report.get("host_config") or {}
if host_config.get("version"):
    manifest["host_config"] = {key: host_config[key] for key in ("group", "roles", "all", "commit", "version")}
with open(os.path.join(work, "units-manifest.json"), "w", encoding="utf-8") as fh:
    json.dump(manifest, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY

mtime=$(git -C "$repo_root" log -1 --format=%ct "$head_sha")
"$tar_bin" --create --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$mtime" \
  --directory "$work" bin tools units-manifest.json | gzip -n > "$out_dir/units.tar.gz"
install -m 0644 "$work/units-manifest.json" "$out_dir/units-manifest.json"
(cd "$out_dir" && sha256sum units.tar.gz units-manifest.json > SHA256SUMS)
echo "unit release ready in $out_dir: $(jq -r '.deployable | join(" ")' "$report")"

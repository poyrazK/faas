#!/usr/bin/env bash
# Build guest-init's Java restore reseed agent (ADR-20261110150000) into $1.
# Reproducible for one JDK major: fixed entry dates, sorted entries, and the
# source's SHA-256 in the manifest, which a guest-init test compares with the
# committed source.
set -euo pipefail
out=${1:?usage: build-java-reseed-agent.sh OUT.jar}
src=guest/init/rngpreload/java/dev/gregale/reseed/Agent.java
want=${RNG_JAVA_AGENT_JDK:-25}
have=$(javac -version 2>&1 | awk '{print $2}' | cut -d. -f1)
[ "$have" = "$want" ] || { echo "build-java-reseed-agent: need JDK $want, have '$have'" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
javac --release 8 -Xlint:-options -g:none -d "$tmp/classes" "$src"
if command -v sha256sum >/dev/null; then sum=$(sha256sum "$src"); else sum=$(shasum -a 256 "$src"); fi
printf 'Manifest-Version: 1.0\nPremain-Class: dev.gregale.reseed.Agent\nAgent-Class: dev.gregale.reseed.Agent\nGregale-Source-SHA256: %s\n' "${sum%% *}" >"$tmp/MANIFEST.MF"
entries=$(cd "$tmp/classes" && find . -name '*.class' | LC_ALL=C sort)
# shellcheck disable=SC2086 # one entry per word, in sorted order
(cd "$tmp/classes" && jar --create --date=1980-01-02T00:00:02Z --file "$tmp/agent.jar" --manifest "$tmp/MANIFEST.MF" $entries)
cp "$tmp/agent.jar" "$out"

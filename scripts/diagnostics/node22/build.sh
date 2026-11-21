#!/usr/bin/env bash
set -Eeuo pipefail
umask 022
ulimit -c 0

node_version=22.23.2
source_sha=bbe768df8d5815d7fa76124052985332452e0a4742d39f32027550d1aab8f6fb
official_binary_sha=f371ff1f3888ed7ce00949104bc39a125888d2ed6f246468b4adc40ce0e19859
cd /diagnostic
mkdir -p artifact bundle
printf '%s  %s\n' "$official_binary_sha" /usr/local/bin/node | sha256sum -c -
test "$(/usr/local/bin/node --version)" = "v$node_version"
/usr/local/bin/node framed-smoke.cjs /usr/local/bin/node fixture > official-smoke.json

curl --fail --location --retry 3 --connect-timeout 15 --max-time 300 \
  "https://nodejs.org/dist/v$node_version/node-v$node_version.tar.xz" \
  --output node-source.tar.xz
printf '%s  %s\n' "$source_sha" node-source.tar.xz | sha256sum -c -
tar -xJf node-source.tar.xz
cd "node-v$node_version"
./configure
# Two compiler jobs keep the full source rebuild inside the CI runner's RAM.
make -j2
cp out/Release/node /diagnostic/bundle/node-stock
printf '%s  %s\n' 04b2b4112720882c5d40dba99fd8b46d07378507c7bff65ac05ec3f8aff060e9 \
  deps/v8/src/utils/identity-map.cc | sha256sum -c -
patch --dry-run -p1 < /diagnostic/identity-map.patch
patch -p1 < /diagnostic/identity-map.patch
make -j2
cp out/Release/node /diagnostic/bundle/node-labeled
cd /diagnostic
./bundle/node-stock framed-smoke.cjs ./bundle/node-stock fixture > stock-smoke.json
./bundle/node-labeled framed-smoke.cjs ./bundle/node-labeled fixture > labeled-smoke.json
python3 manifest.py > artifact/build-manifest.json
cp artifact/build-manifest.json official-smoke.json stock-smoke.json labeled-smoke.json bundle/
cp identity-map.patch bundle/
tar -czf artifact/node22-diagnostic.tar.gz -C bundle .

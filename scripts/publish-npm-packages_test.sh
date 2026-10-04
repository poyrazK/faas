#!/usr/bin/env bash
# ADR-172: exercise registry propagation, incomplete retries and tag isolation.
set -euo pipefail
cd "$(dirname "$0")/.."
node --test scripts/publish-npm-packages.test.mjs

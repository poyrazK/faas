#!/usr/bin/env bash
# Run as root on the selected, designated acceptance node after capacity clears.
set -euo pipefail
cd /mnt/e2e-scratch/gregale-run-system-env-d59371f13ad7/source
export FAAS_RUN_LIFECYCLE_SOURCE_SHA=e74c7cfc6e5b9cff5596d7f430a4d5e0c6bbfab1
export FAAS_RUN_LIFECYCLE_GO=/var/cache/faas-native-e2e/go-mod/golang.org/toolchain@v0.0.1-go1.26.11.linux-amd64/bin/go
export FAAS_RUN_LIFECYCLE_OUTPUT=/mnt/e2e-scratch/run-lifecycle-system-tools-d59371f13ad7-retry2/evidence
export FAAS_RUN_LIFECYCLE_COHORT=system-environments
export FAAS_RUN_LIFECYCLE_LOCK_WAIT_SECONDS=7200
export FAAS_TEST_SYSTEM_ENVIRONMENT_BASE_REF=cgr.dev/chainguard/wolfi-base@sha256:e9b8c3739e5fb5fc778843a2ce74f8620588c9ddc9c44918cead527abcb0b628
export FAAS_TEST_SYSTEM_ENVIRONMENT_PACKAGES=python-3.13=3.13.15-r0,poppler-utils=26.08.0-r0
exec bash scripts/ci/run-run-lifecycle-acceptance.sh

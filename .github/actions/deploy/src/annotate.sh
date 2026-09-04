#!/usr/bin/env bash
# src/annotate.sh — Gregale deploy action failure annotator.
#
# Reads the CLI's RFC 7807 stderr (written by run.sh on failure to
# $RUNNER_TEMP/gregale-deploy-action.stderr) and emits a single
# `::error file=action.yml,line=1::code=<code> — <detail>` line
# with the bearer token redacted.
#
# SECURITY: the regex scrub is the load-bearing defence for token
# leakage into the GitHub Actions log. The token is NEVER in the
# Problem body — pkg/api/client.go sets the Authorization header,
# and the server doesn't echo it back — but the scrub is a
# belt-and-braces defence against future server regressions or
# test fixtures that leak a token through some other channel.
#
# The REDACT_PATTERNS list mirrors the (gho_|ghp_|ghu_|ghs_|ghr_),
# Bearer <token>, and FAAS_TOKEN=<token> families. Update both
# this file and the related regex list in any TS / JS surface that
# surfaces server errors.

set -euo pipefail

ACTION_PATH="${ACTION_PATH:-$(cd "$(dirname "$0")/.." && pwd)}"
STDERR_FILE="${RUNNER_TEMP:-/tmp}/gregale-deploy-action.stderr"

# Token patterns to redact. Order matters: longer / more specific
# patterns first. The leading "g" pattern matches gh*_ prefixes;
# the Bearer pattern matches the full opaque token; the FAAS_TOKEN
# pattern matches the env-var form (defence-in-depth).
REDACT_PATTERNS=(
    # Matches every documented GitHub token prefix: gho_ (OAuth),
    # ghp_ (PAT), ghu_ (user-to-server), ghs_ (server-to-server),
    # ghr_ (refresh). The 20+ length floor matches GitHub's own
    # token-length floor (classic PATs and fine-grained PATs are
    # both >= 36 chars; OAuth tokens are >= 40).
    's/gh[opsur]_[A-Za-z0-9_]\{20,\}/[REDACTED_TOKEN]/g'
    's/Bearer [A-Za-z0-9._-]\{8,\}/Bearer [REDACTED_TOKEN]/g'
    's/FAAS_TOKEN=[A-Za-z0-9._-]\{8,\}/FAAS_TOKEN=[REDACTED_TOKEN]/g'
)

redact() {
    local input="$1"
    local output="$input"
    for pat in "${REDACT_PATTERNS[@]}"; do
        output="$(printf '%s' "$output" | sed "$pat")"
    done
    printf '%s' "$output"
}

emit() {
    local raw="$1"
    # Try to extract the RFC 7807 problem fields. The CLI's
    # --json failure path emits a single JSON object on stderr
    # (cmd/gregale/json_flag.go:116-122 writeJSONProblem). The
    # fields are grep-extractable; we don't need a full JSON
    # parser for this surface.
    local code
    local detail
    code="$(printf '%s' "$raw" | grep -oE '"code"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"code"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"
    detail="$(printf '%s' "$raw" | grep -oE '"detail"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"detail"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"

    if [ -z "$code" ]; then
        # No RFC 7807 fields → surface the raw stderr redacted.
        # This is the test-fixture or unexpected-error path.
        echo "::error file=action.yml,line=1::$(redact "$raw")"
    else
        # Compose the canonical ::error line. file=action.yml is
        # the customer-facing reference; line=1 is the synthetic
        # step location.
        echo "::error file=action.yml,line=1::code=$code — $(redact "$detail")"
    fi
}

if [ -f "$STDERR_FILE" ]; then
    raw="$(cat "$STDERR_FILE")"
    emit "$raw"
else
    echo "::error file=action.yml,line=1::deploy failed (no stderr captured; check the Actions UI for the upstream step output)"
fi

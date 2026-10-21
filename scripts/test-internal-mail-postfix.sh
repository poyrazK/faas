#!/usr/bin/env bash
# Build a disposable relay fixture. Running it has no external network access;
# the only SMTP peer and report API are loopback/Unix test servers.
set -euo pipefail
cd "$(dirname "$0")/.."
postfix_test_context=$(mktemp -d)
trap 'rm -rf "$postfix_test_context"' EXIT
CGO_ENABLED=0 "${GO:-go}" build -o "$postfix_test_context/mail-report" ./cmd/mail-report
CGO_ENABLED=0 "${GO:-go}" test -c -tags postfix -o "$postfix_test_context/postfix.test" ./pkg/internalmail
cp deploy/internal-mail/test.Dockerfile "$postfix_test_context/Dockerfile"
cp deploy/internal-mail/postfix-*.example "$postfix_test_context/"
cp deploy/ansible/roles/internal_mail_service/files/faas-mail-report "$postfix_test_context/"
postfix_build_args=()
if [ -n "${CODEX_PROXY_CERT:-}" ]; then
    postfix_build_args+=(--secret "id=proxy_ca,src=$CODEX_PROXY_CERT")
fi
docker build "${postfix_build_args[@]}" -t gregale-internal-mail-postfix-test "$postfix_test_context"
docker run --rm --network none --memory=512m --cpus=2 --pids-limit=128 gregale-internal-mail-postfix-test

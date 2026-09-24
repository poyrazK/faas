package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The production script must add verified deployments when a valid first
// wave is skewed, and stop as soon as actual node coverage is observed.
func TestProductionReleaseAcceptanceFillsSkewedNodeCoverage(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is required by the production acceptance script")
	}
	dir := t.TempDir()
	writeExecutable := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	gregale := writeExecutable("gregale", `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  apps) exit 0 ;;
  deployment)
    printf '{"id":"redeployed","status":"live","rollout_state":"complete","app_url":"https://test.invalid","hosting_receipt":{"smoke":{"status":"verified","status_code":200,"path":"/healthz"}}}\n'
    exit 0 ;;
  deploy)
    slug=""; no_wait=false
    while (($#)); do
      case "$1" in
        --name) slug="$2"; shift 2 ;;
        --no-wait) no_wait=true; shift ;;
        *) shift ;;
      esac
    done
    printf '%s\n' "$slug" >>"$TEST_DEPLOY_LOG"
    if [[ "$no_wait" == true ]]; then
      printf '{"id":"redeploy-queued"}\n'
    else
      printf '{"id":"deployment-%s","status":"live","rollout_state":"complete","app_url":"https://test.invalid","hosting_receipt":{"smoke":{"status":"verified","status_code":200,"path":"/healthz"}}}\n' "$slug"
    fi
    exit 0 ;;
esac
exit 2
`)
	gregalectl := writeExecutable("gregalectl", `#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  'release-acceptance mint-token')
    printf '{"key_id":"key-1","token":"test-token"}\n'; exit 0 ;;
  'release-acceptance revoke-token')
    printf 'revoked\n' >>"$TEST_REVOKE_LOG"; exit 0 ;;
  'release-acceptance verify-placement')
    if [[ "${TEST_NEVER_READY:-}" != 1 && "$*" == *-x2* ]]; then
      printf '{"ready":true,"nodes":[{"name":"fsn-2"},{"name":"fsn-3"}]}\n'; exit 0
    fi
    printf '{"ready":false,"nodes":[{"name":"fsn-2"},{"name":"fsn-3"}]}\n'; exit 3 ;;
esac
exit 2
`)
	writeExecutable("curl", `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TEST_CURL_LOG"
if [[ "${TEST_REDEPLOY_503:-}" == 1 && "$*" == *"ra-"* ]]; then
  headers=""; body=""
  while (($#)); do
    case "$1" in
      --dump-header) headers="$2"; shift 2 ;;
      --output) body="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  printf 'HTTP/2 503\r\nx-faas-request-id: failed-probe-1\r\n\r\n' >"$headers"
  printf '{"code":"capacity","detail":"previous revision unavailable"}' >"$body"
  exit 22
fi
`)
	deployLog := filepath.Join(dir, "deploys")
	curlLog := filepath.Join(dir, "curls")
	revokeLog := filepath.Join(dir, "revokes")
	script := filepath.Join("..", "..", "deploy", "scripts", "production-release-acceptance.sh")
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
		"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
		"TEST_DEPLOY_LOG="+deployLog, "TEST_CURL_LOG="+curlLog, "TEST_REVOKE_LOG="+revokeLog,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("acceptance script: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "production acceptance passed") {
		t.Fatalf("success report missing: %s", out)
	}
	deployed, err := os.ReadFile(deployLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deployed), "-x1\n") || !strings.Contains(string(deployed), "-x2\n") || strings.Contains(string(deployed), "-x3\n") {
		t.Fatalf("extra deployment bound was not respected: %s", deployed)
	}
	curls, err := os.ReadFile(curlLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(curls), "/healthz"); got < 7 {
		t.Fatalf("public health count = %d, want initial four, two extras, and redeploy: %s", got, curls)
	}
	if got := strings.Count(string(curls), "https://test.invalid/"); got < 7 ||
		!strings.Contains(string(curls), "https://ra-aaaaaaaa-12345678-a1.gregale.dev/") {
		t.Fatalf("origin route was not checked for each receipt and redeploy continuity: %s", curls)
	}
	if _, err := os.Stat(revokeLog); err != nil {
		t.Fatalf("acceptance token was not revoked: %v", err)
	}

	// A continuity failure stays a hard gate but leaves the exact response
	// status, request id, and bounded problem body in the workflow log.
	continuityCmd := exec.Command("bash", script)
	continuityCmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
		"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
		"TEST_DEPLOY_LOG="+filepath.Join(dir, "continuity-deploys"),
		"TEST_CURL_LOG="+filepath.Join(dir, "continuity-curls"),
		"TEST_REVOKE_LOG="+filepath.Join(dir, "continuity-revokes"), "TEST_REDEPLOY_503=1",
	)
	continuityOutput, err := continuityCmd.CombinedOutput()
	if err == nil || !strings.Contains(string(continuityOutput), "previous serving revision became unavailable") ||
		!strings.Contains(string(continuityOutput), "HTTP/2 503") ||
		!strings.Contains(string(continuityOutput), "failed-probe-1") ||
		!strings.Contains(string(continuityOutput), "previous revision unavailable") {
		t.Fatalf("continuity failure lost its diagnostics: err=%v output=%s", err, continuityOutput)
	}

	// A persistently uncovered node must remain a release failure after the
	// bounded follow-ups, with the temporary credential revoked on exit.
	failDeployLog := filepath.Join(dir, "failed-deploys")
	failRevokeLog := filepath.Join(dir, "failed-revokes")
	failCmd := exec.Command("bash", script)
	failCmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
		"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
		"TEST_DEPLOY_LOG="+failDeployLog, "TEST_CURL_LOG="+filepath.Join(dir, "failed-curls"),
		"TEST_REVOKE_LOG="+failRevokeLog, "TEST_NEVER_READY=1",
	)
	failOutput, err := failCmd.CombinedOutput()
	if err == nil || !strings.Contains(string(failOutput), "did not cover every active node") {
		t.Fatalf("uncovered node did not fail closed: err=%v output=%s", err, failOutput)
	}
	failedDeploys, err := os.ReadFile(failDeployLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(failedDeploys), "-x8\n") || strings.Contains(string(failedDeploys), "-x9\n") ||
		strings.Contains(string(failedDeploys), "redeploy-queued") {
		t.Fatalf("failed gate did not stop at the bound: %s", failedDeploys)
	}
	if _, err := os.Stat(failRevokeLog); err != nil {
		t.Fatalf("failed acceptance did not revoke token: %v", err)
	}
}

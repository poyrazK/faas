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
  logs) echo "build: builder VM exited with status 1" ; exit 0 ;;
  deployment)
    # Keep the simulated rollout open until its continuity probe is observed.
    # An immediate fake completion can otherwise skip the script's probe loop.
    if [[ "${TEST_REDEPLOY_503:-}" == 1 ]]; then
      for ((attempt=0; attempt<1000; attempt++)); do
        if [[ -f "${TEST_CURL_LOG}.continuity" ]]; then break; fi
        sleep 0.01
      done
      if [[ ! -f "${TEST_CURL_LOG}.continuity" ]]; then
        echo "continuity probe was never observed" >&2
        exit 1
      fi
    fi
    # Like the real CLI, deployment wait prints the bare deployment: no app_url.
    printf '{"id":"redeployed","status":"live","rollout_state":"complete","hosting_receipt":{"smoke":{"status":"verified","status_code":200,"path":"/healthz"}}}\n'
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
    if [[ -n "${TEST_INFRA_FAIL_ONCE_SUFFIX:-}" && "$slug" == *"$TEST_INFRA_FAIL_ONCE_SUFFIX" && ! -e "$TEST_DEPLOY_LOG.infra" ]]; then
      : >"$TEST_DEPLOY_LOG.infra"
      printf '{"id":"deployment-%s","status":"failed","rollout_state":"aborted","error":"builderd: vm exit 1","failure_class":"infra"}\n' "$slug"
      exit 1
    fi
    if [[ -n "${TEST_FAIL_SLUG_SUFFIX:-}" && "$slug" == *"$TEST_FAIL_SLUG_SUFFIX" ]]; then
      echo "build: step 4/7 failed: npm ci exited 1" >&2
      printf '{"id":"deployment-%s","status":"failed","rollout_state":"aborted","error":"build failed","error_code":"build_failed"}\n' "$slug"
      exit 1
    fi
    if [[ "$no_wait" == true ]]; then
      printf '{"id":"redeploy-queued"}\n'
    else
      printf '{"id":"deployment-%s","status":"live","rollout_state":"complete","app_url":"https://test.invalid","hosting_receipt":{"smoke":{"status":"verified","status_code":200,"path":"/healthz"}}}\n' "$slug"
    fi
    exit 0 ;;
  app)
    slug="$2"; shift 2
    maintenance=false; streaming=true; websocket=true; route_metrics=true; consumer_auth=optional
    while (($#)); do
      case "$1" in
        --maintenance) maintenance=true ;;
        --no-maintenance) maintenance=false ;;
        --streaming-enabled) streaming=true ;;
        --no-streaming-enabled) streaming=false ;;
        --websocket-enabled) websocket=true ;;
        --no-websocket) websocket=false ;;
        --route-metrics) route_metrics=true ;;
        --no-route-metrics) route_metrics=false ;;
        --consumer-auth-mode) consumer_auth="$2"; shift ;;
      esac
      shift
    done
    printf '{"slug":"%s","maintenance_mode":%s,"streaming_enabled":%s,"websocket_enabled":%s,"route_metrics_enabled":%s,"consumer_auth_mode":"%s"}\n' \
      "$slug" "$maintenance" "$streaming" "$websocket" "$route_metrics" "$consumer_auth"
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
if [[ "${TEST_REDEPLOY_503:-}" == 1 && "$*" == *"ra-"* && "$*" != *"--write-out"* ]]; then
  touch "${TEST_CURL_LOG}.continuity"
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
if [[ "$*" == *"--write-out"* ]]; then
  headers=""; body=""
  while (($#)); do
    case "$1" in
      --dump-header) headers="$2"; shift 2 ;;
      --output) body="$2"; shift 2 ;;
      --write-out) shift 2 ;;
      *) shift ;;
    esac
  done
  count_file="${headers}.count"
  count=0
  if [[ -f "$count_file" ]]; then read -r count <"$count_file"; fi
  count=$((count + 1))
  printf '%s\n' "$count" >"$count_file"
  case "$count" in
    1) status=503; response='{"code":"app_maintenance_mode"}'; extra='Retry-After: 60' ;;
    2|4) status=200; response='{}'; extra='' ;;
    3) status=401; response='{"code":"consumer_key_required"}'; extra='' ;;
    5) status=501; response='{"code":"websocket_not_on_plan"}'; extra='x-faas-error-reason: websocket_not_on_plan' ;;
    *) echo "unexpected policy status request $count" >&2; exit 2 ;;
  esac
  printf 'HTTP/2 %s\r\n%s\r\n\r\n' "$status" "$extra" >"$headers"
  printf '%s' "$response" >"$body"
  printf '%s' "$status"
  exit 0
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

	// A failed first-wave or coverage deployment must name the deployment and
	// carry its receipt and CLI stderr into the workflow log before cleanup
	// deletes them; the gate used to say only that something failed.
	for _, tt := range []struct {
		name, suffix, extraEnv, wantSummary string
	}{
		{name: "first wave", suffix: "-f1", wantSummary: "one or more production acceptance deployments failed"},
		{name: "coverage follow-up", suffix: "-x1", wantSummary: "a production acceptance coverage deployment failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
				"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
				"TEST_DEPLOY_LOG="+filepath.Join(t.TempDir(), "deploys"),
				"TEST_CURL_LOG="+filepath.Join(t.TempDir(), "curls"),
				"TEST_REVOKE_LOG="+filepath.Join(t.TempDir(), "revokes"),
				"TEST_FAIL_SLUG_SUFFIX="+tt.suffix,
			)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("a failed acceptance deployment passed the gate: %s", out)
			}
			for _, want := range []string{
				"acceptance deployment ra-aaaaaaaa-12345678" + tt.suffix + " failed (exit 1)",
				`"status":"failed"`,
				`"error_code":"build_failed"`,
				"npm ci exited 1",
				"--- deployment deployment-ra-aaaaaaaa-12345678" + tt.suffix + " log",
				tt.wantSummary,
			} {
				if !strings.Contains(string(out), want) {
					t.Errorf("failure output is missing %q:\n%s", want, out)
				}
			}
		})
	}

	// production-us rc.243 (hunt #4 H4-12): an infrastructure-class build
	// failure right after node activation is retried once, with its evidence
	// printed; a release defect (build_failed above) is never retried.
	t.Run("infrastructure failure retried once", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "deploys")
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
			"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
			"TEST_DEPLOY_LOG="+log, "TEST_CURL_LOG="+filepath.Join(t.TempDir(), "curls"),
			"TEST_REVOKE_LOG="+filepath.Join(t.TempDir(), "revokes"),
			"TEST_INFRA_FAIL_ONCE_SUFFIX=-f1", "INFRA_RETRY_DELAY_SECONDS=0",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("a once-transient infrastructure failure failed the gate: %v\n%s", err, out)
		}
		for _, want := range []string{"infrastructure-class failure; retrying once", "builderd: vm exit 1", "builder VM exited with status 1"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("retry output is missing %q:\n%s", want, out)
			}
		}
		deployed, _ := os.ReadFile(log)
		if got := strings.Count(string(deployed), "ra-aaaaaaaa-12345678-f1\n"); got != 2 {
			t.Fatalf("infra-failed deploy attempts = %d, want 2: %s", got, deployed)
		}
	})
}

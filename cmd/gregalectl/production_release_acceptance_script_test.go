package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
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
    # Keep the simulated rollout open until every scripted continuity probe
    # is observed. An immediate fake completion can otherwise skip the
    # script's probe loop.
    if [[ -n "${TEST_CONTINUITY:-}" ]]; then
      IFS=, read -ra pattern <<<"$TEST_CONTINUITY"
      seen=0
      for ((attempt=0; attempt<2000; attempt++)); do
        if [[ -f "${TEST_CURL_LOG}.continuity" ]]; then read -r seen <"${TEST_CURL_LOG}.continuity"; fi
        if ((seen >= ${#pattern[@]})); then break; fi
        sleep 0.01
      done
      if ((seen < ${#pattern[@]})); then
        echo "continuity probes observed: ${seen}, want ${#pattern[@]}" >&2
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
    printf '%s\n' "$*" >>"${TEST_APP_LOG:-/dev/null}"
    slug="$2"; shift 2
    maintenance=false; streaming=true; websocket=true; route_metrics=true; consumer_auth=optional
    refuse() {
      if [[ ",${TEST_HIDDEN_CAPS:-}," == *",$1,"* ]]; then
        printf '{"status":403,"code":"capability_not_released","capability":"%s"}\n' "$1"; exit 1
      fi
    }
    while (($#)); do
      case "$1" in
        --route-metrics|--no-route-metrics) refuse route-insights ;;
      esac
      case "$1" in
        --maintenance) maintenance=true ;;
        --no-maintenance) maintenance=false ;;
        --streaming-enabled) streaming=true ;;
        --no-streaming-enabled) streaming=false ;;
        --websocket-enabled) websocket=true ;;
        --no-websocket) websocket=false ;;
        --route-metrics) route_metrics=true ;;
        --no-route-metrics) route_metrics=false ;;
        --consumer-auth-mode)
          if [[ "$2" == required ]]; then refuse api-consumers; fi
          consumer_auth="$2"; shift ;;
      esac
      shift
    done
    printf '{"slug":"%s","maintenance_mode":%s,"streaming_enabled":%s,"websocket_enabled":%s,"route_metrics_enabled":%s,"consumer_auth_mode":"%s"}\n' \
      "$slug" "$maintenance" "$streaming" "$websocket" "$route_metrics" "$consumer_auth"
    exit 0 ;;
  capabilities)
    if [[ -n "${TEST_HIDDEN_CAPS:-}" ]]; then
      printf '{"plan":"scale","capabilities":[],"hidden_capabilities":["%s"]}\n' "${TEST_HIDDEN_CAPS//,/\",\"}"
    else
      printf '{"plan":"scale","capabilities":[]}\n'
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
  'release-acceptance verify-node')
    printf '%s\n' "$*" >>"${TEST_VERIFY_NODE_LOG:-/dev/null}"
    if [[ "${TEST_NODE_NEVER_READY:-}" != 1 && "$*" == *-fsn-2-2* ]]; then
      printf '{"node":"fsn-2.faas","instance":true,"build":true,"ready":true}\n'; exit 0
    fi
    printf '{"node":"fsn-2.faas","instance":true,"build":false,"ready":false}\n'; exit 3 ;;
esac
exit 2
`)
	writeExecutable("curl", `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TEST_CURL_LOG"
# TEST_CONTINUITY scripts the redeploy continuity probes' statuses in order;
# the last one repeats.
if [[ -n "${TEST_CONTINUITY:-}" && "$*" == *"ra-"* && "$*" != *"--write-out"* ]]; then
  count=0
  if [[ -f "${TEST_CURL_LOG}.continuity" ]]; then read -r count <"${TEST_CURL_LOG}.continuity"; fi
  count=$((count + 1))
  printf '%s\n' "$count" >"${TEST_CURL_LOG}.continuity"
  IFS=, read -ra pattern <<<"$TEST_CONTINUITY"
  index=$((count - 1))
  if ((index >= ${#pattern[@]})); then index=$((${#pattern[@]} - 1)); fi
  status="${pattern[$index]}"
  headers=""; body=""
  while (($#)); do
    case "$1" in
      --dump-header) headers="$2"; shift 2 ;;
      --output) body="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  if [[ "$status" == 200 ]]; then
    printf 'HTTP/2 200\r\n\r\n' >"$headers"
    printf 'hello' >"$body"
    exit 0
  fi
  printf 'HTTP/2 %s\r\nx-faas-request-id: failed-probe-%s\r\n\r\n' "$status" "$count" >"$headers"
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
  # Without the consumer auth leg the WebSocket probe is the third request.
  if [[ ",${TEST_HIDDEN_CAPS:-}," == *",api-consumers,"* && "$count" -ge 3 ]]; then count=$((count + 2)); fi
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
	appLog := filepath.Join(dir, "app-policy")
	script := filepath.Join("..", "..", "deploy", "scripts", "production-release-acceptance.sh")
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
		"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
		"TEST_DEPLOY_LOG="+deployLog, "TEST_CURL_LOG="+curlLog, "TEST_REVOKE_LOG="+revokeLog,
		"TEST_APP_LOG="+appLog,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("acceptance script: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "production acceptance passed") {
		t.Fatalf("success report missing: %s", out)
	}
	appCalls, err := os.ReadFile(appLog)
	if err != nil {
		t.Fatal(err)
	}
	assertAcceptancePolicyGuards(t, readRepoFile(t, "deploy", "scripts", "production-release-acceptance.sh"), string(appCalls))
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

	// rc.302/rc.303: consumer auth and route metrics belong to internal
	// capabilities. While one is hidden from the acceptance account apid
	// answers its setting with 403, so that check is skipped and every other
	// policy check still runs.
	for _, tt := range []struct{ hidden, skipped string }{
		{"api-consumers", "consumer auth check skipped"},
		{"route-insights", "route metrics check skipped"},
		{"api-consumers,route-insights", "route metrics check skipped"},
	} {
		hiddenCmd := exec.Command("bash", script)
		hiddenCmd.Env = append(os.Environ(),
			"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
			"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
			"TEST_DEPLOY_LOG="+filepath.Join(t.TempDir(), "deploys"),
			"TEST_CURL_LOG="+filepath.Join(t.TempDir(), "curls"),
			"TEST_REVOKE_LOG="+filepath.Join(t.TempDir(), "revokes"), "TEST_HIDDEN_CAPS="+tt.hidden,
		)
		hiddenOutput, err := hiddenCmd.CombinedOutput()
		if err != nil || !strings.Contains(string(hiddenOutput), tt.skipped) ||
			!strings.Contains(string(hiddenOutput), "production acceptance passed") {
			t.Fatalf("hidden %s failed acceptance: err=%v output=%s", tt.hidden, err, hiddenOutput)
		}
	}

	// The redeploy continuity probe tolerates one isolated failure that an
	// immediate retry clears (rc.307: one 503 in a control-plane Postgres
	// stall). A failed retry or a second failure stays a hard gate, and every
	// failure leaves its status, request id and bounded body in the log.
	for i, tt := range []struct {
		name, pattern string
		wantPass      bool
		want          []string
	}{
		{name: "persistent failure", pattern: "503",
			want: []string{"previous serving revision became unavailable during redeploy (2 failed probes", "HTTP/2 503", "failed-probe-1", "failed-probe-2", "previous revision unavailable"}},
		{name: "isolated failure", pattern: "503,200", wantPass: true,
			want: []string{"continuity probe failed once and recovered on an immediate retry", "failed-probe-1", "production acceptance passed"}},
		{name: "second failure", pattern: "503,200,200,503",
			want: []string{"previous serving revision became unavailable during redeploy (2 failed probes", "failed-probe-1", "failed-probe-4"}},
	} {
		continuityCmd := exec.Command("bash", script)
		continuityCmd.Env = append(os.Environ(),
			"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
			"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
			"TEST_DEPLOY_LOG="+filepath.Join(dir, fmt.Sprintf("continuity-%d-deploys", i)),
			"TEST_CURL_LOG="+filepath.Join(dir, fmt.Sprintf("continuity-%d-curls", i)),
			"TEST_REVOKE_LOG="+filepath.Join(dir, fmt.Sprintf("continuity-%d-revokes", i)),
			"TEST_CONTINUITY="+tt.pattern, "CONTINUITY_PROBE_INTERVAL_SECONDS=0.1",
		)
		continuityOutput, err := continuityCmd.CombinedOutput()
		if (err == nil) != tt.wantPass {
			t.Fatalf("continuity %s: err=%v, want pass=%t\n%s", tt.name, err, tt.wantPass, continuityOutput)
		}
		for _, want := range tt.want {
			if !strings.Contains(string(continuityOutput), want) {
				t.Fatalf("continuity %s output lacks %q:\n%s", tt.name, want, continuityOutput)
			}
		}
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

	// The per-node canary (ACCEPTANCE_NODE) deploys until verify-node proves
	// the node, skips the fleet gate, stays bounded, and fails on any failed
	// deployment, revoking its token every time. Each round deploys both
	// shapes at once, so another node's single build slot cannot take every
	// build: one round, one verify-node, proves a healthy node.
	for _, tt := range []struct {
		name, extraEnv, wantOutput, wantDeployed, notDeployed string
		wantErr                                               bool
		wantVerifies                                          int
	}{
		{name: "node proven", wantOutput: "node canary passed: release=" + strings.Repeat("a", 40) + " node=fsn-2 deployments=2",
			wantDeployed: "-fsn-2-2\n", notDeployed: "-a1\n", wantVerifies: 1},
		{name: "node never proven", extraEnv: "TEST_NODE_NEVER_READY=1", wantErr: true,
			wantOutput: "node canary did not see fsn-2 serve and build", wantDeployed: "-fsn-2-3\n", notDeployed: "-fsn-2-4\n",
			wantVerifies: 2},
		{name: "deployment failed", extraEnv: "TEST_FAIL_SLUG_SUFFIX=-fsn-2-1", wantErr: true,
			wantOutput: "node canary deployment for fsn-2 failed", wantDeployed: "-fsn-2-2\n", notDeployed: "-fsn-2-3\n"},
	} {
		t.Run("node canary "+tt.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "deploys")
			revokes := filepath.Join(t.TempDir(), "revokes")
			verifies := filepath.Join(t.TempDir(), "verifies")
			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=1",
				"GREGALE_BIN="+gregale, "GREGALECTL_BIN="+gregalectl,
				"TEST_DEPLOY_LOG="+log, "TEST_CURL_LOG="+filepath.Join(t.TempDir(), "curls"),
				"TEST_REVOKE_LOG="+revokes, "ACCEPTANCE_NODE=fsn-2", "NODE_CANARY_MAX_DEPLOYMENTS=3",
				"TEST_VERIFY_NODE_LOG="+verifies,
				tt.extraEnv,
			)
			out, err := cmd.CombinedOutput()
			if (err != nil) != tt.wantErr || !strings.Contains(string(out), tt.wantOutput) {
				t.Fatalf("err=%v wantErr=%v, want output %q:\n%s", err, tt.wantErr, tt.wantOutput, out)
			}
			deployed, _ := os.ReadFile(log)
			if !strings.Contains(string(deployed), tt.wantDeployed) || strings.Contains(string(deployed), tt.notDeployed) {
				t.Fatalf("deployments = %q, want %q and not %q", deployed, tt.wantDeployed, tt.notDeployed)
			}
			if _, err := os.Stat(revokes); err != nil {
				t.Fatalf("node canary did not revoke its token: %v", err)
			}
			calls, _ := os.ReadFile(verifies)
			if got := strings.Count(string(calls), "verify-node"); got != tt.wantVerifies {
				t.Fatalf("verify-node ran %d times, want %d: %s", got, tt.wantVerifies, calls)
			}
		})
	}
}

// acceptancePolicySettings maps each `gregale app` flag the acceptance script
// passes to the app setting it turns on (capgate.AppSettings form), or "" for
// a flag that only turns a setting off. A new flag in the script must be added
// here, so its capability is checked below.
var acceptancePolicySettings = map[string]string{
	"--maintenance":                 "app.maintenance_mode",
	"--no-maintenance":              "",
	"--streaming-enabled":           "app.streaming_enabled",
	"--no-streaming-enabled":        "",
	"--websocket-enabled":           "app.websocket_enabled",
	"--no-websocket":                "",
	"--route-metrics":               "app.route_metrics_enabled",
	"--no-route-metrics":            "",
	"--consumer-auth-mode=required": "app.consumer_auth_mode=required",
	"--consumer-auth-mode=optional": "app.consumer_auth_mode=optional",
}

// assertAcceptancePolicyGuards: rc.297, rc.302 and rc.303 failed production
// acceptance on 403 capability_not_released, because the script toggled an
// app setting a preview or internal capability had just come to own, and that
// capability is off for the acceptance account by default
// (ADR-20261023115741). Every setting the script turns on whose catalog owner
// is off by default must sit behind `capability_released <owner>`, so a
// catalog change that gates one fails here instead of in a release.
func assertAcceptancePolicyGuards(t *testing.T, script, appCalls string) {
	t.Helper()
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]productcap.Capability{}
	owners := map[string]string{}
	for _, capability := range catalog.Capabilities {
		byID[capability.ID] = capability
		for _, setting := range capability.Settings {
			owners[setting] = capability.ID
		}
	}
	offByDefault := func(id string) bool {
		for seen := map[string]bool{}; id != "" && !seen[id]; id = byID[id].Parent {
			seen[id] = true
			if m := byID[id].Maturity; m == productcap.MaturityPreview || m == productcap.MaturityInternal {
				return true
			}
		}
		return false
	}
	checked := 0
	for _, call := range strings.Split(strings.TrimSpace(appCalls), "\n") {
		fields := strings.Fields(call)
		for i := 0; i < len(fields); i++ {
			flag := fields[i]
			if !strings.HasPrefix(flag, "--") || flag == "--json" {
				continue
			}
			if flag == "--consumer-auth-mode" && i+1 < len(fields) {
				i++
				flag += "=" + fields[i]
			}
			setting, known := acceptancePolicySettings[flag]
			if !known {
				t.Errorf("acceptance script passes `gregale app %s`; add it to acceptancePolicySettings", flag)
				continue
			}
			if setting == "" {
				continue
			}
			checked++
			owner, owned := owners[setting]
			if !owned {
				field, _, _ := strings.Cut(setting, "=")
				owner, owned = owners[field]
			}
			if owned && offByDefault(owner) && !strings.Contains(script, "capability_released "+owner) {
				t.Errorf("acceptance script turns on %s, which %s owns and releases to no account by default; guard it with `if capability_released %s`", setting, owner, owner)
			}
		}
	}
	if checked == 0 {
		t.Fatal("acceptance script turned on no app policy setting; the app call log is empty")
	}
}

// TestProductionReleaseAcceptanceResolvesRetryBudgetGate: cd-platform pipes
// the script to the control plane over `bash -s`, where the old relative
// deploy/scripts path did not exist, so the shared retry-budget mode could
// only fail after every deployment ran. It now fails before minting a token.
func TestProductionReleaseAcceptanceResolvesRetryBudgetGate(t *testing.T) {
	body := readRepoFile(t, "deploy", "scripts", "production-release-acceptance.sh")
	dir := t.TempDir()
	minted := filepath.Join(dir, "minted")
	gregalectl := filepath.Join(dir, "gregalectl")
	if err := os.WriteFile(gregalectl, []byte("#!/usr/bin/env bash\ntouch \""+minted+"\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-s")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(body)
	cmd.Env = append(os.Environ(),
		"RELEASE_SHA="+strings.Repeat("a", 40), "RUN_ID=12345678", "ACTIVE_NODE_COUNT=2",
		"SHARED_RETRY_BUDGET_REQUIRED=true", "PROMETHEUS_URL=http://127.0.0.1:9095",
		"GREGALE_BIN="+gregalectl, "GREGALECTL_BIN="+gregalectl,
	)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "shared retry-budget gate is missing") {
		t.Fatalf("piped script did not refuse the missing retry-budget gate: err=%v output=%s", err, out)
	}
	if _, err := os.Stat(minted); err == nil {
		t.Fatal("the retry-budget gate was checked only after minting an acceptance token")
	}
}

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCDComputeWorkflowSupportsPrepareThenActivate(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	for _, required := range []string{
		"rollout_phase:",
		"default: full",
		"ROLLOUT_PHASE: ${{ inputs.rollout_phase }}",
		"prepare) JOIN_ARGS+=(--prepare-only)",
		"activate) JOIN_ARGS+=(--activate-prepared)",
		"if: inputs.rollout_phase != 'activate'",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("phased compute rollout is missing %q", required)
		}
	}
	for _, step := range []string{
		"Verify compute registry lifecycle authorization",
		"Verify compute OCI cache uses fast storage after activation",
		"Verify guest service proxy listener after activation",
		"Verify imaged public-smoke tenant routing after activation",
		"Verify private compute gateway reachability",
	} {
		start := strings.Index(workflow, "- name: "+step)
		if start < 0 {
			t.Errorf("missing post-activation step %q", step)
			continue
		}
		end := strings.Index(workflow[start:], "\n      - name:")
		if end < 0 {
			end = len(workflow) - start
		}
		if !strings.Contains(workflow[start:start+end], "if: inputs.rollout_phase != 'prepare'") {
			t.Errorf("post-activation step %q does not skip preparation", step)
		}
	}
}

func TestCDComputeWorkflowRequiresExplicitFleetPreflightSkip(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)

	input := regexp.MustCompile(`(?ms)^      skip_fleet_preflight:\n(?:        .*\n)*?        default: false\n(?:        .*\n)*?        type: boolean$`)
	if !input.MatchString(workflow) {
		t.Fatal("cd-compute skip_fleet_preflight input must be boolean and default false")
	}
	if !strings.Contains(workflow, `SKIP_FLEET_PREFLIGHT: ${{ inputs.skip_fleet_preflight }}`) {
		t.Fatal("cd-compute workflow does not pass the explicit input to the adoption step")
	}

	guard := regexp.MustCompile(`(?ms)if \[\[ "\$SKIP_FLEET_PREFLIGHT" == "true" \]\]; then\n(?:            .*\n)*?            JOIN_ARGS\+\=\(--skip-fleet-preflight\)\n          fi`)
	if !guard.MatchString(workflow) {
		t.Fatal("cd-compute workflow must guard --skip-fleet-preflight behind an explicit true input")
	}
	if got := strings.Count(workflow, "JOIN_ARGS+=(--skip-fleet-preflight)"); got != 1 {
		t.Fatalf("cd-compute workflow has %d fleet-preflight skip arguments, want exactly 1", got)
	}
}

func TestCDComputeWorkflowDownloadsCanonicalAssetsFromOneLookupInParallel(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	start := strings.Index(workflow, "- name: Download and verify the canonical release")
	end := strings.Index(workflow, "- name: Assemble the standard join artifact directory")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("cannot isolate the canonical release download step")
	}
	download := workflow[start:end]
	for _, want := range []string{
		`-o "$release_json" "$release_api"`,
		`jq -er --arg asset "$asset"`,
		`download_asset "$asset" &`,
		`download_pids+=("$!")`,
		`if ! wait "$pid"; then`,
		"one or more canonical release assets failed to download",
	} {
		if !strings.Contains(download, want) {
			t.Errorf("parallel canonical release download is missing %q", want)
		}
	}
	if got := strings.Count(download, "/releases/tags/${RELEASE_TAG}"); got != 1 {
		t.Fatalf("canonical release metadata lookups = %d, want 1", got)
	}
}

func TestCDComputeWorkflowUsesInfrastructureHealthHost(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)

	if !strings.Contains(workflow, `--header 'Host: gatewayd-internal.faas'`) {
		t.Fatal("cd-compute private reachability probe must use the gateway infrastructure health host")
	}
	if strings.Contains(workflow, `--header 'Host: health-probe.invalid'`) {
		t.Fatal("cd-compute private reachability probe must not route health checks through the unknown-app path")
	}
	if !strings.Contains(workflow, `grep -q '^# HELP gateway_compute_node_changed_subscriber_alive '`) {
		t.Fatal("cd-compute metrics probe must require a family registered by gatewayd-internal")
	}
	if strings.Contains(workflow, "gatewayd_ops_total") {
		t.Fatal("cd-compute metrics probe must not require the removed gatewayd_ops_total family")
	}
}

func TestCDComputeWorkflowVerifiesFastCacheAfterActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	join := strings.Index(workflow, `"$ARTIFACT_DIR/gregalectl-linux-amd64" "${JOIN_ARGS[@]}"`)
	cacheGate := strings.Index(workflow, "Verify compute OCI cache uses fast storage after activation")
	gatewayGate := strings.Index(workflow, "Verify private compute gateway reachability")
	if join < 0 || cacheGate < 0 || gatewayGate < 0 || !(join < cacheGate && cacheGate < gatewayGate) {
		t.Fatalf("fast-cache post gate order is invalid: join=%d cache=%d gateway=%d", join, cacheGate, gatewayGate)
	}
	for _, want := range []string{
		"mountpoint -q /var/lib/faas/cache",
		"stat -c %d /var/lib/faas/cache",
		"stat -c %d /srv/fc",
		"findmnt -n -o FSTYPE --mountpoint /var/lib/faas/cache",
		"xfs_info /srv/fc",
		"reflink=1",
		"test -g /var/lib/faas/cache",
		"-mindepth 1",
		"! -group faas",
		"! -perm -g+w",
		"cache permission contract violation",
	} {
		if !strings.Contains(workflow[cacheGate:gatewayGate], want) {
			t.Errorf("fast-cache post gate is missing %q", want)
		}
	}
	if strings.Contains(workflow[cacheGate:gatewayGate], "! -perm -2000") {
		t.Fatal("fast-cache post gate must not require setgid on restricted-daemon child directories")
	}
}

func TestCDComputeWorkflowVerifiesGuestServiceProxyAfterActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(body)
	join := strings.Index(workflow, `"$ARTIFACT_DIR/gregalectl-linux-amd64" "${JOIN_ARGS[@]}"`)
	proxyGate := strings.Index(workflow, "Verify guest service proxy listener after activation")
	if join < 0 || proxyGate < 0 || join >= proxyGate {
		t.Fatalf("guest service proxy gate must follow activation: join=%d gate=%d", join, proxyGate)
	}
	for _, want := range []string{"service_proxy_listen", `10\.100\.0\.1:10080`, "10.100.0.1:53", "ss -ltnH", "ss -lunH"} {
		if !strings.Contains(workflow[proxyGate:], want) {
			t.Errorf("guest service proxy gate is missing %q", want)
		}
	}
}

func TestCDComputeWorkflowVerifiesHostingSmokeTenantRouting(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-compute.yml"))
	if err != nil {
		t.Fatalf("read cd-compute workflow: %v", err)
	}
	workflow := string(body)
	join := strings.Index(workflow, `"$ARTIFACT_DIR/gregalectl-linux-amd64" "${JOIN_ARGS[@]}"`)
	smokeGate := strings.Index(workflow, "Verify imaged public-smoke tenant routing after activation")
	gatewayGate := strings.Index(workflow, "Verify private compute gateway reachability")
	if join < 0 || smokeGate < 0 || gatewayGate < 0 || !(join < smokeGate && smokeGate < gatewayGate) {
		t.Fatalf("public-smoke config gate order is invalid: join=%d smoke=%d gateway=%d", join, smokeGate, gatewayGate)
	}
	for _, want := range []string{
		"systemctl show faas-imaged.service --property=Environment --value",
		"FAAS_API_HOSTING_SMOKE_REQUIRED=1",
		"FAAS_API_HOSTING_SMOKE_URL=https://",
		"FAAS_APPS_DOMAIN=",
	} {
		if !strings.Contains(workflow[smokeGate:gatewayGate], want) {
			t.Errorf("public-smoke config gate is missing %q", want)
		}
	}

	template, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "compute_only_service", "templates", "zz-faas-api-hosting-smoke.conf.j2"))
	if err != nil {
		t.Fatalf("read hosting-smoke drop-in: %v", err)
	}
	if !strings.Contains(string(template), "Environment=FAAS_APPS_DOMAIN={{ gatewayd_apps_domain | default('gregale.dev') }}") {
		t.Fatal("compute hosting-smoke drop-in does not render the tenant apps domain")
	}
}

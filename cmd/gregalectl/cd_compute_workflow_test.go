package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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
	} {
		if !strings.Contains(workflow[cacheGate:gatewayGate], want) {
			t.Errorf("fast-cache post gate is missing %q", want)
		}
	}
}

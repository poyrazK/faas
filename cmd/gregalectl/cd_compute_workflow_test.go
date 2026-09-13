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
}

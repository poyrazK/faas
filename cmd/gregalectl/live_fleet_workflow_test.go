package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestWorkflowsTargetTheLiveFleet: the EU `production` fleet was retired on
// 2026-10-04 (its GCP project is suspended and its runners are gone). A
// dispatch left on that default queued forever on offline runners, and the
// synthetic canary probed a host that no longer existed, failing every run.
// `production` stays selectable for manual dispatch only.
func TestWorkflowsTargetTheLiveFleet(t *testing.T) {
	retiredDefault := regexp.MustCompile(`(?m)^\s+deploy_environment:\s*\n(?:\s+[a-z_]+:.*\n)*?\s+default: production\s*$`)
	for _, name := range []string{"cd-platform.yml", "cd-controlplane.yml", "cd-compute.yml", "pki-renew.yml"} {
		workflow := readWorkflow(t, name)
		if retiredDefault.MatchString(workflow) {
			t.Errorf("%s still defaults deploy_environment to the retired production fleet", name)
		}
		if !strings.Contains(workflow, "default: production-us") {
			t.Errorf("%s does not default deploy_environment to production-us", name)
		}
	}
	canary := readWorkflow(t, "synthetic-canary.yml")
	for _, want := range []string{"environment: production-us", "runs-on: [self-hosted, linux, faas-fleet-us]"} {
		if !strings.Contains(canary, want) {
			t.Errorf("synthetic-canary.yml is missing %q", want)
		}
	}
}

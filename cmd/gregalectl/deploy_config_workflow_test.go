package main

import (
	"strings"
	"testing"
)

// TestDeployConfigGatesRunOnTheirPullRequests: the gates that parse, lint
// and render deploy/ansible, the Prometheus rules and the nftables renderers
// run on every pull request that changes their inputs (deploy-config.yml),
// not only nightly (ci.yml), and both workflows run the same commands.
func TestDeployConfigGatesRunOnTheirPullRequests(t *testing.T) {
	pr := readWorkflow(t, "deploy-config.yml")
	nightly := readWorkflow(t, "ci.yml")
	paths := pr[strings.Index(pr, "  pull_request:\n"):strings.Index(pr, "  workflow_dispatch:")]
	for _, input := range []string{
		`"deploy/ansible/**"`,
		`"pkg/promqlrules/**"`,
		`"pkg/netns/**"`,
		`"scripts/ci/deploy_config_gates.sh"`,
		`"scripts/ci/check_alertmanager_config.sh"`,
		`"scripts/egress-render-cross-check.sh"`,
		`".github/workflows/deploy-config.yml"`,
	} {
		if !strings.Contains(paths, "      - "+input+"\n") {
			t.Errorf("deploy-config.yml does not run when %s changes", input)
		}
	}
	for _, gate := range []string{
		"scripts/ci/deploy_config_gates.sh promtool-install",
		"scripts/ci/deploy_config_gates.sh rules",
		"scripts/ci/deploy_config_gates.sh syntax",
		"scripts/ci/deploy_config_gates.sh templates",
		"make ansible-lint",
		"make prometheus-alert-metadata-check",
		"make alertmanager-config-check",
		"make egress-check",
	} {
		if !strings.Contains(pr, gate) {
			t.Errorf("deploy-config.yml does not run %q", gate)
		}
		if !strings.Contains(nightly, gate) {
			t.Errorf("ci.yml does not run %q", gate)
		}
	}
}

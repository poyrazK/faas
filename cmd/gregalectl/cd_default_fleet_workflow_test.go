package main

import (
	"regexp"
	"testing"
)

// TestCDWorkflowsDefaultToTheLiveFleet: the EU `production` fleet was retired
// on 2026-10-04 and its `faas-fleet` runners are gone. A dispatch that kept
// the `production` default queued forever on those offline runners, and only
// a force-cancel cleared it. `production` stays selectable; it is no longer
// the default of any deploy_environment input.
func TestCDWorkflowsDefaultToTheLiveFleet(t *testing.T) {
	input := regexp.MustCompile(`(?m)^      deploy_environment:\n(?:        .*\n|          .*\n)*?        default: (\S+)\n`)
	for _, name := range []string{
		"cd-platform.yml", "cd-controlplane.yml", "cd-compute.yml",
		"cd-edge-node.yml", "cd-edge-units.yml", "cd-unit-watch.yml", "pki-renew.yml",
	} {
		defaults := input.FindAllStringSubmatch(readWorkflow(t, name), -1)
		if len(defaults) == 0 {
			t.Errorf("%s has no deploy_environment input default", name)
		}
		for _, match := range defaults {
			if match[1] != "production-us" {
				t.Errorf("%s defaults deploy_environment to %q, want production-us", name, match[1])
			}
		}
	}
}

package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestCDControlPlaneKeepsUnchangedHandoffInputs: deployctl hands a daemon off
// (zero-downtime reload) only when none of its unit inputs (unit file,
// drop-ins, environment files, credential sources) is newer than the running
// process. rc.281's rollout restarted apid instead because cd-controlplane
// rewrote its credentials drop-in with identical bytes on every deploy; one
// API probe timed out during the restart and the rollout gate failed. Host
// files under /etc/systemd/system and /etc/faas must only be rewritten when
// their content changes.
func TestCDControlPlaneKeepsUnchangedHandoffInputs(t *testing.T) {
	workflow := readWorkflow(t, "cd-controlplane.yml")
	if m := regexp.MustCompile(`(?m)^\s*cat > /etc/(systemd/system|faas)/\S+`).FindString(workflow); m != "" {
		t.Errorf("cd-controlplane.yml truncates a host file in place on every deploy: %q", strings.TrimSpace(m))
	}
	for _, path := range []string{
		"/etc/faas/secrets/fleet.age",
		"/etc/faas/secrets/fleet.age.pub",
		"/etc/faas/secrets/sign-pub.pem",
		"/etc/faas/apid-age-recipients.env",
		"/etc/systemd/system/faas-apid.service.d/98-age-recipient-credentials.conf",
		"/etc/systemd/system/faas-schedd.service.d/98-signing-public-key-credential.conf",
		"/etc/systemd/system/faas-githubd.service.d/98-fleet-recipient-credential.conf",
	} {
		if !regexp.MustCompile(`(install|write)_if_changed \d{4} (\S+ )?` + regexp.QuoteMeta(path) + `\b`).MatchString(workflow) {
			t.Errorf("%s is not written with install_if_changed/write_if_changed", path)
		}
	}
	if !strings.Contains(workflow, `cmp -s "$remote_stage" /etc/faas/apid-storage.env`) ||
		!strings.Contains(workflow, `cmp -s "$dropin_stage" "$dropin"`) {
		t.Error("apid's storage env file and drop-in must only be replaced when their content changes")
	}
}

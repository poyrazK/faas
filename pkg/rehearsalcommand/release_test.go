// adr: 20261020150000
package rehearsalcommand

import (
	"strings"
	"testing"
)

func TestContainedReleasePinsTheDeploymentsExactCommand(t *testing.T) {
	run, dep := strings.Repeat("a", 32), "11111111-2222-3333-4444-555555555555"
	for _, tc := range []struct {
		name    string
		command []string
		shell   bool
		argv    []string
	}{
		{"argv", []string{"node", "migrate.js", "--up"}, false, []string{"node", "migrate.js", "--up"}},
		{"shell", []string{"npm run migrate && echo done"}, true, []string{"/bin/sh", "-lc", "npm run migrate && echo done"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope, err := ContainedRelease(run, dep, tc.command, tc.shell)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := Decode(envelope)
			if err != nil || spec.RunID != run || spec.DeploymentID != dep || strings.Join(spec.Command, "\x00") != strings.Join(tc.argv, "\x00") || len(spec.Env) != 0 || len(spec.Files) != 0 {
				t.Fatalf("pinned release = %+v, %v", spec, err)
			}
			if !IsContainedReleaseFor(envelope, dep, tc.command, tc.shell) {
				t.Fatal("pinned release does not match its deployment")
			}
			if IsContainedReleaseFor(envelope, "99999999-2222-3333-4444-555555555555", tc.command, tc.shell) ||
				IsContainedReleaseFor(envelope, dep, append(append([]string{}, tc.command...), "--extra"), tc.shell) ||
				IsContainedReleaseFor(envelope, dep, tc.command, !tc.shell) || IsContainedReleaseFor(tc.command, dep, tc.command, tc.shell) {
				t.Fatal("a different deployment, command, shell mode or raw argv matched the pinned release")
			}
		})
	}
	withEnv, err := Encode(Spec{Version: Version, RunID: run, DeploymentID: dep, Command: []string{"node", "migrate.js"}, Env: map[string]string{"EXTRA": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if IsContainedReleaseFor(withEnv, dep, []string{"node", "migrate.js"}, false) {
		t.Fatal("a recipe command with extra env was accepted as the deployment's release")
	}
	if _, err := ContainedRelease(run, dep, nil, true); err == nil {
		t.Fatal("an empty shell release encoded")
	}
}

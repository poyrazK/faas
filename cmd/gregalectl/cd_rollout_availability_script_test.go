package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRolloutAvailabilitySeparatesStatusAndCustomerPaths(t *testing.T) {
	for _, tc := range []struct {
		name               string
		failTarget         string
		baselineFailTarget string
		withoutOptional    bool
		commandExit        int
		wantExit           int
		wantText           string
	}{
		{name: "status timeout is diagnostic", failTarget: "status", wantExit: 0, wantText: "status: HTTP status counts: 000"},
		{name: "canary app failure gates", failTarget: "app", wantExit: 1, wantText: "public readiness or canary app failed after a healthy pre-rollout baseline"},
		{name: "API readiness failure gates", failTarget: "api", wantExit: 1, wantText: "public readiness or canary app failed after a healthy pre-rollout baseline"},
		{name: "unhealthy app baseline is inconclusive", baselineFailTarget: "app", wantExit: 0, wantText: "app: rollout attribution is **inconclusive**"},
		{name: "API-only observer supports empty optional targets", withoutOptional: true, wantExit: 0, wantText: "api: HTTP status counts: 200"},
		{name: "wrapped command failure wins", failTarget: "app", commandExit: 17, wantExit: 17, wantText: "app: HTTP status counts: 000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			fakeCurl := `#!/usr/bin/env bash
set -euo pipefail
url="${@: -1}"
case "$url" in
  */readyz?*) target=api ;;
  */v1/status?*) target=status ;;
  *) target=app ;;
esac
if [[ "$target" == "$FAKE_BASELINE_FAIL_TARGET" || ( -f "$FAKE_ROLLOUT_MARKER" && "$target" == "$FAKE_FAIL_TARGET" ) ]]; then
  printf '000\t3.000\t0.001\t0.000'
  echo 'curl: (28) Operation timed out' >&2
  exit 28
fi
printf '200\t0.010\t0.001\t0.005'
`
			if err := os.WriteFile(filepath.Join(temp, "curl"), []byte(fakeCurl), 0755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(temp, "mutating-command-started")
			script := filepath.Join("..", "..", "scripts", "ci", "observe_rollout_availability.sh")
			command := exec.Command("bash", script, "https://api.example/readyz", "--",
				"sh", "-c", "touch \"$1\"; sleep 0.6; exit \"$2\"", "sh", marker, strconv.Itoa(tc.commandExit))
			command.Env = append(os.Environ(),
				"PATH="+temp+":"+os.Getenv("PATH"),
				"RUNNER_TEMP="+temp,
				"ROLLOUT_BASELINE_SAMPLE_COUNT=1",
				"ROLLOUT_PROBE_INTERVAL_SECONDS=0.02",
				"FAKE_ROLLOUT_MARKER="+marker,
				"FAKE_FAIL_TARGET="+tc.failTarget,
				"FAKE_BASELINE_FAIL_TARGET="+tc.baselineFailTarget,
			)
			if !tc.withoutOptional {
				command.Env = append(command.Env,
					"ROLLOUT_APP_PROBE_URL=https://app.example/",
					"ROLLOUT_STATUS_PROBE_URL=https://api.example/v1/status",
				)
			}
			output, err := command.CombinedOutput()
			gotExit := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					gotExit = exitErr.ExitCode()
				} else {
					t.Fatalf("run observer: %v", err)
				}
			}
			if gotExit != tc.wantExit {
				t.Fatalf("observer exit = %d, want %d; output:\n%s", gotExit, tc.wantExit, output)
			}
			if !strings.Contains(string(output), tc.wantText) {
				t.Fatalf("observer output missing %q:\n%s", tc.wantText, output)
			}
			if tc.failTarget == "status" {
				log, readErr := os.ReadFile(filepath.Join(temp, "gregale-rollout-availability-local-1-status.tsv"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !strings.Contains(string(log), "\t28\tcurl: (28) Operation timed out") {
					t.Fatalf("status evidence lacks curl exit and error: %s", log)
				}
			}
		})
	}
}

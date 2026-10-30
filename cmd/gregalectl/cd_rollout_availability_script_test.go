package main

import (
	"errors"
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
		failures           string // target:sequence:result rollout samples to fail (result 28 = timeout)
		finalFailTarget    string // fail every sample of this target after the command finishes
		commandExit        int
		wantExit           int
		wantText           string
		wantWarning        string
		wantTolerated      string
	}{
		{name: "status timeout is diagnostic", failTarget: "status", wantExit: 0, wantText: "status: HTTP status counts: 000"},
		{name: "canary app failure gates", failTarget: "app", wantExit: 1, wantText: "public readiness or canary app failed after a healthy pre-rollout baseline"},
		{name: "API readiness failure gates", failTarget: "api", wantExit: 1, wantText: "public readiness or canary app failed after a healthy pre-rollout baseline"},
		{name: "unhealthy app baseline is inconclusive", baselineFailTarget: "app", wantExit: 0, wantText: "app: rollout attribution is **inconclusive**",
			wantWarning: "app gate gave this activation no signal: its baseline was 0/1 healthy (HTTP 000: 1). Check that https://app.example/ still serves. ROLLOUT_APP_PROBE_URL names the canary app."},
		{name: "API-only observer supports empty optional targets", withoutOptional: true, wantExit: 0, wantText: "api: HTTP status counts: 200"},
		{name: "wrapped command failure wins", failTarget: "app", commandExit: 17, wantExit: 17, wantText: "app: HTTP status counts: 000"},
		// rc.304, rc.305 and rc.307 failed activation on one or two 3 s API
		// timeouts while the bundle unpacked; isolated timeouts are tolerated.
		{name: "isolated API timeouts are tolerated", failures: "api:3:28,api:4:28", wantExit: 0, wantText: "api: HTTP status counts: 000: 2",
			wantTolerated: "api tolerated 2 isolated timed-out sample(s)"},
		{name: "three API timeouts in a row gate", failures: "api:3:28,api:4:28,api:5:28", wantExit: 1,
			wantText: "api: gate failed: 3 consecutive failed samples (budget 2)."},
		{name: "API timeouts over the budget gate", failures: "api:2:28,api:3:28,api:5:28,api:6:28", wantExit: 1,
			wantText: "api: gate failed: 4 timed-out samples (budget 3)."},
		{name: "one API HTTP error still gates", failures: "api:3:503", wantExit: 1,
			wantText: "api: gate failed: 1 failure(s) other than a timeout."},
		{name: "timed-out final API sample gates", finalFailTarget: "api", wantExit: 1, wantText: "the final sample failed"},
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
timeout() {
  printf '000\t3.000\t0.001\t0.000'
  echo 'curl: (28) Operation timed out' >&2
  exit 28
}
if [[ "$target" == "$FAKE_BASELINE_FAIL_TARGET" || ( -f "$FAKE_ROLLOUT_MARKER" && "$target" == "$FAKE_FAIL_TARGET" ) ]]; then
  timeout
fi
if [[ -f "$FAKE_DONE_MARKER" && "$target" == "$FAKE_FINAL_FAIL_TARGET" ]]; then
  timeout
fi
if [[ -f "$FAKE_ROLLOUT_MARKER" ]]; then
  case ",$FAKE_FAILURES," in
    *",$target:${url##*-}:28,"*) timeout ;;
    *",$target:${url##*-}:503,"*) printf '503\t0.010\t0.001\t0.005'; exit 0 ;;
  esac
fi
printf '200\t0.010\t0.001\t0.005'
`
			if err := os.WriteFile(filepath.Join(temp, "curl"), []byte(fakeCurl), 0755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(temp, "mutating-command-started")
			done := filepath.Join(temp, "mutating-command-finished")
			script := filepath.Join("..", "..", "scripts", "ci", "observe_rollout_availability.sh")
			command := exec.Command("bash", script, "https://api.example/readyz", "--",
				"sh", "-c", "touch \"$1\"; sleep 1; touch \"$3\"; exit \"$2\"", "sh", marker, strconv.Itoa(tc.commandExit), done)
			command.Env = append(os.Environ(),
				"PATH="+temp+":"+os.Getenv("PATH"),
				"RUNNER_TEMP="+temp,
				"GITHUB_RUN_ID=local",
				"GITHUB_RUN_ATTEMPT=1",
				"ROLLOUT_BASELINE_SAMPLE_COUNT=1",
				"ROLLOUT_PROBE_INTERVAL_SECONDS=0.02",
				"FAKE_ROLLOUT_MARKER="+marker,
				"FAKE_FAIL_TARGET="+tc.failTarget,
				"FAKE_BASELINE_FAIL_TARGET="+tc.baselineFailTarget,
				"FAKE_FAILURES="+tc.failures,
				"FAKE_DONE_MARKER="+done,
				"FAKE_FINAL_FAIL_TARGET="+tc.finalFailTarget,
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
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
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
			// A gate with an unhealthy baseline is annotated and called out in
			// the step summary, so a vanished canary does not pass unnoticed.
			if tc.wantTolerated != "" {
				for _, want := range []string{"::warning title=Rollout gate tolerated timeouts::" + tc.wantTolerated, "> [!NOTE]\n> " + tc.wantTolerated} {
					if !strings.Contains(string(output), want) {
						t.Fatalf("observer output missing %q:\n%s", want, output)
					}
				}
			} else if strings.Contains(string(output), "tolerated timeouts") {
				t.Fatalf("observer tolerated timeouts it should not have:\n%s", output)
			}
			if tc.wantWarning == "" {
				if strings.Contains(string(output), "[!WARNING]") || strings.Contains(string(output), "::warning title=Rollout gate baseline unhealthy") {
					t.Fatalf("observer warned about a healthy baseline:\n%s", output)
				}
			} else {
				for _, want := range []string{"::warning title=Rollout gate baseline unhealthy::" + tc.wantWarning, "> [!WARNING]\n> " + tc.wantWarning} {
					if !strings.Contains(string(output), want) {
						t.Fatalf("observer output missing %q:\n%s", want, output)
					}
				}
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

// A rollout without a healthy baseline cannot tell an outage it caused from
// one it walked into, so the observer refuses to run one (ADR-202610171900).
func TestRolloutAvailabilityRefusesAnEmptyBaseline(t *testing.T) {
	for _, count := range []string{"0", "-1", "three"} {
		t.Run(count, func(t *testing.T) {
			temp := t.TempDir()
			marker := filepath.Join(temp, "mutating-command-started")
			script := filepath.Join("..", "..", "scripts", "ci", "observe_rollout_availability.sh")
			command := exec.Command("bash", script, "https://api.example/readyz", "--", "touch", marker)
			command.Env = append(os.Environ(), "RUNNER_TEMP="+temp, "ROLLOUT_BASELINE_SAMPLE_COUNT="+count)
			output, err := command.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("observer with baseline %q: err=%v, want exit 2\n%s", count, err, output)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("observer ran the rollout command without a baseline: %v", err)
			}
		})
	}
}

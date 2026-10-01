package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRemoteRolloutPreservesActivationAndProbeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, activation, failTarget, baselineFail, wantActivated string
		wantExit                                                  int
	}{
		{"healthy", "touch \"$ROLLOUT_ACTIVATION_MARKER\"; sleep 0.4", "", "", "true", 0},
		{"preactivation failure", "exit 17", "", "", "false", 17},
		{"postactivation failure", "touch \"$ROLLOUT_ACTIVATION_MARKER\"; exit 19", "", "", "true", 19},
		{"customer regression", "touch \"$ROLLOUT_ACTIVATION_MARKER\"; sleep 0.4", "app", "", "true", 1},
		{"unhealthy API never activates", "touch \"$ROLLOUT_ACTIVATION_MARKER\"", "", "api", "false", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			writeRolloutExecutable(t, temp, "ssh", `#!/usr/bin/env bash
set -euo pipefail
while [[ "$1" == -* ]]; do
  case "$1" in -i|-o) shift 2 ;; *) echo 'forwarding is disabled' >&2; exit 99 ;; esac
done
shift
exec bash -c "$1"
`)
			writeRolloutExecutable(t, temp, "scp", `#!/usr/bin/env bash
set -euo pipefail
recursive=false
while [[ "$1" == -* ]]; do
  case "$1" in -i|-o) shift 2 ;; -r) recursive=true; shift ;; *) exit 99 ;; esac
done
args=()
for arg in "$@"; do args+=("${arg#fake:}"); done
if [[ "$recursive" == true ]]; then cp -R "${args[@]}"; else cp "${args[@]}"; fi
`)
			writeRolloutExecutable(t, temp, "curl", `#!/usr/bin/env bash
set -euo pipefail
url="${@: -1}"
case "$url" in */readyz?*) target=api ;; */v1/status?*) target=status ;; *) target=app ;; esac
if [[ "$target" == "$FAKE_BASELINE_FAIL" || ( -f "$ROLLOUT_ACTIVATION_MARKER" && "$target" == "$FAKE_FAIL_TARGET" ) ]]; then
  printf '503\t0.01\t0.001\t0.005'
else
  printf '200\t0.01\t0.001\t0.005'
fi
`)
			activation := filepath.Join(temp, "activation.sh")
			if err := os.WriteFile(activation, []byte("#!/bin/bash\nset -eu\n"+tc.activation+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			outputPath := filepath.Join(temp, "output")
			command := exec.Command("bash", "scripts/ci/observe_remote_rollout.sh", "fake", "unused-key", activation)
			command.Dir = filepath.Join("..", "..")
			command.Env = append(os.Environ(), "PATH="+temp+":"+os.Getenv("PATH"), "RUNNER_TEMP="+temp,
				"GITHUB_RUN_ID=local", "GITHUB_RUN_ATTEMPT=1", "GITHUB_OUTPUT="+outputPath,
				"GITHUB_STEP_SUMMARY="+filepath.Join(temp, "summary"), "ROLLOUT_BASELINE_SAMPLE_COUNT=1",
				"FAKE_FAIL_TARGET="+tc.failTarget, "FAKE_BASELINE_FAIL="+tc.baselineFail)
			output, err := command.CombinedOutput()
			if got := rolloutExitCode(t, err); got != tc.wantExit {
				t.Fatalf("exit=%d, want=%d: %s", got, tc.wantExit, output)
			}
			activationOutput, err := os.ReadFile(outputPath)
			if err != nil || string(activationOutput) != "activated="+tc.wantActivated+"\n" {
				t.Fatalf("activation output=%q, err=%v", activationOutput, err)
			}
			evidence, err := os.ReadFile(filepath.Join(temp, "gregale-rollout-availability-local-1-api.tsv"))
			if err != nil || !strings.Contains(string(evidence), "baseline\tapi") {
				t.Fatalf("baseline evidence missing: %s, %v", evidence, err)
			}
		})
	}
}

func TestPlatformRollbackRequiresThisRunsActivation(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-platform.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ Name, Run string }
		}
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range workflow.Jobs["verify"].Steps {
		if step.Name == "Roll back the platform release on gate failure" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("rollback step missing")
	}
	for _, tc := range []struct {
		name, activated, active string
		wantRollback            bool
	}{
		{"no activation", "false", "/opt/faas/releases/selected", false},
		{"output unavailable", "", "/opt/faas/releases/selected", false},
		{"another release active", "true", "/opt/faas/releases/other", false},
		{"confirmed selected activation", "true", "/opt/faas/releases/selected", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			writeRolloutExecutable(t, temp, "ssh", `#!/bin/bash
case "${@: -1}" in
  'readlink -f /opt/faas/current') printf '%s\n' "$FAKE_ACTIVE_RELEASE" ;;
  '/opt/faas/current/bin/deployctl rollback') touch "$FAKE_ROLLBACK_MARKER" ;;
  *) exit 99 ;;
esac
`)
			marker := filepath.Join(temp, "rolled-back")
			command := exec.Command("bash", "-c", script)
			command.Env = append(os.Environ(), "PATH="+temp+":"+os.Getenv("PATH"), "CONTROL_ACTIVATED="+tc.activated,
				"DESIRED_RELEASE=selected", "CP_HOST=fake", "FAKE_ACTIVE_RELEASE="+tc.active, "FAKE_ROLLBACK_MARKER="+marker)
			output, err := command.CombinedOutput()
			if rolloutExitCode(t, err) != 1 {
				t.Fatalf("failed rollout must remain failed: %s", output)
			}
			_, err = os.Stat(marker)
			if got := err == nil; got != tc.wantRollback {
				t.Fatalf("rollback=%v, want=%v: %s", got, tc.wantRollback, output)
			}
		})
	}
}

func writeRolloutExecutable(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}

func rolloutExitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return exitErr.ExitCode()
}

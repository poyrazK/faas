package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataAPIReleaseAggregatesFreshEvidenceAndBlocksDependentStages(t *testing.T) {
	contract, err := parseDataAPIContract(dataAPIContractFixture(t, dataAPIDiffFixture()))
	if err != nil {
		t.Fatal(err)
	}
	config := dataAPIReleaseConfig{Directory: t.TempDir(), Baseline: "baseline", CurrentSchema: "current", Checks: "checks", Collection: "requests", PlanBaseline: "plans", Budget: "budget", CoveragePolicy: "policy"}
	commands := []string{"data-api migration preview", "data-api verify", "data-api explain", "data-api verify coverage"}
	for _, failure := range []int{-1, 0, 1, 2, 3} {
		t.Run("stage_"+string(rune('A'+failure+1)), func(t *testing.T) {
			calls := 0
			runner := func(ctx context.Context, args []string) ([]byte, error) {
				stage := calls
				calls++
				if stage == 3 {
					for _, flag := range []string{"--verification", "--plans"} {
						for i, arg := range args {
							if arg == flag {
								raw, err := os.ReadFile(args[i+1])
								if err != nil || !json.Valid(raw) {
									t.Fatal("coverage did not receive fresh temporary reports")
								}
							}
						}
					}
				}
				report := map[string]any{"version": 1, "command": commands[stage], "status": "passed", "cleanup": "passed", "schema_fingerprint": contract.Fingerprint}
				if stage == 0 {
					delete(report, "cleanup")
					report["steps"] = map[string]any{"cleanup": map[string]any{"status": "passed"}}
					report["compatibility"] = map[string]any{"current_fingerprint": contract.Fingerprint}
				}
				var err error
				if stage == failure {
					report["status"] = "failed"
					err = errors.New("failed")
				}
				raw, _ := json.Marshal(report)
				return raw, err
			}
			report, err := runDataAPIRelease(context.Background(), config, contract, runner)
			if err != nil {
				t.Fatal(err)
			}
			if failure < 0 {
				if report.Status != "passed" || calls != 4 {
					t.Fatal("complete release did not pass")
				}
			} else {
				if report.Status != "failed" || calls != failure+1 {
					t.Fatal("release did not stop at failed stage")
				}
				for _, step := range report.Steps[failure+1:] {
					if step.Status != "blocked" {
						t.Fatal("dependent stage was not blocked")
					}
				}
			}
		})
	}
	calls := 0
	report, _ := runDataAPIRelease(context.Background(), config, contract, func(context.Context, []string) ([]byte, error) {
		calls++
		return []byte(`{"version":1,"command":"data-api migration preview","status":"passed","cleanup":"passed","compatibility":{"current_fingerprint":"stale"}}`), nil
	})
	if report.Status != "failed" || calls != 1 || !strings.Contains(report.Steps[0].Error, "current_schema") {
		t.Fatal("stale candidate schema accepted")
	}
	report, _ = runDataAPIRelease(context.Background(), config, contract, func(context.Context, []string) ([]byte, error) { return []byte("non-json"), errors.New("runtime") })
	if report.Cleanup != "unknown" {
		t.Fatal("cleanup claimed after missing report")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, _ = runDataAPIRelease(ctx, config, contract, func(context.Context, []string) ([]byte, error) { t.Fatal("ran after cancellation"); return nil, nil })
	if report.Status != "failed" {
		t.Fatal("cancelled release passed")
	}
}
func TestDataAPIReleaseConfigValidatesInputsBeforeStartingRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release.json")
	for _, raw := range []string{`{}`, `{"version":1,"unknown":true}`, `{"version":1,"from_version":1}`} {
		_ = os.WriteFile(path, []byte(raw), 0600)
		if _, _, err := readDataAPIReleaseConfig(path); err == nil {
			t.Fatal("incomplete config accepted")
		}
	}
	config := dataAPIReleaseConfig{Version: 1, Baseline: "schema.json", CurrentSchema: "schema.json", Checks: "checks.json", Collection: "requests.json", PlanBaseline: "plans.json", Budget: "budget.json", CoveragePolicy: "policy.json"}
	_ = os.WriteFile(filepath.Join(dir, "schema.json"), dataAPIContractFixture(t, dataAPIDiffFixture()), 0600)
	for _, name := range []string{"checks.json", "requests.json", "plans.json", "budget.json"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0600)
	}
	_ = os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"version":1,"default_required":["integrity"],"relations":[]}`), 0600)
	raw, _ := json.Marshal(config)
	_ = os.WriteFile(path, raw, 0600)
	got, _, err := readDataAPIReleaseConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Directory != dir || got.Checks != filepath.Join(dir, "checks.json") {
		t.Fatal("relative paths not project-bound")
	}
	config.Fixture = "fixture.sql"
	_ = os.WriteFile(filepath.Join(dir, "fixture.sql"), []byte("SELECT 1;"), 0600)
	raw, _ = json.Marshal(config)
	_ = os.WriteFile(path, raw, 0600)
	if _, _, err = readDataAPIReleaseConfig(path); err == nil {
		t.Fatal("clean rebuild accepted unrelated fixture")
	}
	_ = os.WriteFile(filepath.Join(dir, "data-api.dev.json"), []byte(`{"seed":"fixture.sql"}`), 0600)
	if _, _, err = readDataAPIReleaseConfig(path); err != nil {
		t.Fatal(err)
	}
	config.Fixture = ""
	config.FromVersion = 1
	raw, _ = json.Marshal(config)
	_ = os.WriteFile(path, raw, 0600)
	if _, _, err = readDataAPIReleaseConfig(path); err == nil {
		t.Fatal("upgrade without distinct fixtures accepted")
	}
}

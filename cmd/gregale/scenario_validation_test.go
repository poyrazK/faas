package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestScenarioSuiteRejectsUnsupportedEnginesBeforeAnyExecution(t *testing.T) {
	fault := &main.TestChaosSpec{Duration: "1s", Rules: []main.TestChaosRule{{To: "audit-app", Kind: "http_status", StatusCode: 503, Percent: 100}}}
	for _, kind := range []string{"staged", "chaos"} {
		engines := []string{"local"}
		if kind == "chaos" {
			engines = append(engines, "simulated")
		}
		for _, engine := range engines {
			for _, override := range []bool{false, true} {
				for _, validate := range []bool{false, true} {
					name := kind + "/" + engine
					if override {
						name += "/override"
					}
					if validate {
						name += "/validate"
					}
					t.Run(name, func(t *testing.T) {
						dir := t.TempDir()
						writeHostedTestApp(t, dir)
						main.CaptureSuiteTestOutput(t)
						command := []string{"sh", "-c", "printf ran > started"}
						first := main.TestScenario{Project: "first-app", Command: command, Simulation: command}
						second := main.TestScenario{Project: "audit-app", Command: command, Simulation: command, Chaos: fault}
						if kind == "staged" {
							second = main.TestScenario{Project: "audit-app", Steps: []main.TestScenarioStep{
								{Name: "baseline", Command: []string{"sh", "-c", "exit 71"}},
								{Name: "fault", Chaos: fault, Command: []string{"sh", "-c", "exit 72"}},
								{Name: "clear", ClearChaos: true},
								{Name: "recovery", Command: []string{"sh", "-c", "exit 73"}},
							}}
						}
						declaredEngine := engine
						if override {
							declaredEngine = "real-vm"
						}
						manifest := main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1,
							Scenarios: map[string]main.TestScenario{"first": first, "second": second},
							Suites: map[string]main.TestSuite{"audit": {Scenarios: []main.TestSuiteMember{
								{Scenario: "first", Engine: declaredEngine}, {Scenario: "second", Engine: declaredEngine},
							}}},
						})
						args := []string{"--manifest", manifest, "--suite", "audit"}
						if override {
							args = append(args, "--engine", engine)
						}
						if engine == "local" {
							args = append(args, "--base-url", "http://127.0.0.1:1")
						}
						if validate {
							args = append(args, "--validate")
						}
						code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) })
						if code == 0 || !strings.Contains(problem, "require the real-vm engine") {
							t.Fatalf("unsupported suite exit %d: %s", code, problem)
						}
						if _, err := os.Stat(filepath.Join(dir, "started")); !os.IsNotExist(err) {
							t.Fatal("an earlier suite member executed before rejecting the unsupported engine")
						}
					})
				}
			}
		}
	}
}

func writeScenarioProfileManifest(t *testing.T, dir string, suspend bool) string {
	t.Helper()
	scenarios, source, err := main.ReadTestManifest("../../tests/scenario-acceptance/rehearsal-running-job/gregale-test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	scenario := scenarios["running-import"]
	scenario.Source = source
	for name, service := range scenario.Services {
		service.Source = filepath.Join(source, service.Source)
		scenario.Services[name] = service
	}
	if !suspend {
		scenario.SuspendResume = nil
	}
	return main.WriteSuiteTestManifest(t, dir, main.TestManifest{Version: 1,
		Scenarios: map[string]main.TestScenario{"running-import": scenario},
		Suites:    map[string]main.TestSuite{"running": {Scenarios: []main.TestSuiteMember{{Scenario: "running-import", Engine: "real-vm", Profile: "warm"}}}},
	})
}

func TestScenarioSuiteValidatesSelectedLifecycleProfiles(t *testing.T) {
	for _, suspend := range []bool{false, true} {
		name := "contained"
		if suspend {
			name = "suspend"
		}
		t.Run(name, func(t *testing.T) {
			manifest := writeScenarioProfileManifest(t, t.TempDir(), suspend)
			for _, profile := range []string{"warm", "cold", "restored", "all"} {
				t.Run(profile, func(t *testing.T) {
					main.CaptureSuiteTestOutput(t)
					args := []string{"--manifest", manifest, "--suite", "running", "--profile", profile}
					valid := profile == "warm" || (profile == "cold" && !suspend)
					code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(append(args, "--validate")) })
					if (code == 0) != valid {
						t.Fatalf("suite validation exit %d, want valid=%t: %s", code, valid, problem)
					}
					if valid {
						return
					}
					for _, mode := range [][]string{nil, {"--preflight"}} {
						code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(append(args, mode...)) })
						if code == 0 || !strings.Contains(problem, "profile") || strings.Contains(problem, "Not logged in") {
							t.Fatalf("unsupported profile reached platform setup: exit %d: %s", code, problem)
						}
					}
				})
			}
		})
	}
}

func TestScenarioCIGeneratorValidatesEveryMatrixProfile(t *testing.T) {
	for _, suspend := range []bool{false, true} {
		name := "contained"
		if suspend {
			name = "suspend"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			manifest := writeScenarioProfileManifest(t, dir, suspend)
			t.Chdir(dir)
			main.CaptureSuiteTestOutput(t)
			args := []string{"ci", "init", "--manifest", manifest, "--suite", "running", "--max-workload-minutes", "1000"}
			for _, profiles := range []string{"all", "warm,cold,restored"} {
				code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(append(args, "--profiles", profiles)) })
				if code == 0 || !strings.Contains(problem, "profile") {
					t.Fatalf("unsupported profile matrix exit %d: %s", code, problem)
				}
				for _, path := range []string{main.DefaultTestCIWorkflow, main.DefaultTestCIBudget} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("rejected profile matrix wrote %s", path)
					}
				}
			}
			profiles := "warm"
			if !suspend {
				profiles = "warm,cold"
			}
			code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(append(args, "--profiles", profiles)) })
			if code != 0 {
				t.Fatalf("supported profile matrix exit %d: %s", code, problem)
			}
		})
	}
}

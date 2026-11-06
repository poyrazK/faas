package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

func TestCmdTestCIInitFindsRepositoryFromSubdirectory(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "default outputs", true: "custom outputs"}[custom], func(t *testing.T) {
			root := t.TempDir()
			main.RehearsalTestGit(t, root, "init", "-q")
			app := filepath.Join(root, "apps", "api")
			if err := os.MkdirAll(app, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(app)
			main.CaptureSuiteTestOutput(t)
			main.WriteSuiteTestManifest(t, app, main.TestManifest{Version: 1,
				Scenarios: map[string]main.TestScenario{"audit": {Project: "audit-app", Command: []string{"true"}, Simulation: []string{"true"}}},
				Suites:    map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{{Scenario: "audit", Engine: "simulated"}}}},
			})
			workflow, budget := main.DefaultTestCIWorkflow, main.DefaultTestCIBudget
			args := []string{"ci", "init", "--suite", "smoke"}
			if custom {
				workflow, budget = ".github/workflows/custom.yml", "../../custom-budget.yaml"
				args = append(args, "--workflow", workflow, "--budget", budget)
			}
			if code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) }); code != 0 {
				t.Fatalf("CI initialization exit %d: %s", code, problem)
			}
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(workflow)))
			if err != nil {
				t.Fatalf("workflow missing from repository root: %v", err)
			}
			for _, path := range []string{"apps/api/gregale-test.yaml", filepath.ToSlash(filepath.Clean(filepath.Join("apps/api", budget)))} {
				if !strings.Contains(string(body), path) {
					t.Errorf("workflow does not reference repository-relative input %q", path)
				}
			}
			if _, err := main.ReadTestComparisonBudget(filepath.Join(app, budget)); err != nil {
				t.Errorf("budget missing from requested directory: %v", err)
			}
			if _, err := os.Stat(filepath.Join(app, ".github")); !os.IsNotExist(err) {
				t.Errorf("CI initialization created an ignored nested workflow directory: %v", err)
			}
		})
	}
}

func TestCmdTestCIInitRejectsNestedWorkflowFiles(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	main.CaptureSuiteTestOutput(t)
	main.WriteSuiteTestManifest(t, root, main.TestManifest{Version: 1,
		Scenarios: map[string]main.TestScenario{"audit": {Project: "audit-app", Command: []string{"true"}, Simulation: []string{"true"}}},
		Suites:    map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{{Scenario: "audit", Engine: "simulated"}}}},
	})
	for _, path := range []string{".github/workflows/nested/audit.yml", filepath.Join(root, ".github/workflows/nested/audit.yaml")} {
		code, problem := main.RunWithStderr(t, func() int {
			return main.CmdTest([]string{"ci", "init", "--suite", "smoke", "--workflow", path})
		})
		if code == 0 || !strings.Contains(problem, ".github/workflows") {
			t.Errorf("nested workflow accepted: exit %d: %s", code, problem)
		}
	}
	for _, path := range []string{main.DefaultTestCIBudget, ".github"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Errorf("invalid workflow created %s: %v", path, err)
		}
	}
}

func TestCmdTestCIInitSizesTimeoutForSelectedRuns(t *testing.T) {
	for _, tc := range []struct {
		name, engine  string
		repeat, cases int
		pass          bool
	}{
		{"long local load", "local", 20, 1, true},
		{"too many data cases", "local", 20, 100, false},
		{"long simulated suite", "simulated", 20, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			main.CaptureSuiteTestOutput(t)
			scenario := main.TestScenario{Project: "audit-app", Timeout: "10m", Command: []string{"true"}, Simulation: []string{"true"}}
			member := main.TestSuiteMember{Scenario: "audit", Engine: tc.engine}
			args := []string{"ci", "init", "--suite", "smoke", "--repeat", "20"}
			if tc.engine == "local" {
				scenario.Local = &main.TestLocalAppSpec{Command: []string{"node", "server.js"}}
				scenario.Requests = []main.TestHTTPRequest{{Name: "health", Method: "GET", Path: "/health", Expect: main.TestHTTPExpect{Status: 200}}}
				scenario.Load = &main.TestLoadSpec{Duration: "5m", VUs: 1}
				args = append(args, "--load")
			}
			if tc.cases > 1 {
				member.Data = "cases.csv"
				main.WriteSuiteTestFile(t, filepath.Join(root, member.Data), "id\n"+strings.Repeat("case\n", tc.cases))
			}
			main.WriteSuiteTestManifest(t, root, main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"audit": scenario},
				Suites: map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{member}}},
			})
			code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) })
			if (code == 0) != tc.pass {
				t.Fatalf("timeout validation exit %d: %s", code, problem)
			}
			if !tc.pass {
				if !strings.Contains(problem, "360") {
					t.Errorf("missing GitHub job limit diagnostic: %s", problem)
				}
				for _, path := range []string{main.DefaultTestCIBudget, main.DefaultTestCIWorkflow} {
					if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
						t.Errorf("oversized plan wrote %s: %v", path, err)
					}
				}
				return
			}
			body, err := os.ReadFile(filepath.Join(root, main.DefaultTestCIWorkflow))
			if err != nil {
				t.Fatal(err)
			}
			var workflow struct {
				Jobs map[string]struct {
					Timeout int `yaml:"timeout-minutes"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal(body, &workflow); err != nil {
				t.Fatal(err)
			}
			if timeout := workflow.Jobs["scenarios"].Timeout; timeout < 10*tc.repeat || timeout > 360 {
				t.Errorf("job timeout %d does not cover %d runs with 10-minute scenario limits", timeout, tc.repeat)
			}
		})
	}
}

func TestCmdTestCIInitDetectsSimulationAndStagedNodeCommands(t *testing.T) {
	for _, executable := range []string{"node", "npm", "npx", "sh"} {
		for _, engine := range []string{"simulated", "real-vm"} {
			t.Run(engine+"/"+executable, func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				main.CaptureSuiteTestOutput(t)
				scenario := main.TestScenario{Project: "audit-app", Timeout: "5s", Command: []string{"true"}, Simulation: []string{executable, "check.js"}}
				args := []string{"ci", "init", "--suite", "smoke"}
				if engine == "real-vm" {
					main.WriteSuiteTestFile(t, filepath.Join(root, "handler.js"), "exports.handler = async () => ({statusCode: 200});\n")
					scenario.Command, scenario.Simulation = nil, nil
					scenario.Steps = []main.TestScenarioStep{
						{Name: "baseline", Command: []string{executable, "check.js"}},
						{Name: "fault", Command: []string{"true"}, Chaos: &main.TestChaosSpec{Duration: "1s", Rules: []main.TestChaosRule{{To: "audit-app", Kind: "http_status", Percent: 100, StatusCode: 503}}}},
						{Name: "clear", ClearChaos: true},
						{Name: "recovery", Command: []string{"true"}},
					}
					args = append(args, "--profiles", "warm", "--max-workload-minutes", "10")
				}
				main.WriteSuiteTestManifest(t, root, main.TestManifest{Version: 1,
					Scenarios: map[string]main.TestScenario{"audit": scenario},
					Suites:    map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{{Scenario: "audit", Engine: engine}}}},
				})
				if code, problem := main.RunWithStderr(t, func() int { return main.CmdTest(args) }); code != 0 {
					t.Fatalf("CI initialization exit %d: %s", code, problem)
				}
				body, err := os.ReadFile(filepath.Join(root, main.DefaultTestCIWorkflow))
				if err != nil {
					t.Fatal(err)
				}
				if got, want := strings.Contains(string(body), "actions/setup-node@"), executable != "sh"; got != want {
					t.Errorf("Node setup present=%t, want %t for %s %s", got, want, engine, executable)
				}
			})
		}
	}
}

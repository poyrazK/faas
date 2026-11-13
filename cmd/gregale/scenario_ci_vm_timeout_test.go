package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

func TestCmdTestCIInitSizesVMTimeoutAndDispatchRepeats(t *testing.T) {
	for _, tc := range []struct {
		name, timeout, profiles, expansion string
		scenarios, repeat, slots           int
		choices                            []string
	}{
		{"small suite", "1m", "warm", "", 1, 1, 1, []string{"1", "2", "3"}},
		{"long suite", "1h", "warm", "", 5, 1, 1, []string{"1"}},
		{"two repeats fit", "40m", "warm", "", 3, 2, 1, []string{"1", "2"}},
		{"profiles have separate jobs", "40m", "all", "", 3, 2, 1, []string{"1", "2"}},
		{"resilience runs", "15m", "warm", "resilience", 1, 1, 12, []string{"1"}},
		{"fault matrix runs", "15m", "warm", "matrix", 1, 1, 12, []string{"1"}},
		{"original oversized suite", "15m", "warm", "", 18, 1, 1, nil},
		{"one repeat exceeds job limit", "1h", "warm", "", 6, 1, 1, nil},
		{"selected default exceeds job limit", "40m", "warm", "", 3, 3, 1, nil},
		{"expanded suite exceeds job limit", "1h", "warm", "matrix", 1, 1, 12, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			main.CaptureSuiteTestOutput(t)
			main.WriteSuiteTestFile(t, filepath.Join(root, "package.json"), `{"scripts":{"start":"node server.js"}}`)
			main.WriteSuiteTestFile(t, filepath.Join(root, "server.js"), "require('http').createServer((req,res)=>res.end('ok')).listen(process.env.PORT||3000);\n")
			manifest := main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{}, Suites: map[string]main.TestSuite{}}
			var members []main.TestSuiteMember
			for i := 0; i < tc.scenarios; i++ {
				name := fmt.Sprintf("vm-scenario-%02d", i)
				scenario := main.TestScenario{Project: "vm-audit", Source: ".", Timeout: tc.timeout, Command: []string{"true"}}
				if tc.expansion != "" {
					scenario = main.FaultMatrixTestScenario()
					scenario.Source, scenario.Timeout = ".", tc.timeout
					for name, service := range scenario.Services {
						service.Source = "."
						scenario.Services[name] = service
					}
					scenario.Resilience.Runs = 6
					if tc.expansion == "resilience" {
						scenario.Resilience.FaultMatrix = nil
						scenario.Resilience.Runs = 12
					}
				}
				manifest.Scenarios[name] = scenario
				members = append(members, main.TestSuiteMember{Scenario: name, Engine: "real-vm", Profile: "warm"})
			}
			manifest.Suites["vm-suite"] = main.TestSuite{Scenarios: members}
			path := main.WriteSuiteTestManifest(t, root, manifest)
			code, problem := main.RunWithStderr(t, func() int {
				return main.CmdTest([]string{"ci", "init", "--manifest", path, "--suite", "vm-suite", "--engine", "real-vm", "--profiles", tc.profiles,
					"--repeat", strconv.Itoa(tc.repeat), "--max-workload-minutes", "10000"})
			})
			if tc.choices == nil {
				if code == 0 || !strings.Contains(problem, "360") {
					t.Fatalf("oversized VM plan exit %d: %s", code, problem)
				}
				if _, err := os.Stat(filepath.Join(root, ".github")); !os.IsNotExist(err) {
					t.Fatalf("oversized VM plan created workflow directories: %v", err)
				}
				return
			}
			if code != 0 {
				t.Fatalf("VM workflow generation exit %d: %s", code, problem)
			}
			body, err := os.ReadFile(filepath.Join(root, main.DefaultTestCIWorkflow))
			if err != nil {
				t.Fatal(err)
			}
			var workflow struct {
				On struct {
					Dispatch struct {
						Inputs map[string]struct {
							Default string   `yaml:"default"`
							Options []string `yaml:"options"`
						} `yaml:"inputs"`
					} `yaml:"workflow_dispatch"`
				} `yaml:"on"`
				Jobs map[string]struct {
					Timeout int `yaml:"timeout-minutes"`
					Steps   []struct {
						Name string `yaml:"name"`
						Run  string `yaml:"run"`
					} `yaml:"steps"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal(body, &workflow); err != nil {
				t.Fatal(err)
			}
			repeat := workflow.On.Dispatch.Inputs["repeat"]
			if repeat.Default != strconv.Itoa(tc.repeat) || !slices.Equal(repeat.Options, tc.choices) {
				t.Errorf("dispatch repeat default=%s, options=%v; want %d, %v", repeat.Default, repeat.Options, tc.repeat, tc.choices)
			}
			limit := workflow.Jobs["scenarios"].Timeout
			if limit < 30 || limit > 360 {
				t.Fatalf("invalid GitHub job timeout %d", limit)
			}
			duration, err := time.ParseDuration(tc.timeout)
			if err != nil {
				t.Fatal(err)
			}
			for _, option := range repeat.Options {
				count, err := strconv.Atoi(option)
				if err != nil {
					t.Fatal(err)
				}
				work := time.Duration(tc.scenarios*tc.slots*count)*duration + 10*time.Minute
				if time.Duration(limit)*time.Minute <= work {
					t.Errorf("%d-minute job does not cover %d repeats (%s) plus VM cleanup", limit, count, work)
				}
			}
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			var config string
			for _, step := range workflow.Jobs["scenarios"].Steps {
				if step.Name == "Check Gregale credentials" {
					config = step.Run
				}
			}
			if config == "" {
				t.Fatal("workflow has no dispatch validation")
			}
			// API dispatches must obey the same repeat limit as the UI choices.
			for _, count := range []int{tc.repeat, len(tc.choices) + 1} {
				command := exec.CommandContext(t.Context(), "bash", "-c", config)
				command.Env = append(os.Environ(), "FAAS_API=http://127.0.0.1:1", "FAAS_TOKEN=disposable-test-token",
					"GREGALE_CI_REPEAT="+strconv.Itoa(count), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				output, err := command.CombinedOutput()
				if (err == nil) != (count == tc.repeat) {
					t.Errorf("dispatch repeat %d: %s (%v)", count, output, err)
				}
			}
		})
	}
}

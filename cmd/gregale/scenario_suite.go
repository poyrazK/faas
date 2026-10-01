package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

type testSuite struct {
	Scenarios []testSuiteMember `yaml:"scenarios"`
}

type testSuiteMember struct {
	Scenario string `yaml:"scenario"`
	Data     string `yaml:"data,omitempty"`
	Engine   string `yaml:"engine,omitempty"`
	Profile  string `yaml:"profile,omitempty"`
}

type testSuiteOptions struct {
	Engine             string
	EngineSet          bool
	Profile            string
	ProfileSet         bool
	BaseURL            string
	Repeat             int
	Load               bool
	LoadOverrides      testLoadOverrides
	Progress           bool
	BaselinePath       string
	MaxWorkloadMinutes int
	Validate           bool
	Preflight          bool
}

var testSuiteNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,79}$`)

func validTestEngine(engine string) bool {
	return engine == "real-vm" || engine == "local" || engine == "simulated"
}

func validateTestSuites(manifest testManifest) error {
	for name, suite := range manifest.Suites {
		if !testSuiteNamePattern.MatchString(name) {
			return fmt.Errorf("suite %q needs a name of 1..80 lowercase letters, digits, or hyphens starting with a letter", name)
		}
		if len(suite.Scenarios) == 0 || len(suite.Scenarios) > 100 {
			return fmt.Errorf("suite %q must contain between 1 and 100 scenarios", name)
		}
		seen := make(map[string]bool, len(suite.Scenarios))
		for _, member := range suite.Scenarios {
			if _, ok := manifest.Scenarios[member.Scenario]; !ok {
				return fmt.Errorf("suite %q references unknown scenario %q", name, member.Scenario)
			}
			if seen[member.Scenario] {
				return fmt.Errorf("suite %q repeats scenario %q; use --repeat or profile: all", name, member.Scenario)
			}
			seen[member.Scenario] = true
			if member.Engine != "" && !validTestEngine(member.Engine) {
				return fmt.Errorf("suite %q scenario %q has invalid engine %q", name, member.Scenario, member.Engine)
			}
			if member.Profile != "" {
				if _, err := selectedTestProfiles(member.Profile); err != nil {
					return fmt.Errorf("suite %q scenario %q: %w", name, member.Scenario, err)
				}
				if member.Engine != "" && member.Engine != "real-vm" {
					return fmt.Errorf("suite %q scenario %q: profile applies only to real-vm tests", name, member.Scenario)
				}
			}
		}
	}
	return nil
}

func cmdTestSuite(path, name string, options testSuiteOptions, failFast bool, reportPath, junitPath, htmlPath string) int {
	scenarios, err := prepareTestSuite(path, name, options)
	if err != nil {
		// Preparation only reads local inputs. Filesystem errors must not be
		// mistaken for SDK transport failures by the generic error renderer.
		return printErr("Invalid test suite", &exitErr{msg: err.Error(), code: 1})
	}
	if options.Validate {
		return validatePreparedTestSuite(name, scenarios)
	}
	var client *Client
	if testSuiteHasEngine(scenarios, "real-vm") {
		client, err = authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
	}
	if options.Preflight {
		return runTestSuitePreflight(context.Background(), client, scenarios, options.Repeat)
	}
	return executePreparedTests(client, name, scenarios, options.Repeat, failFast, reportPath, junitPath, htmlPath, options.BaselinePath)
}

func prepareTestSuite(path, name string, options testSuiteOptions) ([]testPreparedScenario, error) {
	if !validTestEngine(options.Engine) {
		return nil, fmt.Errorf("engine %q is invalid; use real-vm, local, or simulated", options.Engine)
	}
	if options.ProfileSet {
		if _, err := selectedTestProfiles(options.Profile); err != nil {
			return nil, err
		}
	}
	manifest, dir, err := readTestManifestDocument(path, func(_ string, scenario testScenario) []string {
		return testHTTPDataFields(scenario)
	})
	if err != nil {
		return nil, err
	}
	suite, ok := manifest.Suites[name]
	if !ok {
		return nil, fmt.Errorf("suite %q is not declared in %s", name, path)
	}
	prepared := make([]testPreparedScenario, 0, len(suite.Scenarios))
	for _, member := range suite.Scenarios {
		scenario, err := prepareTestSuiteMember(member, manifest.Scenarios[member.Scenario], dir, options)
		if err != nil {
			return nil, fmt.Errorf("scenario %q: %w", member.Scenario, err)
		}
		prepared = append(prepared, scenario)
	}
	if err := validateTestSuiteOptions(prepared, options); err != nil {
		return nil, err
	}
	return prepared, nil
}

func prepareTestSuiteMember(member testSuiteMember, scenario testScenario, dir string, options testSuiteOptions) (testPreparedScenario, error) {
	prepared := testPreparedScenario{Name: member.Scenario, Scenario: scenario, ManifestDir: dir, Engine: options.Engine}
	if member.Engine != "" && !options.EngineSet {
		prepared.Engine = member.Engine
	}
	if options.Load && prepared.Engine != "local" {
		return prepared, errors.New("--load currently requires every suite member to use --engine local")
	}
	if prepared.Engine != "local" && member.Data != "" {
		return prepared, errors.New("case data applies only to the local engine")
	}
	dataPath := member.Data
	if dataPath != "" && !filepath.IsAbs(dataPath) {
		dataPath = filepath.Join(dir, dataPath)
	}
	cases, fields, err := readTestData(dataPath)
	if err != nil {
		return prepared, fmt.Errorf("case data: %w", err)
	}
	prepared.Cases = cases
	if err := validateTestHTTPRequestsWithData(scenario, fields); err != nil {
		return prepared, err
	}
	if options.Validate || prepared.Engine == "real-vm" {
		if _, err := collectTestValidationForEngine(map[string]testScenario{member.Scenario: scenario}, dir, member.Scenario, prepared.Engine); err != nil {
			return prepared, err
		}
	} else {
		source := scenario.Source
		if source == "" {
			source = "."
		}
		if _, err := resolveDeploySourceDir(dir, source); err != nil {
			return prepared, err
		}
	}
	if err := prepareTestSuiteEngine(&prepared, member, options); err != nil {
		return prepared, err
	}
	return prepared, nil
}

func prepareTestSuiteEngine(prepared *testPreparedScenario, member testSuiteMember, options testSuiteOptions) error {
	scenario := prepared.Scenario
	switch prepared.Engine {
	case "real-vm":
		profile := options.Profile
		if member.Profile != "" && !options.ProfileSet {
			profile = member.Profile
		}
		var err error
		prepared.Profiles, err = selectedTestProfiles(profile)
		return err
	case "simulated":
		if len(scenario.Simulation) == 0 {
			return errors.New("scenario has no simulation command")
		}
	case "local":
		if err := validateLocalTestScenario(scenario); err != nil {
			return err
		}
		if options.BaseURL != "" {
			var err error
			prepared.BaseURL, err = validateLocalTestURL(options.BaseURL)
			if err != nil {
				return err
			}
		}
		if !options.Validate {
			if prepared.BaseURL == "" && scenario.Local == nil {
				return errors.New("local tests require --base-url or a local.command in the scenario")
			}
			if _, err := localTestConsumerEnv(scenario); err != nil {
				return err
			}
		}
		if options.Load {
			cfg, err := resolveTestLoadConfig(scenario, options.LoadOverrides)
			if err != nil {
				return err
			}
			if options.Progress {
				cfg.Progress = func(update testLoadProgress) { printTestLoadProgress(osStderr, update) }
			}
			prepared.Load = cfg
		}
	}
	return nil
}

func testSuiteHasEngine(scenarios []testPreparedScenario, engine string) bool {
	for _, scenario := range scenarios {
		if scenario.Engine == engine {
			return true
		}
	}
	return false
}

func validateTestSuiteOptions(scenarios []testPreparedScenario, options testSuiteOptions) error {
	if options.BaseURL != "" && !testSuiteHasEngine(scenarios, "local") {
		return errors.New("--base-url applies only to local tests")
	}
	if options.ProfileSet && !testSuiteHasEngine(scenarios, "real-vm") {
		return errors.New("--profile applies only to real-vm tests")
	}
	if options.MaxWorkloadMinutes > 0 && !testSuiteHasEngine(scenarios, "real-vm") {
		return errors.New("--max-workload-minutes applies only to real-vm tests")
	}
	if options.Preflight {
		for _, scenario := range scenarios {
			if scenario.Engine != "real-vm" {
				return errors.New("--preflight requires every suite member to use real-vm")
			}
		}
	}
	if options.MaxWorkloadMinutes > 0 {
		estimate := estimateTestSuiteWorkloadMinutes(scenarios, options.Repeat)
		if estimate > options.MaxWorkloadMinutes {
			return fmt.Errorf("suite resource guard exceeded: up to %d workload-minutes exceed --max-workload-minutes %d", estimate, options.MaxWorkloadMinutes)
		}
	}
	return nil
}

func estimateTestSuiteWorkloadMinutes(scenarios []testPreparedScenario, repeat int) int {
	estimate := 0
	for _, scenario := range scenarios {
		if scenario.Engine == "real-vm" {
			estimate += estimateTestWorkloadMinutes(scenario.Scenario, len(scenario.Profiles)*repeat)
		}
	}
	return estimate
}

func validatePreparedTestSuite(name string, scenarios []testPreparedScenario) int {
	results := make([]testValidationResult, 0, len(scenarios))
	for _, scenario := range scenarios {
		validation, err := collectTestValidationForEngine(map[string]testScenario{scenario.Name: scenario.Scenario}, scenario.ManifestDir, scenario.Name, scenario.Engine)
		if err != nil {
			return printErr("Invalid scenario source", err)
		}
		result := validation[0]
		result.Suite, result.Engine = name, scenario.Engine
		results = append(results, result)
	}
	if jsonOutput {
		if err := writeJSON(results); err != nil {
			return printErr("Could not print validation", err)
		}
	} else {
		for _, result := range results {
			_, _ = fmt.Fprintf(osStdout, "%s: valid (%s, suite %s)\n", result.Scenario, result.Engine, name)
		}
	}
	return 0
}

func runTestSuitePreflight(ctx context.Context, client testPreflightClient, scenarios []testPreparedScenario, repeat int) int {
	account, err := client.Whoami(ctx)
	if err != nil {
		return printErr("Could not inspect account", err)
	}
	capabilities, err := client.GetCapabilities(ctx)
	if err != nil {
		return printErr("Could not inspect capabilities", err)
	}
	results := make([]testPreflightResult, 0, len(scenarios))
	ready := true
	for _, scenario := range scenarios {
		result := assessTestPreflight(scenario.Name, scenario.Scenario, scenario.Profiles, repeat, account, capabilities)
		results = append(results, result)
		ready = ready && result.Ready
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStdout, "%s: estimate %d deployments, up to %d workload-minutes\n", result.Scenario, result.EstimatedDeploys, result.MaxWorkloadMinutes)
			for _, check := range result.Checks {
				status := "ok"
				if !check.Passed {
					status = "blocked"
				}
				_, _ = fmt.Fprintf(osStdout, "%s: %s — %s\n", check.Name, status, check.Detail)
			}
		}
	}
	if jsonOutput {
		if err := writeJSON(results); err != nil {
			return printErr("Could not print preflight", err)
		}
	}
	if !ready {
		return 1
	}
	return 0
}

package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type testPhaseEvidence struct {
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Status     string    `json:"status"`
}

type testPhaseRecorder struct {
	name    string
	started time.Time
	phases  []testPhaseEvidence
}

func newTestPhaseRecorder(name string) *testPhaseRecorder {
	return &testPhaseRecorder{name: name, started: time.Now().UTC()}
}

func (r *testPhaseRecorder) advance(name, previousStatus string) {
	r.finishCurrent(previousStatus)
	r.name, r.started = name, time.Now().UTC()
}

func (r *testPhaseRecorder) finish(status string) []testPhaseEvidence {
	r.finishCurrent(status)
	return r.phases
}

func (r *testPhaseRecorder) finishCurrent(status string) {
	if r.name == "" {
		return
	}
	r.phases = append(r.phases, testPhaseEvidence{
		Name: r.name, StartedAt: r.started, DurationMS: time.Since(r.started).Milliseconds(), Status: status,
	})
	r.name = ""
}

type testValidationResult struct {
	Suite               string `json:"suite,omitempty"`
	Engine              string `json:"engine,omitempty"`
	Scenario            string `json:"scenario"`
	Project             string `json:"project"`
	Workloads           int    `json:"workloads"`
	Buckets             int    `json:"buckets"`
	Consumers           int    `json:"consumers"`
	SimulationAvailable bool   `json:"simulation_available"`
}

// Real-VM validation checks deployable app shapes. Local validation checks the
// command directory and rejects waits that require platform evidence.
func validateTestManifestForEngine(scenarios map[string]testScenario, manifestDir, selected, engine string) int {
	results, err := collectTestValidationForEngine(scenarios, manifestDir, selected, engine)
	if err != nil {
		return printErr("Invalid scenario manifest", err)
	}
	if jsonOutput {
		if err := writeJSON(results); err != nil {
			return printErr("Could not print validation", err)
		}
		return 0
	}
	for _, result := range results {
		if engine == "local" {
			_, _ = fmt.Fprintf(osStdout, "%s: valid (local HTTP scenario)\n", result.Scenario)
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "%s: valid (%d workloads, %d buckets, %d consumers; simulation %s)\n",
			result.Scenario, result.Workloads, result.Buckets, result.Consumers,
			map[bool]string{true: "available", false: "unavailable"}[result.SimulationAvailable])
	}
	return 0
}

func collectTestValidation(scenarios map[string]testScenario, manifestDir, selected string) ([]testValidationResult, error) {
	return collectTestValidationForEngine(scenarios, manifestDir, selected, "real-vm")
}

func collectTestValidationForEngine(scenarios map[string]testScenario, manifestDir, selected, engine string) ([]testValidationResult, error) {
	names := make([]string, 0, len(scenarios))
	if selected != "" {
		if _, ok := scenarios[selected]; !ok {
			return nil, fmt.Errorf("%q is not declared in the manifest", selected)
		}
		names = append(names, selected)
	} else {
		for name := range scenarios {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("manifest declares no scenarios")
	}
	results := make([]testValidationResult, 0, len(names))
	for _, name := range names {
		scenario := scenarios[name]
		if scenario.Chaos != nil && engine != "real-vm" {
			return nil, fmt.Errorf("scenario %q declares chaos rules, which require the real-vm engine", name)
		}
		source := scenario.Source
		if source == "" {
			source = "."
		}
		if engine == "local" {
			if err := validateLocalTestScenario(scenario); err != nil {
				return nil, fmt.Errorf("scenario %q: %w", name, err)
			}
			if _, err := resolveDeploySourceDir(manifestDir, source); err != nil {
				return nil, fmt.Errorf("scenario %q: %w", name, err)
			}
		} else {
			if err := validateTestSource(manifestDir, source); err != nil {
				return nil, fmt.Errorf("scenario %q: %w", name, err)
			}
			for service, spec := range scenario.Services {
				if spec.Source == "" {
					continue // built-in fixture, validated by readTestManifest
				}
				if err := validateTestSource(manifestDir, spec.Source); err != nil {
					return nil, fmt.Errorf("scenario %q service %q: %w", name, service, err)
				}
			}
		}
		results = append(results, testValidationResult{
			Scenario: name, Project: scenario.Project, Workloads: 1 + len(scenario.Services),
			Buckets: len(scenario.Buckets), Consumers: len(scenario.Consumers),
			SimulationAvailable: len(scenario.Simulation) > 0,
		})
	}
	return results, nil
}

func validateTestSource(manifestDir, source string) error {
	dir, err := resolveDeploySourceDir(manifestDir, source)
	if err != nil {
		return err
	}
	if _, err := resolveDevSourceConfig(dir); err != nil {
		return fmt.Errorf("detect app shape in %s: %w", source, err)
	}
	return nil
}

type testJUnitSuite struct {
	XMLName  xml.Name        `xml:"testsuite"`
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []testJUnitCase `xml:"testcase"`
}

type testJUnitCase struct {
	Name      string            `xml:"name,attr"`
	ClassName string            `xml:"classname,attr"`
	Time      string            `xml:"time,attr"`
	Failure   *testJUnitFailure `xml:"failure,omitempty"`
	Skipped   *testJUnitSkipped `xml:"skipped,omitempty"`
	SystemOut string            `xml:"system-out"`
}

type testJUnitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type testJUnitSkipped struct {
	Message string `xml:"message,attr"`
}

func writeTestJUnit(path string, receipts []testRunReceipt) error {
	suite := testJUnitSuite{Name: "gregale scenarios", Tests: len(receipts), Cases: make([]testJUnitCase, 0, len(receipts))}
	if len(receipts) > 0 && receipts[0].Suite != "" {
		suite.Name = "gregale suite " + receipts[0].Suite
	}
	var totalMS int64
	for _, receipt := range receipts {
		body, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			return err
		}
		failureText := strings.TrimSpace(strings.Join([]string{receipt.Error, receipt.CleanupError}, "; "))
		caseName := receipt.Scenario + "/" + receipt.Profile
		if receipt.Load != nil {
			caseName += "/load"
		}
		if receipt.Case != "" {
			caseName += "/" + receipt.Case
		}
		caseResult := testJUnitCase{
			Name:      fmt.Sprintf("%s#%d", caseName, receipt.Attempt),
			ClassName: "gregale.scenario." + receipt.Engine,
			Time:      fmt.Sprintf("%.3f", float64(receipt.DurationMS)/1000),
			SystemOut: string(body),
		}
		if receipt.Status == "skipped" {
			suite.Skipped++
			caseResult.Skipped = &testJUnitSkipped{Message: receipt.SkipReason}
		} else if receipt.Status != "passed" {
			suite.Failures++
			if failureText == "" {
				failureText = "scenario did not pass"
			}
			caseResult.Failure = &testJUnitFailure{Message: failureText, Body: failureText}
		}
		suite.Cases = append(suite.Cases, caseResult)
		totalMS += receipt.DurationMS
	}
	suite.Time = fmt.Sprintf("%.3f", float64(totalMS)/1000)
	body, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return err
	}
	body = append([]byte(xml.Header), body...)
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o600)
}

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
)

// References the original configuration without copying credentials into reports.
type testReplayManifest struct {
	CampaignCoverage           bool                           `json:"campaign_coverage,omitempty"`
	DurationOverrides          []testFaultDurationOverride    `json:"duration_overrides,omitempty"`
	Sweep                      *testFaultSweepSetting         `json:"sweep,omitempty"`
	RecoveryOverrides          []testRecoveryDeadlineOverride `json:"recovery_overrides,omitempty"`
	PercentOverrides           []testFaultPercentOverride     `json:"percent_overrides,omitempty"`
	BandwidthOverrides         []testFaultBandwidthOverride   `json:"bandwidth_overrides,omitempty"`
	LatencyOverrides           []testFaultLatencyOverride     `json:"latency_overrides,omitempty"`
	RemovedRules               []testFaultRuleRef             `json:"removed_rules,omitempty"`
	ReducedConfigurationSHA256 string                         `json:"reduced_configuration_sha256,omitempty"`
	Version                    int                            `json:"version"`
	Scenario                   string                         `json:"scenario"`
	Profile                    string                         `json:"profile"`
	MatrixCase                 string                         `json:"matrix_case,omitempty"`
	ConfigurationSHA256        string                         `json:"configuration_sha256"`
}

func newTestReplayManifest(name, profile, matrixCase string, scenario testScenario) *testReplayManifest {
	var err error
	scenario, err = expandTestOutageCycles(scenario)
	if err != nil {
		return nil
	}
	coverage := scenario.Resilience != nil && scenario.Resilience.CampaignCoverage != nil && hasTestCampaignAggregatePolicy(scenario.Resilience.CampaignCoverage) && matrixCase == ""
	if coverage {
		scenario, err = expandTestFaultSweep(scenario)
		if err != nil {
			return nil
		}
	} else {
		scenario.Resilience = nil
	}
	data, err := json.Marshal(scenario)
	if err != nil {
		return nil
	}
	digest := sha256.Sum256(data)
	version := 1
	if coverage {
		version = 10
	}
	return &testReplayManifest{CampaignCoverage: coverage, Version: version, Scenario: name, Profile: profile, MatrixCase: matrixCase, ConfigurationSHA256: hex.EncodeToString(digest[:])}
}

func selectTestReplay(receipts []testRunReceipt, runID string) (*testRunReceipt, error) {
	var selected *testRunReceipt
	var visit func([]testRunReceipt) error
	visit = func(rows []testRunReceipt) error {
		for i := range rows {
			r := &rows[i]
			if r.RunID == runID && r.Replay != nil && (r.Status == "failed" || (r.Status == "passed" && (((r.Replay.Version == 6 || r.Replay.Version == 7) && len(r.Replay.RecoveryOverrides) > 0) || ((r.Replay.Version == 8 || r.Replay.Version == 9) && r.Replay.Sweep != nil) || (r.Replay.Version == 9 && (len(r.Replay.DurationOverrides) > 0 || len(r.Replay.RecoveryOverrides) > 0))))) {
				if selected != nil {
					return errors.New("report contains duplicate replay run IDs")
				}
				selected = r
			}
			if r.Resilience != nil {
				if err := visit(r.Resilience.History); err != nil {
					return err
				}
			}
			if r.Minimization != nil {
				for j := range r.Minimization.Trials {
					if err := visit(r.Minimization.Trials[j].Runs); err != nil {
						return err
					}
				}
			}
			if r.FaultMatrix != nil {
				if err := visit(r.FaultMatrix.Cases); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if runID == "" {
		return nil, errors.New("--run-id is required")
	}
	if err := visit(receipts); err != nil {
		return nil, err
	}
	if selected == nil {
		return nil, errors.New("no replayable failure or passing recovery/duration configuration or sweep run with that ID in report")
	}
	return selected, nil
}

// The newest edit type determines the minimum compatible replay version.
func testFaultReplayVersion(replay *testReplayManifest) int {
	if replay.CampaignCoverage {
		return 10
	}
	if len(replay.DurationOverrides) > 0 {
		return 9
	}
	if replay.Sweep != nil {
		return 8
	}
	for _, override := range replay.RecoveryOverrides {
		if override.Stability != "" {
			return 7
		}
	}
	if len(replay.RecoveryOverrides) > 0 {
		return 6
	}
	if len(replay.PercentOverrides) > 0 {
		return 5
	}
	if len(replay.BandwidthOverrides) > 0 {
		return 4
	}
	if len(replay.LatencyOverrides) > 0 {
		return 3
	}
	if len(replay.RemovedRules) > 0 {
		return 2
	}
	return 1
}

func prepareTestReplay(original *testRunReceipt, scenarios map[string]testScenario, dir string) (testPreparedScenario, error) {
	replay := original.Replay
	if replay == nil || (replay.Version != 1 && replay.Version != 2 && replay.Version != 3 && replay.Version != 4 && replay.Version != 5 && replay.Version != 6 && replay.Version != 7 && replay.Version != 8 && replay.Version != 9 && replay.Version != 10) || original.Engine != "real-vm" || replay.Scenario != original.Scenario || replay.Profile != original.Profile {
		return testPreparedScenario{}, errors.New("unsupported or inconsistent replay manifest")
	}
	if replay.Version != testFaultReplayVersion(replay) || (replay.Version == 1 && replay.ReducedConfigurationSHA256 != "") {
		return testPreparedScenario{}, errors.New("inconsistent reduced replay manifest version")
	}

	profiles, err := selectedTestProfiles(replay.Profile)
	if err != nil || len(profiles) != 1 {
		return testPreparedScenario{}, errors.New("replay requires one lifecycle profile")
	}
	scenario, ok := scenarios[replay.Scenario]
	if !ok {
		return testPreparedScenario{}, errors.New("replay scenario is absent from manifest")
	}
	scenario, err = expandTestOutageCycles(scenario)
	if err != nil {
		return testPreparedScenario{}, err
	}
	scenario, err = expandTestFaultSweep(scenario)
	if err != nil {
		return testPreparedScenario{}, err
	}

	if replay.CampaignCoverage {
		if replay.MatrixCase != "" || replay.Sweep != nil || len(replay.RemovedRules)+len(replay.LatencyOverrides)+len(replay.BandwidthOverrides)+len(replay.PercentOverrides)+len(replay.RecoveryOverrides)+len(replay.DurationOverrides) != 0 || replay.ReducedConfigurationSHA256 != "" {
			return testPreparedScenario{}, errors.New("campaign coverage replay must retain the complete batch without fault edits")
		}
		if scenario.Resilience == nil || scenario.Resilience.CampaignCoverage == nil || !hasTestCampaignAggregatePolicy(scenario.Resilience.CampaignCoverage) {
			return testPreparedScenario{}, errors.New("campaign coverage requirements are absent from manifest")
		}
		current := newTestReplayManifest(replay.Scenario, replay.Profile, "", scenario)
		if current == nil || !current.CampaignCoverage || current.ConfigurationSHA256 != replay.ConfigurationSHA256 {
			return testPreparedScenario{}, errors.New("campaign coverage configuration changed since original run")
		}
		if err := validateTestResilience(scenario); err != nil {
			return testPreparedScenario{}, err
		}
		return testPreparedScenario{Name: replay.Scenario, Scenario: scenario, ManifestDir: dir, Engine: "real-vm", Profiles: profiles}, nil
	}
	if replay.Sweep != nil && replay.MatrixCase == "" {
		return testPreparedScenario{}, errors.New("sweep replay requires a matrix case")
	}
	if replay.MatrixCase != "" {
		found := false
		if scenario.Resilience != nil {
			for _, spec := range scenario.Resilience.FaultMatrix {
				if spec.Name == replay.MatrixCase {
					if replay.Sweep != nil && (spec.Sweep == nil || *spec.Sweep != *replay.Sweep) {
						return testPreparedScenario{}, errors.New("sweep setting changed since original run")
					}
					scenario, err = applyTestFaultMatrixCase(scenario, spec)
					if err != nil {
						return testPreparedScenario{}, err
					}
					found = true
					break
				}
			}
		}
		if !found {
			return testPreparedScenario{}, errors.New("replay matrix case is absent from manifest")
		}
	}
	scenario.Resilience = nil
	current := newTestReplayManifest(replay.Scenario, replay.Profile, replay.MatrixCase, scenario)
	if current == nil || current.ConfigurationSHA256 != replay.ConfigurationSHA256 {
		return testPreparedScenario{}, errors.New("scenario configuration changed since original run")
	}
	if len(replay.DurationOverrides) > 0 || len(replay.RemovedRules) > 0 || len(replay.LatencyOverrides) > 0 || len(replay.BandwidthOverrides) > 0 || len(replay.PercentOverrides) > 0 || len(replay.RecoveryOverrides) > 0 || replay.ReducedConfigurationSHA256 != "" {
		scenario, err = applyTestFaultReplayOverrides(scenario, replay.RemovedRules, testFaultReplayOverrides{Duration: replay.DurationOverrides, Latency: replay.LatencyOverrides, Bandwidth: replay.BandwidthOverrides, Percent: replay.PercentOverrides, Recovery: replay.RecoveryOverrides})
		if err != nil {
			return testPreparedScenario{}, err
		}
		reduced := newTestReplayManifest(replay.Scenario, replay.Profile, replay.MatrixCase, scenario)
		if reduced == nil || reduced.ConfigurationSHA256 != replay.ReducedConfigurationSHA256 {
			return testPreparedScenario{}, errors.New("reduced replay configuration does not match saved manifest")
		}
		if err := validateTestScenarioSteps(scenario, nil); err != nil {
			return testPreparedScenario{}, err
		}
	}

	return testPreparedScenario{Name: replay.Scenario, Scenario: scenario, ManifestDir: dir, Engine: "real-vm", Profiles: profiles}, nil
}

func cmdTestReplay(args []string) int {
	fs := newFlagSet("test replay", flag.ContinueOnError)
	from := fs.String("from", "", "JSON report containing a failure or passing recovery/duration configuration or sweep run")
	id := fs.String("run-id", "", "replayable isolated run ID")
	manifest := fs.String("manifest", "gregale-test.yaml", "original scenario manifest")
	validate := fs.Bool("validate", false, "validate replay without provisioning")
	report := fs.String("report", "", "write replay JSON report")
	junit := fs.String("junit", "", "write replay JUnit report")
	html := fs.String("html", "", "write replay HTML report")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		return printErr("Invalid replay", errors.New("unexpected positional arguments"))
	}
	if *from == "" {
		return printErr("Invalid replay", errors.New("--from is required"))
	}
	if *validate && (*report != "" || *junit != "" || *html != "") {
		return printErr("Invalid replay", errors.New("validation does not write reports"))
	}
	if err := validateDistinctTestReportPaths(*from, *manifest, *report, *junit, *html); err != nil {
		return printErr("Invalid replay paths", err)
	}
	rows, _, err := readTestBaselineReport(*from, []string{*report, *junit, *html})
	if err != nil {
		return printErr("Invalid replay report", err)
	}
	original, err := selectTestReplay(rows, *id)
	if err != nil {
		return printErr("Invalid replay selection", err)
	}
	scenarios, dir, err := readTestManifestForScenario(*manifest, original.Scenario, nil)
	if err != nil {
		return printErr("Invalid replay manifest", err)
	}
	prepared, err := prepareTestReplay(original, scenarios, dir)
	if err != nil {
		return printErr("Invalid replay configuration", err)
	}
	effective := map[string]testScenario{prepared.Name: prepared.Scenario}
	if _, err := collectTestValidation(effective, dir, prepared.Name); err != nil {
		return printErr("Invalid replay source", err)
	}
	if *validate {
		fmt.Fprintln(osStdout, "Replay configuration validated")
		return 0
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	plans := prepareTestRunPlans([]testPreparedScenario{prepared}, 1)
	plans[0].ReplayCase, plans[0].ReplayOf = original.Replay.MatrixCase, original.RunID
	plans[0].ReplayManifest = original.Replay
	return executeTestReplayPlans(client, plans, *report, *junit, *html)
}

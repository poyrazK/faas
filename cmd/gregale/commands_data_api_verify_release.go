package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"
)

type dataAPIReleaseConfig struct {
	Version           int    `json:"version"`
	Directory         string `json:"directory,omitempty"`
	DevelopmentConfig string `json:"development_config,omitempty"`
	Baseline          string `json:"baseline"`
	CurrentSchema     string `json:"current_schema"`
	Checks            string `json:"checks,omitempty"`
	AccessChecks      string `json:"access_checks,omitempty"`
	Fixture           string `json:"fixture,omitempty"`
	Collection        string `json:"collection"`
	PlanBaseline      string `json:"plan_baseline"`
	Budget            string `json:"budget"`
	CoveragePolicy    string `json:"coverage_policy"`
	Dependencies      string `json:"dependencies,omitempty"`
	Approval          string `json:"approval,omitempty"`
	FromVersion       int    `json:"from_version,omitempty"`
	UpgradeFixture    string `json:"upgrade_fixture,omitempty"`
}
type dataAPIReleaseStep struct {
	Name   string          `json:"name"`
	Status string          `json:"status"`
	Error  string          `json:"error,omitempty"`
	Report json.RawMessage `json:"report,omitempty"`
}
type dataAPIReleaseReport struct {
	Version     int                  `json:"version"`
	Command     string               `json:"command"`
	Status      string               `json:"status"`
	Fingerprint string               `json:"schema_fingerprint"`
	Cleanup     string               `json:"cleanup"`
	Steps       []dataAPIReleaseStep `json:"steps"`
}
type dataAPIReleaseRunner func(context.Context, []string) ([]byte, error)

func readDataAPIReleaseConfig(path string) (dataAPIReleaseConfig, dataAPIContract, error) {
	var config dataAPIReleaseConfig
	var contract dataAPIContract
	f, err := openCustomerFile(path)
	if err != nil {
		return config, contract, errors.New("cannot read release config")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return config, contract, errors.New("release config exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || config.Version != 1 || config.Baseline == "" || config.CurrentSchema == "" || (config.Checks == "" && config.AccessChecks == "") || config.Collection == "" || config.PlanBaseline == "" || config.Budget == "" || config.CoveragePolicy == "" || config.FromVersion < 0 || config.FromVersion > 9999 || (config.UpgradeFixture != "" && config.FromVersion == 0) || (config.FromVersion > 0 && (config.UpgradeFixture == "" || config.Fixture == "")) {
		return config, contract, errors.New("use a complete version 1 release config; upgrades need both upgrade_fixture and current-schema fixture")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return config, contract, err
	}
	if config.Directory == "" {
		config.Directory = base
	} else {
		config.Directory = dataAPISyncPath(base, config.Directory)
	}
	info, err := os.Stat(config.Directory)
	if err != nil || !info.IsDir() {
		return config, contract, errors.New("project directory is unavailable")
	}
	for _, input := range []*string{&config.DevelopmentConfig, &config.Baseline, &config.CurrentSchema, &config.Checks, &config.AccessChecks, &config.Fixture, &config.Collection, &config.PlanBaseline, &config.Budget, &config.CoveragePolicy, &config.Dependencies, &config.Approval, &config.UpgradeFixture} {
		if *input == "" {
			continue
		}
		*input = dataAPISyncPath(config.Directory, *input)
		file, err := openCustomerFile(*input)
		if err != nil {
			return config, contract, errors.New("a configured input file is unavailable")
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1048576 {
			return config, contract, errors.New("release inputs must be regular files under 1 MiB")
		}
	}
	if config.FromVersion == 0 && config.Fixture != "" {
		developmentPath := config.DevelopmentConfig
		if developmentPath == "" {
			developmentPath = filepath.Join(config.Directory, "data-api.dev.json")
		}
		var development struct {
			Seed string `json:"seed"`
		}
		if verificationJSON(developmentPath, &development, 65536) != nil || development.Seed == "" {
			return config, contract, errors.New("clean rebuild fixture must also be configured as the development seed")
		}
		seedInfo, seedErr := os.Stat(dataAPISyncPath(config.Directory, development.Seed))
		fixtureInfo, fixtureErr := os.Stat(config.Fixture)
		if seedErr != nil || fixtureErr != nil || !os.SameFile(seedInfo, fixtureInfo) {
			return config, contract, errors.New("clean rebuild fixture must match the configured development seed")
		}
	}
	contract, err = readDataAPIContractFile(config.CurrentSchema)
	if err != nil {
		return config, contract, errors.New("invalid current_schema contract")
	}
	if _, err = readDataAPIContractFile(config.Baseline); err != nil {
		return config, contract, errors.New("invalid baseline contract")
	}
	policy, err := coveragePolicy(config.CoveragePolicy)
	if err != nil {
		return config, contract, errors.New("invalid coverage policy")
	}
	if _, err = evaluateDataAPICoverage(contract, policy, nil, nil, nil); err != nil {
		return config, contract, err
	}
	return config, contract, nil
}
func dataAPIReleaseArguments(config dataAPIReleaseConfig, dir string) [][]string {
	preview := []string{"--json", "data-api", "migration", "preview", "--directory", config.Directory, "--baseline", config.Baseline}
	verify := []string{"--json", "data-api", "verify", "--directory", config.Directory}
	plans := []string{"--json", "data-api", "explain", "--directory", config.Directory, "--collection", config.Collection, "--baseline", config.PlanBaseline, "--check", "--budget", config.Budget}
	add := func(args []string, flag, value string) []string {
		if value != "" {
			return append(args, flag, value)
		}
		return args
	}
	for _, entry := range [][2]string{{"--dependencies", config.Dependencies}, {"--approval", config.Approval}} {
		preview = add(preview, entry[0], entry[1])
	}
	if config.FromVersion > 0 {
		preview = append(preview, "--from-version", strconv.Itoa(config.FromVersion), "--fixture", config.UpgradeFixture)
		preview = add(preview, "--data-check", config.Checks)
	}
	verify = add(verify, "--checks", config.Checks)
	verify = add(verify, "--access-checks", config.AccessChecks)
	verify = add(verify, "--fixture", config.Fixture)
	plans = add(plans, "--fixture", config.Fixture)
	preview = add(preview, "--config", config.DevelopmentConfig)
	verify = add(verify, "--config", config.DevelopmentConfig)
	plans = add(plans, "--config", config.DevelopmentConfig)
	coverage := []string{"--json", "data-api", "verify", "coverage", "--schema", config.CurrentSchema, "--policy", config.CoveragePolicy, "--verification", filepath.Join(dir, "verification.json"), "--plans", filepath.Join(dir, "plans.json"), "--baseline-schema", config.Baseline, "--check"}
	return [][]string{preview, verify, plans, coverage}
}
func runDataAPIRelease(ctx context.Context, config dataAPIReleaseConfig, contract dataAPIContract, runner dataAPIReleaseRunner) (report dataAPIReleaseReport, runErr error) {
	report = dataAPIReleaseReport{Version: 1, Command: "data-api verify release", Status: "failed", Fingerprint: contract.Fingerprint, Cleanup: "passed", Steps: []dataAPIReleaseStep{{Name: "migrations", Status: "blocked"}, {Name: "verification", Status: "blocked"}, {Name: "query_budgets", Status: "blocked"}, {Name: "coverage", Status: "blocked"}}}
	dir, err := os.MkdirTemp("", "gregale-data-api-release-")
	if err != nil {
		return report, err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			report.Cleanup = "failed"
			report.Status = "failed"
			if runErr == nil {
				runErr = errors.New("release output cleanup failed")
			}
		}
	}()
	arguments := dataAPIReleaseArguments(config, dir)
	commands := []string{"data-api migration preview", "data-api verify", "data-api explain", "data-api verify coverage"}
	for i, args := range arguments {
		if ctx.Err() != nil {
			report.Steps[i].Status = "failed"
			report.Steps[i].Error = "Release checks interrupted"
			return report, nil
		}
		raw, runErr := runner(ctx, args)
		var result struct {
			Version     int    `json:"version"`
			Command     string `json:"command"`
			Status      string `json:"status"`
			Cleanup     string `json:"cleanup"`
			Fingerprint string `json:"schema_fingerprint"`
			Steps       map[string]struct {
				Status string `json:"status"`
			} `json:"steps"`
			Compatibility *struct {
				Fingerprint string `json:"current_fingerprint"`
			} `json:"compatibility"`
		}
		valid := len(raw) <= 1048576 && json.Unmarshal(raw, &result) == nil && result.Version == 1 && result.Command == commands[i]
		if i == 0 && result.Steps != nil {
			result.Cleanup = result.Steps["cleanup"].Status
		}
		step := &report.Steps[i]
		step.Status = "failed"
		if !valid {
			step.Error = "Check did not produce a bounded version 1 JSON report; inspect its inputs and local runtime"
			if i < 3 {
				report.Cleanup = "unknown"
			}
			return report, nil
		}
		step.Report = json.RawMessage(raw)
		if i < 3 && result.Cleanup != "passed" {
			report.Cleanup = "unknown"
			if result.Cleanup == "failed" {
				report.Cleanup = "failed"
			}
		}
		fingerprint := result.Fingerprint
		if i == 0 && result.Compatibility != nil {
			fingerprint = result.Compatibility.Fingerprint
		}
		if runErr != nil || result.Status != "passed" || (i < 3 && result.Cleanup != "passed") {
			step.Error = "Check failed; review its report before releasing"
			return report, nil
		}
		if fingerprint != contract.Fingerprint {
			step.Error = "Check schema differs from current_schema; refresh the candidate contract and rerun"
			return report, nil
		}
		step.Status = "passed"
		if i == 1 || i == 2 {
			file := "verification.json"
			if i == 2 {
				file = "plans.json"
			}
			if err := os.WriteFile(filepath.Join(dir, file), raw, 0600); err != nil {
				step.Status = "failed"
				step.Error = "Could not save temporary check evidence"
				return report, err
			}
		}
	}
	report.Status = "passed"
	return report, nil
}

type dataAPIReleaseOutput struct{ bytes.Buffer }

func (output *dataAPIReleaseOutput) Write(raw []byte) (int, error) {
	if output.Len()+len(raw) > 1048576 {
		return 0, errors.New("release child report exceeds 1 MiB")
	}
	return output.Buffer.Write(raw)
}
func dataAPIReleaseProcess(ctx context.Context, args []string) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	stage, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(stage, executable, args...) //nolint:gosec // Fixed subcommands, structured validated arguments; no shell.
	var output dataAPIReleaseOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 240 * time.Second
	err = command.Run()
	return output.Bytes(), err
}
func cmdDataAPIVerifyRelease(args []string) int {
	fs := newFlagSet("data-api verify release", flag.ContinueOnError)
	path := fs.String("config", "", "version 1 release config")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *path == "" {
		return printErr("Invalid release options", errors.New("provide --config FILE"))
	}
	config, contract, err := readDataAPIReleaseConfig(*path)
	if err != nil {
		return printErr("Invalid release config", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	report, err := runDataAPIRelease(ctx, config, contract, dataAPIReleaseProcess)
	if jsonOutput {
		raw, _ := json.Marshal(report)
		if _, err := fmt.Fprintln(osStdout, string(raw)); err != nil {
			return printErr("Output failed", err)
		}
	} else {
		if _, err := fmt.Fprintf(osStdout, "Data API release check: %s\n", report.Status); err != nil {
			return printErr("Output failed", err)
		}
		for _, step := range report.Steps {
			if _, err := fmt.Fprintf(osStdout, "%s: %s", step.Name, step.Status); err != nil {
				return printErr("Output failed", err)
			}
			if step.Error != "" {
				if _, err := fmt.Fprintf(osStdout, " — %s", step.Error); err != nil {
					return printErr("Output failed", err)
				}
			}
			if _, err := fmt.Fprintln(osStdout); err != nil {
				return printErr("Output failed", err)
			}
		}
		if _, err := fmt.Fprintf(osStdout, "Cleanup: %s\n", report.Cleanup); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err != nil || report.Status != "passed" {
		return 1
	}
	return 0
}

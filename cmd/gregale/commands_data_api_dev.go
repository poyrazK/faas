package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

type dataAPIDevOptions struct {
	AccessChecks      string `json:"access_checks,omitempty"`
	Verify            bool   `json:"verify,omitempty"`
	PlanCheck         bool   `json:"plan_check,omitempty"`
	PlanBudget        string `json:"plan_budget,omitempty"`
	Explain           bool   `json:"explain,omitempty"`
	Collection        string `json:"collection,omitempty"`
	PlanBaseline      string `json:"plan_baseline,omitempty"`
	PlanScenario      string `json:"plan_scenario,omitempty"`
	Preview           bool   `json:"preview,omitempty"`
	Dependencies      string `json:"dependencies,omitempty"`
	FromVersion       int    `json:"from_version,omitempty"`
	Fixture           string `json:"fixture,omitempty"`
	DataCheck         string `json:"data_check,omitempty"`
	Directory         string `json:"directory"`
	Output            string `json:"output"`
	Port              int    `json:"port"`
	Check             bool   `json:"check"`
	Validators        bool   `json:"validators"`
	CheckTypes        bool   `json:"check_types"`
	Once              bool   `json:"once"`
	JSON              bool   `json:"json"`
	Watch             bool   `json:"watch"`
	Baseline          string `json:"baseline"`
	CheckBreaking     bool   `json:"check_breaking"`
	Approval          string `json:"approval"`
	AcceptedContracts string `json:"accepted_contracts"`
	Scenario          string `json:"scenario"`
	Replay            string `json:"replay"`
	CLI               string `json:"cli"`
	Config            string `json:"config"`
	OutputSet         bool   `json:"output_set"`
}

func cmdDataAPIDev(args []string) int { return runDataAPIDev(args, nil) }

func runDataAPIDev(args []string, rehearsal *dataAPIMigrationOptions) int {
	preview := rehearsal != nil && !rehearsal.Explain && !rehearsal.Verify
	if rehearsal == nil {
		rehearsal = &dataAPIMigrationOptions{}
	}
	dependencies := rehearsal.Dependencies
	fs := newFlagSet("data-api dev", flag.ContinueOnError)
	directory := fs.String("directory", ".", "Data API project directory")
	config := fs.String("config", "", "project-relative development config (default: data-api.dev.json)")
	output := fs.String("output", "client/src/database.types.ts", "generated types path relative to the project")
	port := fs.Int("port", 8080, "loopback gateway port (0 selects a free port)")
	check := fs.Bool("check", false, "run configured client checks")
	validators := fs.Bool("validators", false, "generate runtime table and RPC argument validators alongside types")
	checkTypes := fs.Bool("check-types", false, "compare committed types without rewriting them (requires --once)")
	once := fs.Bool("once", false, "verify the local workflow and exit, removing containers")
	watch := fs.Bool("watch", true, "watch migrations and refresh the local API and types (disabled by --once)")
	baseline := fs.String("baseline", "", "read-only contract baseline (default: last successful local contract)")
	scenario := fs.String("scenario", "", "select one named replay scenario (requires --replay)")
	replay := fs.String("replay", "", "replay saved requests relative to the project (requires --once)")
	checkBreaking := fs.Bool("check-breaking", false, "reject breaking schema changes; requires an existing baseline")
	acceptedContracts := fs.String("accepted-contracts", "", "project-relative reviewed runtime contract policy")
	approval := fs.String("approval", "", "reviewed compatibility approvals (requires --baseline and --check-breaking)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if (*approval != "" && (*baseline == "" || !*checkBreaking)) || (*checkTypes && !*once) || fs.NArg() != 0 || *port < 0 || *port > 65535 || *directory == "" || *output == "" || (*replay != "" && !*once) || (*scenario != "" && *replay == "") {
		PrintUsage(os.Stderr, "usage: gregale data-api dev [--directory DIR] [--config FILE] [--output FILE] [--port PORT] [--check] [--validators] [--check-types --once] [--once] [--watch=false] [--baseline FILE] [--check-breaking] [--approval FILE] [--accepted-contracts FILE] [--replay FILE --once] [--scenario NAME]", "data-api")
		return 1
	}
	base, err := filepath.Abs(*directory)
	if err != nil {
		return printErr("Invalid project directory", err)
	}
	if dependencies != "" {
		dependencies = dataAPISyncPath(base, dependencies)
		if _, err := readDataAPIConsumers(dependencies); err != nil {
			return printErr("Invalid dependency manifest", err)
		}
	}
	for _, input := range []*string{&rehearsal.Fixture, &rehearsal.DataCheck, &rehearsal.Collection, &rehearsal.PlanBaseline, &rehearsal.PlanBudget, &rehearsal.AccessChecks} {
		if *input != "" {
			*input = dataAPISyncPath(base, *input)
		}
	}
	configPath := filepath.Join(base, "data-api.dev.json")
	if *config != "" {
		*config = dataAPISyncPath(base, *config)
		configPath = *config
	}
	if info, statErr := os.Lstat(configPath); statErr == nil {
		if !info.Mode().IsRegular() || info.Size() > 65536 {
			return printErr("Invalid development config", errors.New("use a regular JSON file under 64 KiB"))
		}
	} else if *config != "" || !errors.Is(statErr, os.ErrNotExist) {
		return printErr("Invalid development config", statErr)
	} else if info, statErr := os.Stat(filepath.Join(base, "migrations", "sql")); statErr != nil || !info.IsDir() {
		return printErr("Invalid project directory", errors.New("configure data-api.dev.json or initialize a data-api-starter project"))
	}
	if *replay != "" {
		*replay = dataAPISyncPath(base, *replay)
		info, replayErr := os.Lstat(*replay)
		if replayErr != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			return printErr("Invalid replay collection", errors.New("use an existing regular file under 64 KiB"))
		}
	}
	target := dataAPISyncPath(base, *output)
	if *replay != "" {
		replayInfo, replayErr := os.Stat(*replay)
		outputInfo, outputErr := os.Stat(target)
		if target == *replay || (replayErr == nil && outputErr == nil && os.SameFile(replayInfo, outputInfo)) {
			return printErr("Invalid output", errors.New("types output and replay collection must be different files"))
		}
	}
	contractPath := filepath.Join(base, ".gregale", "data-api-contract.json")
	if *baseline != "" {
		*baseline = dataAPISyncPath(base, *baseline)
		contractPath = *baseline
	}
	baselineInfo, baselineErr := os.Stat(contractPath)
	outputInfo, outputErr := os.Stat(target)
	if target == contractPath || (baselineErr == nil && outputErr == nil && os.SameFile(baselineInfo, outputInfo)) {
		return printErr("Invalid output", errors.New("types output and compatibility baseline must be different files"))
	}
	if *baseline != "" || *checkBreaking {
		contract, contractErr := readDataAPIContractFile(contractPath)
		if contractErr != nil {
			return printErr("Invalid compatibility baseline", fmt.Errorf("%w; initialize a local contract with data-api dev --once or use --baseline FILE", contractErr))
		}
		if *approval != "" {
			*approval = dataAPISyncPath(base, *approval)
			approved, approvalErr := readDataAPIApprovalFile(*approval)
			if approvalErr != nil {
				return printErr("Invalid compatibility approval", approvalErr)
			}
			if approved.BaselineFingerprint != contract.Fingerprint {
				return printErr("Invalid compatibility approval", errors.New("baseline fingerprint does not match"))
			}
			info, statErr := os.Stat(*approval)
			if target == *approval || (statErr == nil && outputErr == nil && os.SameFile(info, outputInfo)) {
				return printErr("Invalid output", errors.New("types output and compatibility approval must be different files"))
			}
		}
	}
	if *acceptedContracts != "" {
		*acceptedContracts = dataAPISyncPath(base, *acceptedContracts)
		if _, err := readDataAPIAcceptedContractsFile(*acceptedContracts); err != nil {
			return printErr("Invalid accepted contracts", err)
		}
		for _, path := range []string{target, contractPath, configPath, *replay, *approval} {
			if path != "" && dataAPIContractOutputAliases(*acceptedContracts, path) {
				return printErr("Invalid accepted contracts", errors.New("policy must differ from types, baseline, config, replay and approval files"))
			}
		}
	}
	cli, err := os.Executable()
	if err != nil {
		return printErr("Could not locate the contract comparator", err)
	}
	if info, statErr := os.Lstat(target); statErr == nil && !info.Mode().IsRegular() {
		return printErr("Invalid output", errors.New("types output must be a regular file"))
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return printErr("Invalid output", statErr)
	}
	for _, tool := range []string{"node", "npm", "docker"} {
		if _, err = exec.LookPath(tool); err != nil {
			return printErr("Missing local development prerequisite", fmt.Errorf("install %s before running data-api dev", tool))
		}
	}
	runtime, err := os.MkdirTemp("", "gregale-data-api-dev-")
	if err != nil {
		return printErr("Could not prepare local runtime", err)
	}
	defer func() { _ = os.RemoveAll(runtime) }()
	if err = templates.Materialize("data-api", runtime); err != nil {
		return printErr("Could not prepare local runtime", err)
	}
	for target, source := range map[string]string{
		"migrate.mjs":                 "data-api-starter/migrations/migrate.mjs",
		"rpc-permissions.mjs":         "data-api-starter/migrations/rpc-permissions.mjs",
		"rpc-runtime/types.mjs":       "data-api/types.mjs",
		"rpc-runtime/type-render.mjs": "data-api/type-render.mjs",
		"rpc-runtime/config.mjs":      "data-api/config.mjs",
	} {
		content, readErr := templates.FS.ReadFile(source)
		if readErr != nil {
			return printErr("Could not prepare local migrations", readErr)
		}
		path := filepath.Join(runtime, target)
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return printErr("Could not prepare local migrations", err)
		}
		if err = os.WriteFile(path, content, 0o600); err != nil {
			return printErr("Could not prepare local migrations", err)
		}
	}
	outputSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "output" {
			outputSet = true
		}
	})
	options, err := json.Marshal(dataAPIDevOptions{Explain: rehearsal.Explain, Verify: rehearsal.Verify, AccessChecks: rehearsal.AccessChecks, PlanCheck: rehearsal.PlanCheck, PlanBudget: rehearsal.PlanBudget, Collection: rehearsal.Collection, PlanBaseline: rehearsal.PlanBaseline, PlanScenario: rehearsal.PlanScenario, Preview: preview, Dependencies: dependencies, FromVersion: rehearsal.FromVersion, Fixture: rehearsal.Fixture, DataCheck: rehearsal.DataCheck, Directory: base, Output: target, Port: *port, Check: *check, CheckTypes: *checkTypes, Validators: *validators, Once: *once, JSON: jsonOutput, Watch: *watch, Baseline: *baseline, CheckBreaking: *checkBreaking, Approval: *approval, AcceptedContracts: *acceptedContracts, CLI: cli, Replay: *replay, Scenario: *scenario, Config: *config, OutputSet: outputSet})
	if err != nil {
		return printErr("Could not prepare local options", err)
	}
	path := filepath.Join(runtime, "dev-options.json")
	if err = os.WriteFile(path, options, 0o600); err != nil {
		return printErr("Could not prepare local options", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	install, cancel := context.WithTimeout(ctx, 5*time.Minute)
	err = runDataAPISyncCommand(install, dataAPISyncCommand{Command: []string{"npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund"}, Directory: runtime})
	cancel()
	if err != nil {
		return printErr("Could not install local runtime dependencies", err)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(runtime, "dev.mjs"), path) //nolint:gosec // Embedded runtime and private options file; no shell.
	command.Dir, command.Stdout, command.Stderr = runtime, osStdout, osStderr
	command.WaitDelay = 210 * time.Second
	// Allow Node's signal handler to remove its containers before force-killing it.
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	err = command.Run()
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		if preview || rehearsal.Explain || rehearsal.Verify || *replay != "" {
			return printErr("Local verification interrupted", ctx.Err())
		}
		return 0
	}
	if err != nil {
		return printErr("Local Data API failed", err)
	}
	return 0
}

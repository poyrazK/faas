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
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

func cmdAutomations(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale automations <init|check|diff|list|get|health|runs|diagnose|pause|resume|revisions|restore|delete|validate|simulate|apply|publish|publish-policy|failure-policy|failure-resume>", "automations")
		return 1
	}
	switch args[0] {
	case "failure-policy":
		return cmdAutomationsFailurePolicy(args[1:])
	case "failure-resume":
		return cmdAutomationsFailureResume(args[1:])
	case "publish-policy":
		return cmdAutomationsPublishPolicy(args[1:])
	case "diff":
		return cmdAutomationsDiff(args[1:])
	case "check":
		return cmdAutomationsCheck(args[1:])
	case "init":
		return cmdAutomationsInit(args[1:])
	case "list":
		return cmdAutomationsList(args[1:])
	case "get":
		return cmdAutomationsGet(args[1:])
	case "runs":
		return cmdAutomationsRuns(args[1:])
	case "diagnose":
		return cmdAutomationsDiagnose(args[1:])
	case "health":
		return cmdAutomationsHealth(args[1:])
	case "pause":
		return cmdAutomationsPause(args[1:])
	case "resume":
		return cmdAutomationsResume(args[1:])
	case "revisions":
		return cmdAutomationsRevisions(args[1:])
	case "restore":
		return cmdAutomationsRestore(args[1:])
	case "delete":
		return cmdAutomationsDelete(args[1:])
	case "validate":
		return cmdAutomationsValidate(args[1:])
	case "simulate":
		return cmdAutomationsSimulate(args[1:])
	case "apply":
		return cmdAutomationsApply(args[1:])
	case "publish":
		return cmdAutomationsPublish(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown automations subcommand: %s", args[0]), "automations")
		return 1
	}
}

func cmdAutomationsPause(args []string) int {
	return cmdAutomationsSetEnabled(args, false)
}

func cmdAutomationsResume(args []string) int {
	return cmdAutomationsSetEnabled(args, true)
}

func cmdAutomationsSetEnabled(args []string, enabled bool) int {
	verb := "pause"
	action := "Paused"
	if enabled {
		verb, action = "resume", "Resumed"
	}
	fs := newFlagSet("automations "+verb, flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	expectedVersion := fs.Int64("expected-version", 0, "current automation version")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *expectedVersion <= 0 {
		PrintUsage(os.Stderr, "usage: gregale automations "+verb+" --app <slug> --name <name> --expected-version <n>", "automations")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.SetAutomationEnabled(context.Background(), *app, *name, api.SetAutomationEnabledRequest{ExpectedVersion: *expectedVersion, Enabled: &enabled})
	if err != nil {
		return printErr("Could not "+verb+" automation", err)
	}
	return printAutomationEnabled(action, result)
}

func cmdAutomationsHealth(args []string) int {
	fs := newFlagSet("automations health", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	createdAfter := fs.String("created-after", "", "inclusive RFC3339 window start")
	createdBefore := fs.String("created-before", "", "inclusive RFC3339 window end")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" {
		PrintUsage(os.Stderr, "usage: gregale automations health --app <slug> --name <name> [--created-after RFC3339] [--created-before RFC3339]", "automations")
		return 1
	}
	options, err := parseAutomationHealthOptions(*createdAfter, *createdBefore, time.Now().UTC())
	if err != nil {
		return printErr("Invalid automation health time range", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetAutomationHealth(context.Background(), *app, *name, options)
	if err != nil {
		return printErr("Could not get automation health", err)
	}
	return printAutomationHealth(result)
}

func parseAutomationHealthOptions(createdAfter, createdBefore string, now time.Time) (api.AutomationHealthOptions, error) {
	options := api.AutomationHealthOptions{}
	if createdAfter != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, createdAfter)
		if parseErr != nil {
			return options, fmt.Errorf("--created-after must be RFC3339: %w", parseErr)
		}
		value = value.UTC()
		options.CreatedAfter = &value
	}
	if createdBefore != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, createdBefore)
		if parseErr != nil {
			return options, fmt.Errorf("--created-before must be RFC3339: %w", parseErr)
		}
		value = value.UTC()
		options.CreatedBefore = &value
	}
	windowEnd := now
	if options.CreatedBefore != nil {
		windowEnd = *options.CreatedBefore
	}
	windowStart := windowEnd.Add(-api.WorkflowAutomationHealthDefaultRange)
	if options.CreatedAfter != nil {
		windowStart = *options.CreatedAfter
	}
	if windowEnd.After(now) || windowStart.After(windowEnd) || windowEnd.Sub(windowStart) > api.WorkflowAutomationHealthMaxRange {
		return options, errors.New("window must be non-future, ordered, and no longer than 30 days")
	}
	return options, nil
}

func cmdAutomationsRevisions(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale automations revisions <list|show>", "automations")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdAutomationsRevisionsList(args[1:])
	case "show":
		return cmdAutomationsRevisionsShow(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown automation revisions subcommand: %s", args[0]), "automations")
		return 1
	}
}

func cmdAutomationsRevisionsList(args []string) int {
	fs := newFlagSet("automations revisions list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	limit := fs.Int("limit", 50, "page size (1..100)")
	offset := fs.Int("offset", 0, "number of revisions to skip")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *limit < 1 || *limit > 100 || *offset < 0 {
		PrintUsage(os.Stderr, "usage: gregale automations revisions list --app <slug> --name <name> [--limit 1..100] [--offset <n>]", "automations")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.ListAutomationRevisions(context.Background(), *app, *name, *limit, *offset)
	if err != nil {
		return printErr("Could not list automation revisions", err)
	}
	return printAutomationRevisionList(result)
}

func cmdAutomationsRevisionsShow(args []string) int {
	fs := newFlagSet("automations revisions show", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	revision := fs.Int64("revision", 0, "published revision number")
	definitionOut := fs.String("definition-out", "", "export this definition as JSON to a new file")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *revision <= 0 {
		PrintUsage(os.Stderr, "usage: gregale automations revisions show --app <slug> --name <name> --revision <n> [--definition-out <path>]", "automations")
		return 1
	}
	if *definitionOut != "" && strings.TrimSpace(*definitionOut) == "" {
		return printErr("Invalid definition output path", errors.New("path cannot be blank"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetAutomationRevision(context.Background(), *app, *name, *revision)
	if err != nil {
		return printErr("Could not get automation revision", err)
	}
	return printAutomationRevisionDetails(result, *definitionOut)
}

func cmdAutomationsRestore(args []string) int {
	fs := newFlagSet("automations restore", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	revision := fs.Int64("revision", 0, "published revision number to restore")
	expectedVersion := fs.Int64("expected-version", -1, "current automation version (0 if deleted)")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *revision <= 0 || *expectedVersion < 0 {
		PrintUsage(os.Stderr, "usage: gregale automations restore --app <slug> --name <name> --revision <n> --expected-version <n>", "automations")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.RestoreAutomationRevision(context.Background(), *app, *name, *revision, api.RestoreAutomationRevisionRequest{ExpectedVersion: *expectedVersion})
	if err != nil {
		return printErr("Could not restore automation revision", err)
	}
	return printAutomationRevisionRestore(*revision, result)
}

func cmdAutomationsDelete(args []string) int {
	fs := newFlagSet("automations delete", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	expectedVersion := fs.Int64("expected-version", 0, "current automation version")
	yes := fs.Bool("yes", false, "confirm automation deletion")
	restoreManifest := fs.Bool("restore-manifest", false, "allow the current YAML definition to own this automation again")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *expectedVersion <= 0 {
		PrintUsage(os.Stderr, "usage: gregale automations delete --app <slug> --name <name> --expected-version <n> --yes [--restore-manifest]", "automations")
		return 1
	}
	if !*yes {
		return printErr("Confirmation required", errors.New("automation deletion requires --yes"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteAutomation(context.Background(), *app, *name, *expectedVersion, *restoreManifest); err != nil {
		return printErr("Could not delete automation", err)
	}
	return printAutomationDelete(*name, *expectedVersion, *restoreManifest)
}

func cmdAutomationsSimulate(args []string) int {
	fs := newFlagSet("automations simulate", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	file := fs.String("file", "", "automation definition YAML or JSON file")
	inputFile := fs.String("input-file", "", "sample workflow input JSON file")
	mockOutputsFile := fs.String("mock-outputs-file", "", "JSON object mapping action step names to mock outputs")
	mockItemOutputsFile := fs.String("mock-item-outputs-file", "", "JSON object mapping for_each step names to output arrays")
	mockItemAttemptsFile := fs.String("mock-item-attempts-file", "", "JSON object mapping loops and zero-based item indexes to attempt outcomes")
	mockAttemptsFile := fs.String("mock-attempts-file", "", "JSON object mapping step names to ordered attempt outcome arrays")
	requireComplete := fs.Bool("require-complete", false, "return exit code 1 if sample mocks leave steps unresolved")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*file) == "" {
		PrintUsage(os.Stderr, "usage: gregale automations simulate --app <slug> --file <path> [--input-file <path>] [--mock-outputs-file <path>] [--mock-item-outputs-file <path>] [--mock-attempts-file <path>] [--mock-item-attempts-file <path>] [--require-complete]", "automations")
		return 1
	}
	definition, ok := readAutomationDefinition(*file)
	if !ok {
		return 1
	}
	request, err := loadAutomationSimulation(definition, *inputFile, *mockOutputsFile, *mockItemOutputsFile, *mockAttemptsFile, *mockItemAttemptsFile)
	if err != nil {
		return printErr("Invalid automation simulation input", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.SimulateAutomation(context.Background(), *app, request)
	if err != nil {
		return printErr("Automation simulation failed", err)
	}
	return printAutomationSimulation(definition.Name, result, *requireComplete)
}

func cmdAutomationsList(args []string) int {
	fs := newFlagSet("automations list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" {
		PrintUsage(os.Stderr, "usage: gregale automations list --app <slug>", "automations")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.ListAutomations(context.Background(), *app)
	if err != nil {
		return printErr("Could not list automations", err)
	}
	return printAutomationList(result)
}

func cmdAutomationsGet(args []string) int {
	fs := newFlagSet("automations get", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	definitionOut := fs.String("definition-out", "", "export the selected definition as JSON to a new file")
	published := fs.Bool("published", false, "export the published definition instead of the draft")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || (*published && *definitionOut == "") {
		PrintUsage(os.Stderr, "usage: gregale automations get --app <slug> --name <name> [--definition-out <path> [--published]]", "automations")
		return 1
	}
	if *definitionOut != "" && strings.TrimSpace(*definitionOut) == "" {
		return printErr("Invalid definition output path", errors.New("path cannot be blank"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetAutomation(context.Background(), *app, *name)
	if err != nil {
		return printErr("Could not get automation", err)
	}
	return printAutomationDetails(result, *definitionOut, *published)
}

func cmdAutomationsValidate(args []string) int {
	fs := newFlagSet("automations validate", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	file := fs.String("file", "", "automation definition YAML or JSON file")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*file) == "" {
		PrintUsage(os.Stderr, "usage: gregale automations validate --app <slug> --file <path>", "automations")
		return 1
	}
	definition, ok := readAutomationDefinition(*file)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.ValidateAutomation(context.Background(), *app, api.ValidateAutomationRequest{Definition: definition})
	if err != nil {
		return printErr("Automation validation failed", err)
	}
	return printAutomationValidation(definition.Name, result)
}

func cmdAutomationsApply(args []string) int {
	fs := newFlagSet("automations apply", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	file := fs.String("file", "", "automation definition YAML or JSON file")
	expectedVersion := fs.Int64("expected-version", -1, "current automation version (0 for a new draft)")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*file) == "" || *expectedVersion < 0 {
		PrintUsage(os.Stderr, "usage: gregale automations apply --app <slug> --file <path> --expected-version <n>", "automations")
		return 1
	}
	definition, ok := readAutomationDefinition(*file)
	if !ok {
		return 1
	}
	if strings.TrimSpace(definition.Name) == "" {
		return printErr("Invalid automation definition", errors.New("definition name is required"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.SaveAutomationDraft(context.Background(), *app, definition.Name, api.SaveAutomationDraftRequest{ExpectedVersion: *expectedVersion, Definition: definition})
	if err != nil {
		return printErr("Could not save automation draft", err)
	}
	return printAutomationMutation("Saved draft", result)
}

func cmdAutomationsPublish(args []string) int {
	fs := newFlagSet("automations publish", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	expectedVersion := fs.Int64("expected-version", -1, "current automation version")
	takeOverManifest := fs.Bool("take-over-manifest", false, "explicitly replace YAML ownership for this name")
	requireCoverage := fs.Bool("require-coverage", false, "require scenario coverage before publishing; requires --scenarios")
	scenarioPath := fs.String("scenarios", "", "check scenario suite against the saved draft before publishing")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	scenariosProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "scenarios" {
			scenariosProvided = true
		}
	})
	if *requireCoverage && strings.TrimSpace(*scenarioPath) == "" {
		return printErr("Missing coverage scenarios", errors.New("--require-coverage requires --scenarios"))
	}
	if scenariosProvided && strings.TrimSpace(*scenarioPath) == "" {
		return printErr("Invalid automation scenarios", errors.New("--scenarios requires a nonempty suite path"))
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *expectedVersion < 0 {
		PrintUsage(os.Stderr, "usage: gregale automations publish --app <slug> --name <name> --expected-version <n> [--take-over-manifest] [--scenarios <path>] [--require-coverage]", "automations")
		return 1
	}
	var scenarios []loadedAutomationScenario
	if *scenarioPath != "" {
		var err error
		scenarios, err = loadAutomationScenarios(*scenarioPath)
		if err != nil {
			return printErr("Invalid automation scenarios", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *scenarioPath != "" {
		return publishAutomationWithScenarios(client, *app, *name, *expectedVersion, *takeOverManifest, scenarios, *requireCoverage)
	}
	result, err := client.PublishAutomation(context.Background(), *app, *name, api.PublishAutomationRequest{ExpectedVersion: *expectedVersion, TakeOverManifest: *takeOverManifest})
	if err != nil {
		return printErr("Could not publish automation", err)
	}
	return printAutomationMutation("Published", result)
}

func readAutomationDefinition(path string) (api.WorkflowSpec, bool) {
	file, err := openCustomerFile(path)
	if err != nil {
		printErr("Could not read automation definition", err)
		return api.WorkflowSpec{}, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, api.AutomationDefinitionMaxBytes+1))
	if err != nil {
		printErr("Could not read automation definition", err)
		return api.WorkflowSpec{}, false
	}
	if int64(len(data)) > api.AutomationDefinitionMaxBytes {
		printErr("Invalid automation definition", fmt.Errorf("file exceeds the %d-byte automation definition limit", api.AutomationDefinitionMaxBytes))
		return api.WorkflowSpec{}, false
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var definition api.WorkflowSpec
	if err := decoder.Decode(&definition); err != nil {
		printErr("Invalid automation definition", err)
		return api.WorkflowSpec{}, false
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("definition must contain exactly one YAML document")
		}
		printErr("Invalid automation definition", err)
		return api.WorkflowSpec{}, false
	}
	return definition, true
}

func loadAutomationSimulation(definition api.WorkflowSpec, inputPath, mockPath, itemMockPath, attemptMockPath string, itemAttemptPaths ...string) (api.SimulateAutomationRequest, error) {
	request := api.SimulateAutomationRequest{Definition: definition}
	var err error
	if inputPath != "" {
		request.Input, err = readSimulationJSONFile(inputPath, api.WorkflowRunInputMaxBytes)
		if err != nil {
			return request, fmt.Errorf("input file: %w", err)
		}
	}
	if mockPath != "" {
		request.MockOutputs, err = readAutomationMockOutputs(mockPath)
		if err != nil {
			return request, fmt.Errorf("mock outputs file: %w", err)
		}
	}
	if itemMockPath != "" {
		request.MockItemOutputs, err = readAutomationItemMockOutputs(itemMockPath)
		if err != nil {
			return request, fmt.Errorf("item mock outputs file: %w", err)
		}
	}
	if attemptMockPath != "" {
		request.MockAttempts, err = readAutomationMockAttempts(attemptMockPath)
		if err != nil {
			return request, fmt.Errorf("mock attempts file: %w", err)
		}
	}
	if len(itemAttemptPaths) > 0 && itemAttemptPaths[0] != "" {
		request.MockItemAttempts, err = readAutomationItemMockAttempts(itemAttemptPaths[0])
		if err != nil {
			return request, fmt.Errorf("item mock attempts file: %w", err)
		}
	}
	definitionJSON, err := json.Marshal(request.Definition)
	if err != nil {
		return request, fmt.Errorf("definition is not valid JSON: %w", err)
	}
	if int64(len(definitionJSON)) > api.AutomationDefinitionMaxBytes {
		return request, fmt.Errorf("definition exceeds the %d-byte limit", api.AutomationDefinitionMaxBytes)
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return request, fmt.Errorf("request is not valid JSON: %w", err)
	}
	if int64(len(requestJSON)) > api.AutomationSimulationRequestMaxBytes {
		return request, fmt.Errorf("simulation request exceeds the %d-byte limit", api.AutomationSimulationRequestMaxBytes)
	}
	return request, nil
}

func readAutomationMockAttempts(path string) (map[string][]api.AutomationSimulationMockAttempt, error) {
	raw, err := readSimulationJSONFile(path, api.AutomationSimulationRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	var attempts map[string][]api.AutomationSimulationMockAttempt
	if err := json.Unmarshal(raw, &attempts); err != nil || attempts == nil {
		return nil, errors.New("file must contain a JSON object mapping step names to attempt outcome arrays")
	}
	for name, outcomes := range attempts {
		if outcomes == nil {
			return nil, fmt.Errorf("attempt outcomes for %q must be a JSON array", name)
		}
		if len(outcomes) > api.WorkflowRetryMaxAttempts {
			return nil, fmt.Errorf("attempt outcomes for %q exceed the %d-attempt limit", name, api.WorkflowRetryMaxAttempts)
		}
		for _, outcome := range outcomes {
			if int64(len(outcome.Output)) > api.WorkflowRunInputMaxBytes || int64(len(outcome.Error)) > api.WorkflowRunInputMaxBytes {
				return nil, fmt.Errorf("an attempt mock for %q exceeds the %d-byte value limit", name, api.WorkflowRunInputMaxBytes)
			}
		}
	}
	return attempts, nil
}

func readSimulationJSONFile(path string, maxBytes int64) (json.RawMessage, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file exceeds the %d-byte limit", maxBytes)
	}
	if !json.Valid(data) {
		return nil, errors.New("file must contain one valid JSON value")
	}
	return json.RawMessage(data), nil
}

func readAutomationMockOutputs(path string) (map[string]json.RawMessage, error) {
	raw, err := readSimulationJSONFile(path, api.AutomationSimulationRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outputs); err != nil || outputs == nil {
		return nil, errors.New("file must contain a JSON object mapping step names to outputs")
	}
	for name, output := range outputs {
		if int64(len(output)) > api.WorkflowRunInputMaxBytes {
			return nil, fmt.Errorf("mock output for %q exceeds the %d-byte limit", name, api.WorkflowRunInputMaxBytes)
		}
	}
	return outputs, nil
}

func readAutomationItemMockOutputs(path string) (map[string][]json.RawMessage, error) {
	raw, err := readSimulationJSONFile(path, api.AutomationSimulationRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	var outputs map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &outputs); err != nil || outputs == nil {
		return nil, errors.New("file must contain a JSON object mapping for_each step names to output arrays")
	}
	for name, items := range outputs {
		if items == nil {
			return nil, fmt.Errorf("item outputs for %q must be a JSON array", name)
		}
		if len(items) > api.WorkflowForEachMaxItems {
			return nil, fmt.Errorf("item outputs for %q exceed the %d-item limit", name, api.WorkflowForEachMaxItems)
		}
		for _, output := range items {
			if int64(len(output)) > api.WorkflowRunInputMaxBytes {
				return nil, fmt.Errorf("an item output for %q exceeds the %d-byte limit", name, api.WorkflowRunInputMaxBytes)
			}
		}
	}
	return outputs, nil
}

func printAutomationValidation(name string, result api.ValidateAutomationResponse) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else if result.Valid {
		_, _ = fmt.Fprintf(osStdout, "Automation %q is valid.\n", name)
		if len(result.StepOrder) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Step order: %s\n", strings.Join(result.StepOrder, " -> "))
		}
		if result.NextFireAt != "" {
			_, _ = fmt.Fprintf(osStdout, "Next scheduled run: %s\n", result.NextFireAt)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Automation %q is invalid.\n", name)
		for _, issue := range result.Issues {
			_, _ = fmt.Fprintf(osStdout, "- %s\n", issue)
		}
	}
	if result.Valid {
		return 0
	}
	return 1
}

func printAutomationSimulation(name string, result api.SimulateAutomationResponse, requireComplete bool) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		status := "incomplete"
		if !result.DefinitionValid {
			status = "invalid"
		} else if result.Complete {
			status = "complete"
		}
		_, _ = fmt.Fprintf(osStdout, "Automation %q simulation: %s\n", name, status)
		if len(result.StepOrder) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Step order: %s\n", strings.Join(result.StepOrder, " -> "))
		}
		for _, issue := range result.Issues {
			_, _ = fmt.Fprintf(osStdout, "Issue: %s\n", issue)
		}
		for _, warning := range result.Warnings {
			_, _ = fmt.Fprintf(osStdout, "Warning: %s\n", warning)
		}
		if err := writeAutomationSimulationTrace(osStdout, result.Trace); err != nil {
			return printErr("Could not render simulation trace", err)
		}
	}
	if !result.DefinitionValid || len(result.Issues) > 0 || (requireComplete && !result.Complete) {
		return 1
	}
	return 0
}

func writeAutomationSimulationTrace(w io.Writer, trace []api.AutomationSimulationStep) error {
	if len(trace) == 0 {
		return nil
	}
	writer := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "STEP\tKIND\tSTATE\tREASON\tACTION\tINPUT\tOUTPUT"); err != nil {
		return err
	}
	for _, step := range trace {
		name, reason := step.StepName, step.Reason
		if step.ParentStep != "" {
			name = step.ParentStep + "/" + name
			if step.ItemIndex != nil {
				name += fmt.Sprintf("[%d]", *step.ItemIndex)
			}
		}
		if step.WhenMatched != nil {
			reason = strings.TrimSpace(reason + fmt.Sprintf(" when=%t", *step.WhenMatched))
		}
		if len(step.BlockedBy) > 0 {
			reason = strings.TrimSpace(reason + " blocked_by=" + strings.Join(step.BlockedBy, ","))
		}
		action := automationSimulationAction(step)
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, step.Kind, step.State, reason, action, compactSimulationValue(step.Input), compactSimulationValue(step.Output)); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func automationSimulationAction(step api.AutomationSimulationStep) string {
	if step.IntegrationID != "" {
		path := step.Path
		if step.RawQuery != "" {
			path += "?" + step.RawQuery
		}
		return fmt.Sprintf("integration %s %s %s", step.IntegrationID, step.Method, path)
	}
	if step.Run != "" {
		return "run " + step.Run
	}
	if step.Path != "" {
		method := step.Method
		if method == "" {
			method = "POST"
		}
		return method + " " + step.Path
	}
	if step.WaitFor != "" {
		return "wait " + step.WaitFor
	}
	return "-"
}

func compactSimulationValue(value json.RawMessage) string {
	if len(value) == 0 {
		return "-"
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		return string(value)
	}
	return compact.String()
}

func printAutomationMutation(action string, result api.AutomationResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(summarizeAutomation(result)))
	}
	_, _ = fmt.Fprintf(osStdout, "%s automation %q at version %d.\n", action, result.Name, result.Version)
	return 0
}

func printAutomationEnabled(action string, result api.AutomationResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(summarizeAutomation(result)))
	}
	_, _ = fmt.Fprintf(osStdout, "%s automatic starts for automation %q at version %d.\n", action, result.Name, result.Version)
	if result.FailurePaused {
		_, _ = fmt.Fprintln(osStdout, "Failure pause remains active. Inspect failure-policy and use failure-resume to clear it.")
	}
	return 0
}

type automationDeleteSummary struct {
	Name                     string `json:"name"`
	DeletedVersion           int64  `json:"deleted_version"`
	RestoreManifestRequested bool   `json:"restore_manifest_requested"`
}

func printAutomationDelete(name string, version int64, restoreManifest bool) int {
	summary := automationDeleteSummary{Name: name, DeletedVersion: version, RestoreManifestRequested: restoreManifest}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted automation %q at version %d.\n", name, version)
	if restoreManifest {
		_, _ = fmt.Fprintln(osStdout, "The current YAML definition may own this automation again if one is present.")
	}
	return 0
}

type automationRevisionSummary struct {
	Version        int64     `json:"version"`
	DefinitionHash string    `json:"definition_hash"`
	RecordedAt     time.Time `json:"recorded_at"`
	LegacySnapshot bool      `json:"legacy_snapshot"`
}

type automationRevisionListSummary struct {
	Total     int                         `json:"total"`
	Limit     int                         `json:"limit"`
	Offset    int                         `json:"offset"`
	Revisions []automationRevisionSummary `json:"revisions"`
}

type automationRevisionGetSummary struct {
	CheckEvidence *api.AutomationCheckEvidence `json:"check_evidence,omitempty"`
	automationRevisionSummary
	DefinitionExportedTo string `json:"definition_exported_to,omitempty"`
}

type automationRevisionRestoreSummary struct {
	automationMutationSummary
	RestoredFromRevision int64 `json:"restored_from_revision"`
}

func printAutomationRevisionList(result api.ListAutomationRevisionsResponse) int {
	summary := automationRevisionListSummary{
		Total: result.Total, Limit: result.Limit, Offset: result.Offset,
		Revisions: make([]automationRevisionSummary, 0, len(result.Revisions)),
	}
	for _, revision := range result.Revisions {
		summary.Revisions = append(summary.Revisions, summarizeAutomationRevision(revision))
	}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	if len(summary.Revisions) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No published revisions found (total: %d).\n", summary.Total)
		return 0
	}
	w := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "REVISION\tRECORDED AT\tDEFINITION HASH\tLEGACY SNAPSHOT"); err != nil {
		return printErr("Output failed", err)
	}
	for _, revision := range summary.Revisions {
		if _, err := fmt.Fprintf(w, "%d\t%s\t%s\t%t\n", revision.Version, revision.RecordedAt.Format(time.RFC3339), revision.DefinitionHash, revision.LegacySnapshot); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err := w.Flush(); err != nil {
		return printErr("Output failed", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Showing %d of %d revisions (offset %d).\n", len(summary.Revisions), summary.Total, summary.Offset)
	return 0
}

func printAutomationRevisionDetails(result api.AutomationRevisionResponse, definitionOut string) int {
	path := ""
	if definitionOut != "" {
		if err := writeAutomationDefinitionFile(definitionOut, result.Definition); err != nil {
			return printErr("Could not export automation revision definition", err)
		}
		path = definitionOut
	}
	summary := automationRevisionGetSummary{automationRevisionSummary: summarizeAutomationRevision(result), DefinitionExportedTo: path, CheckEvidence: result.CheckEvidence}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Automation revision %d\n  Recorded at: %s\n  Definition hash: %s\n  Legacy snapshot: %t\n", result.Version, result.RecordedAt.Format(time.RFC3339), result.DefinitionHash, result.LegacySnapshot)
	if evidence := result.CheckEvidence; evidence != nil {
		source := "Client-reported"
		if evidence.ServerVerified {
			source = "Server-verified"
		}
		_, _ = fmt.Fprintf(osStdout, "  %s checks: %s (draft version %d)\n  Checked definition hash: %s\n", source, evidence.CheckedAt.Format(time.RFC3339), evidence.CheckedVersion, evidence.DefinitionHash)
		for _, scenario := range evidence.Scenarios {
			_, _ = fmt.Fprintf(osStdout, "    %s: passed=%t, valid=%t, complete=%t\n", scenario.Name, scenario.Passed, scenario.DefinitionValid, scenario.Complete)
		}
		_, _ = fmt.Fprintf(osStdout, "  Coverage: required=%t, passed=%t, remaining=%d\n", evidence.CoverageRequired, evidence.CoveragePassed, evidence.CoverageRemaining)
		for _, exclusion := range evidence.Exclusions {
			_, _ = fmt.Fprintf(osStdout, "    Excluded %s [%s] (loop %q): %s\n", exclusion.Step, exclusion.Code, exclusion.Loop, exclusion.Reason)
		}
	} else {
		_, _ = fmt.Fprintln(osStdout, "  Check evidence: none recorded")
	}
	if path != "" {
		_, _ = fmt.Fprintf(osStdout, "  Exported definition to %s\n", path)
	}
	return 0
}

func printAutomationRevisionRestore(revision int64, result api.AutomationResponse) int {
	summary := automationRevisionRestoreSummary{automationMutationSummary: summarizeAutomation(result), RestoredFromRevision: revision}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Restored revision %d as draft automation %q at version %d.\n", revision, result.Name, result.Version)
	return 0
}

func summarizeAutomationRevision(result api.AutomationRevisionResponse) automationRevisionSummary {
	return automationRevisionSummary{Version: result.Version, DefinitionHash: result.DefinitionHash, RecordedAt: result.RecordedAt, LegacySnapshot: result.LegacySnapshot}
}

func printAutomationHealth(result api.AutomationHealthResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Automation health: %s/%s\nWindow: %s to %s\nRuns: %d (%d completed)\n", result.AppSlug, result.AutomationName, result.WindowStart.Format(time.RFC3339), result.WindowEnd.Format(time.RFC3339), result.RunCount, result.CompletedRunCount)
	_, _ = fmt.Fprintf(osStdout, "Active now: %d, queued: %d\n", result.ActiveRunCount, result.QueuedRunCount)
	if err := writeAutomationQueueHealth(osStdout, result.Queue); err != nil {
		return printErr("Output failed", err)
	}
	if result.CompletedRunCount == 0 {
		_, _ = fmt.Fprintln(osStdout, "Success rate: n/a (no completed runs)")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Success rate: %.1f%%\n", result.SuccessRate*100)
	}
	_, _ = fmt.Fprintf(osStdout, "Durations: p50 %s, p95 %s\n", formatAutomationDuration(result.P50DurationMS), formatAutomationDuration(result.P95DurationMS))
	if err := writeAutomationHealthStatuses(osStdout, result.StatusCounts); err != nil {
		return printErr("Output failed", err)
	}
	for _, latest := range []struct {
		label string
		run   *api.AutomationHealthRun
	}{{"Last run", result.LastRun}, {"Last success", result.LastSuccess}, {"Last failure", result.LastFailure}} {
		if err := writeAutomationHealthRun(osStdout, latest.label, latest.run); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err := writeAutomationHealthFailures(osStdout, result.FailedSteps); err != nil {
		return printErr("Output failed", err)
	}
	if err := writeAutomationHealthRunHints(osStdout, result); err != nil {
		return printErr("Output failed", err)
	}
	return 0
}

func writeAutomationHealthRunHints(w io.Writer, health api.AutomationHealthResponse) error {
	if !validAutomationRunSelection(health.AppSlug, health.AutomationName) || health.WindowStart.IsZero() || health.WindowEnd.IsZero() || health.WindowStart.After(health.WindowEnd) {
		return nil
	}
	app, name := automationCommandArgument(health.AppSlug), automationCommandArgument(health.AutomationName)
	base := fmt.Sprintf("gregale automations runs --app %s --name %s --created-after %s --created-before %s", app, name, health.WindowStart.UTC().Format(time.RFC3339Nano), health.WindowEnd.UTC().Format(time.RFC3339Nano))
	if _, err := fmt.Fprintf(w, "\nRun history for this window: %s\n", base); err != nil {
		return err
	}
	for _, status := range []string{"failed", "dead"} {
		if health.StatusCounts[status] > 0 || (health.LastFailure != nil && health.LastFailure.Status == status) {
			if _, err := fmt.Fprintf(w, "Inspect %s runs: %s --status %s\n", status, base, status); err != nil {
				return err
			}
		}
	}
	if health.LastFailure != nil && workflowUUIDPattern.MatchString(health.LastFailure.ID) {
		_, err := fmt.Fprintf(w, "Diagnose last failure: gregale automations diagnose --app %s --name %s --run %s\n", app, name, health.LastFailure.ID)
		return err
	}
	return nil
}

// Quote user-visible command arguments for POSIX shells without interpreting them.
func automationCommandArgument(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeAutomationQueueHealth(w io.Writer, queue *api.AutomationQueueHealth) error {
	if queue == nil {
		_, err := fmt.Fprintln(w, "Queue diagnostics: unavailable")
		return err
	}
	capacity := "available"
	if queue.AppAtCapacity {
		capacity = "full"
	}
	if _, err := fmt.Fprintf(w, "Queue observed: %s\nApp dispatch capacity: %d/%d (%s), tenant limit: %d\nWaiting now: %d, due: %d, stale: %d, oldest due: %.1fs\n",
		queue.ObservedAt.Format(time.RFC3339Nano), queue.AppRunningCount, queue.AppDispatchLimit, capacity, queue.TenantDispatchLimit,
		queue.WaitingRunCount, queue.DueRunCount, queue.StaleRunCount, queue.OldestDueAgeSeconds); err != nil {
		return err
	}
	var reasons []string
	for _, reason := range []string{api.AutomationQueueReady, api.AutomationQueueScheduled, api.AutomationQueueRetryBackoff, api.AutomationQueueParkedWait,
		api.AutomationQueueAppCapacity, api.AutomationQueueTenantCapacity, api.AutomationQueueWorkflowCapacity} {
		if count := queue.ReasonCounts[reason]; count > 0 {
			reasons = append(reasons, fmt.Sprintf("%s=%d", strings.ReplaceAll(reason, "_", " "), count))
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "none")
	}
	_, err := fmt.Fprintf(w, "Waiting reasons: %s\n", strings.Join(reasons, ", "))
	return err
}

func formatAutomationDuration(milliseconds *int64) string {
	if milliseconds == nil {
		return "n/a"
	}
	return fmt.Sprintf("%dms", *milliseconds)
}

func writeAutomationHealthStatuses(w io.Writer, counts map[string]int64) error {
	statuses := make([]string, 0, len(counts))
	for status := range counts {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, fmt.Sprintf("%s=%d", status, counts[status]))
	}
	if len(parts) == 0 {
		parts = append(parts, "none")
	}
	_, err := fmt.Fprintf(w, "Status counts: %s\n", strings.Join(parts, ", "))
	return err
}

func writeAutomationHealthRun(w io.Writer, label string, run *api.AutomationHealthRun) error {
	if run == nil {
		_, err := fmt.Fprintf(w, "%s: none\n", label)
		return err
	}
	_, err := fmt.Fprintf(w, "%s: %s (%s, %s)\n", label, run.ID, run.Status, run.CreatedAt.Format(time.RFC3339))
	return err
}

func writeAutomationHealthFailures(w io.Writer, failures []api.AutomationHealthStepFailure) error {
	if _, err := fmt.Fprintln(w, "Failed steps:"); err != nil {
		return err
	}
	if len(failures) == 0 {
		_, err := fmt.Fprintln(w, "  none")
		return err
	}
	writer := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "  STEP\tFAILED RUNS\tLAST FAILURE"); err != nil {
		return err
	}
	for _, failure := range failures {
		if _, err := fmt.Fprintf(writer, "  %s\t%d\t%s\n", failure.StepName, failure.FailedRunCount, failure.LastFailedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return writer.Flush()
}

type automationMutationSummary struct {
	FailurePaused    bool   `json:"failure_paused"`
	Name             string `json:"name"`
	Version          int64  `json:"version"`
	Source           string `json:"source"`
	PublishedVersion int64  `json:"published_version,omitempty"`
	Enabled          bool   `json:"enabled"`
}

type automationListSummary struct {
	AppSlug           string                      `json:"app_slug"`
	RuntimeEnabled    bool                        `json:"runtime_enabled"`
	UnavailableReason string                      `json:"unavailable_reason,omitempty"`
	MaxDefinitions    int                         `json:"max_definitions"`
	Automations       []automationMutationSummary `json:"automations"`
}

type automationGetSummary struct {
	automationMutationSummary
	DefinitionExportedTo string `json:"definition_exported_to,omitempty"`
}

func printAutomationList(result api.ListAutomationsResponse) int {
	summary := automationListSummary{
		AppSlug: result.AppSlug, RuntimeEnabled: result.RuntimeEnabled,
		UnavailableReason: result.UnavailableReason, MaxDefinitions: result.MaxDefinitions,
		Automations: make([]automationMutationSummary, 0, len(result.Automations)),
	}
	for _, automation := range result.Automations {
		summary.Automations = append(summary.Automations, summarizeAutomation(automation))
	}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	if !result.RuntimeEnabled || result.UnavailableReason != "" {
		reason := result.UnavailableReason
		if reason == "" {
			reason = "runtime is disabled"
		}
		if _, err := fmt.Fprintf(osStdout, "Automation runtime unavailable: %s\n", reason); err != nil {
			return printErr("Output failed", err)
		}
	}
	if len(summary.Automations) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No automations for app %s.\n", result.AppSlug)
		return 0
	}
	w := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tVERSION\tPUBLISHED\tSOURCE\tENABLED\tFAILURE PAUSED"); err != nil {
		return printErr("Output failed", err)
	}
	for _, automation := range summary.Automations {
		if _, err := fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%t\t%t\n", automation.Name, automation.Version, automation.PublishedVersion, automation.Source, automation.Enabled, automation.FailurePaused); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err := w.Flush(); err != nil {
		return printErr("Output failed", err)
	}
	return 0
}

func printAutomationDetails(result api.AutomationResponse, definitionOut string, published bool) int {
	path := ""
	if definitionOut != "" {
		definition := &result.Draft
		if published {
			if result.Published == nil {
				return printErr("Could not export automation definition", errors.New("automation has no published definition"))
			}
			definition = result.Published
		}
		if err := writeAutomationDefinitionFile(definitionOut, *definition); err != nil {
			return printErr("Could not export automation definition", err)
		}
		path = definitionOut
	}
	summary := automationGetSummary{automationMutationSummary: summarizeAutomation(result), DefinitionExportedTo: path}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Automation %q\n  Version: %d\n  Source: %s\n  Published version: %d\n  Enabled: %t\n  Failure paused: %t\n", result.Name, result.Version, result.Source, result.PublishedVersion, result.Enabled, result.FailurePaused)
	if path != "" {
		kind := "draft"
		if published {
			kind = "published"
		}
		_, _ = fmt.Fprintf(osStdout, "  Exported %s definition to %s\n", kind, path)
	}
	return 0
}

func summarizeAutomation(result api.AutomationResponse) automationMutationSummary {
	return automationMutationSummary{
		Name: result.Name, Version: result.Version, Source: result.Source,
		PublishedVersion: result.PublishedVersion, Enabled: result.Enabled, FailurePaused: result.FailurePaused,
	}
}

func writeAutomationDefinitionFile(path string, definition api.WorkflowSpec) error {
	data, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if int64(len(data)) > api.AutomationDefinitionMaxBytes {
		return fmt.Errorf("export exceeds the %d-byte automation definition limit", api.AutomationDefinitionMaxBytes)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if n, writeErr := file.Write(data); writeErr != nil || n != len(data) {
		_ = file.Close()
		_ = os.Remove(path)
		if writeErr != nil {
			return writeErr
		}
		return io.ErrShortWrite
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func readAutomationItemMockAttempts(path string) (map[string]map[string][]api.AutomationSimulationMockAttempt, error) {
	raw, err := readSimulationJSONFile(path, api.AutomationSimulationRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	var items map[string]map[string][]api.AutomationSimulationMockAttempt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&items); err != nil || items == nil {
		return nil, errors.New("file must contain an object of loop names and item indexes mapped to attempt arrays")
	}
	for _, indexes := range items {
		if len(indexes) == 0 || len(indexes) > api.WorkflowForEachMaxItems {
			return nil, errors.New("item attempt maps require 1..128 item indexes")
		}
		for value, attempts := range indexes {
			index, err := strconv.Atoi(value)
			if err != nil || index < 0 || index >= api.WorkflowForEachMaxItems || strconv.Itoa(index) != value {
				return nil, errors.New("item indexes must be canonical zero-based indexes below 128")
			}
			if len(attempts) == 0 || len(attempts) > api.WorkflowRetryMaxAttempts {
				return nil, errors.New("each item requires 1..25 attempt outcomes")
			}
			for _, attempt := range attempts {
				if int64(len(attempt.Output)) > api.WorkflowRunInputMaxBytes || int64(len(attempt.Error)) > api.WorkflowRunInputMaxBytes {
					return nil, errors.New("item attempt exceeds the workflow value byte limit")
				}
			}
		}
	}
	return items, nil
}

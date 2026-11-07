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

func cmdStoredWorkflows(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored <live|init|check|diff|list|get|health|history|report|runs|diagnose|recovery-preview|recover|recovery-plan|recovery-start|recovery-retry-plan|event-recovery-plan|event-recovery-start|event-recover|pause|resume|revisions|restore|delete|validate|simulate|apply|publish|publish-policy|failure-policy|failure-resume|recovery-health|recovery-cancel-preview|recovery-cancel|recovery-response-plan|recovery-response-preview|recovery-response-apply|recovery-response-status|recovery-policy|recovery-policy-evaluate|recovery-policy-reviews|recovery-suppress|failures|failure-recovery-plan|failure-recovery-review|failure-recovery-reviews|failure-recovery-evidence|recovery-retry-evidence>", "stored-workflows")
		return 1
	}
	switch args[0] {
	case "live":
		return cmdStoredWorkflowsLive(args[1:])
	case "recovery-policy-evaluate":
		return cmdStoredWorkflowsRecoveryPolicyEvaluate(args[1:])
	case "recovery-policy":
		return cmdStoredWorkflowsRecoveryPolicy(args[1:])
	case "recovery-suppress":
		return cmdStoredWorkflowsRecoverySuppress(args[1:])
	case "recovery-policy-reviews":
		return cmdStoredWorkflowsRecoveryPolicyReviews(args[1:])
	case "recovery-response-plan":
		return cmdStoredWorkflowsResponsePlan(args[1:])
	case "recovery-response-preview":
		return cmdStoredWorkflowsResponseExecute(args[1:], "preview")
	case "recovery-response-apply":
		return cmdStoredWorkflowsResponseExecute(args[1:], "apply")
	case "recovery-response-status":
		return cmdStoredWorkflowsResponseExecute(args[1:], "status")
	case "failures":
		return cmdStoredWorkflowsFailures(args[1:])
	case "failure-recovery-reviews":
		return cmdStoredWorkflowsFailureRecoveryReviews(args[1:])
	case "recovery-retry-evidence":
		return cmdStoredWorkflowsRetryEvidence(args[1:])
	case "failure-recovery-evidence":
		return cmdStoredWorkflowsInitialEvidence(args[1:])
	case "failure-recovery-review":
		return cmdStoredWorkflowsFailureRecoveryReview(args[1:])
	case "failure-recovery-plan":
		return cmdStoredWorkflowsFailureRecoveryPlan(args[1:])
	case "recovery-retry-plan":
		return cmdStoredWorkflowsRecoveryRetryPlan(args[1:])
	case "recovery-start":
		return cmdStoredWorkflowsRecoveryStart(args[1:])
	case "event-recovery-start":
		return cmdStoredWorkflowsEventRecoveryStart(args[1:])
	case "event-recovery-plan":
		return cmdStoredWorkflowsEventRecoveryPlan(args[1:])
	case "event-recover":
		return cmdStoredWorkflowsEventRecover(args[1:])
	case "recovery-plan":
		return cmdStoredWorkflowsRecoveryPlan(args[1:])
	case "recover":
		return cmdStoredWorkflowsRecover(args[1:])
	case "recovery-preview":
		return cmdStoredWorkflowsRecoveryPreview(args[1:])
	case "failure-policy":
		return cmdStoredWorkflowsFailurePolicy(args[1:])
	case "failure-resume":
		return cmdStoredWorkflowsFailureResume(args[1:])
	case "publish-policy":
		return cmdStoredWorkflowsPublishPolicy(args[1:])
	case "diff":
		return cmdStoredWorkflowsDiff(args[1:])
	case "check":
		return cmdStoredWorkflowsCheck(args[1:])
	case "init":
		return cmdStoredWorkflowsInit(args[1:])
	case "list":
		return cmdStoredWorkflowsList(args[1:])
	case "get":
		return cmdStoredWorkflowsGet(args[1:])
	case "runs":
		return cmdStoredWorkflowsRuns(args[1:])
	case "diagnose":
		return cmdStoredWorkflowsDiagnose(args[1:])
	case "recovery-health":
		return cmdStoredWorkflowsRecoveryHealth(args[1:])
	case "recovery-cancel-preview":
		return cmdStoredWorkflowsRecoveryCancellation(args[1:], true)
	case "recovery-cancel":
		return cmdStoredWorkflowsRecoveryCancellation(args[1:], false)
	case "health":
		return cmdStoredWorkflowsHealth(args[1:])
	case "report":
		return cmdStoredWorkflowsReport(args[1:])
	case "history":
		return cmdStoredWorkflowsHistory(args[1:])
	case "pause":
		return cmdStoredWorkflowsPause(args[1:])
	case "resume":
		return cmdStoredWorkflowsResume(args[1:])
	case "revisions":
		return cmdStoredWorkflowsRevisions(args[1:])
	case "restore":
		return cmdStoredWorkflowsRestore(args[1:])
	case "delete":
		return cmdStoredWorkflowsDelete(args[1:])
	case "validate":
		return cmdStoredWorkflowsValidate(args[1:])
	case "simulate":
		return cmdStoredWorkflowsSimulate(args[1:])
	case "apply":
		return cmdStoredWorkflowsApply(args[1:])
	case "publish":
		return cmdStoredWorkflowsPublish(args[1:])
	default:
		printCommandValidation(os.Stderr, "unknown stored workflows subcommand %q\n", args[0])
		return unknownSubcommandChoices(os.Stderr, "stored-workflows", nestedCliCommand("stored-workflows"), args[0])
	}
}

func cmdStoredWorkflowsPause(args []string) int {
	return cmdStoredWorkflowsSetEnabled(args, false)
}

func cmdStoredWorkflowsResume(args []string) int {
	return cmdStoredWorkflowsSetEnabled(args, true)
}

func cmdStoredWorkflowsSetEnabled(args []string, enabled bool) int {
	verb := "pause"
	action := "Paused"
	if enabled {
		verb, action = "resume", "Resumed"
	}
	fs := newFlagSet("workflow stored "+verb, flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	expectedVersion := fs.Int64("expected-version", 0, "current stored workflow version")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *expectedVersion <= 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored "+verb+" --app <slug> --name <name> --expected-version <n>", "stored-workflows")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.SetStoredWorkflowEnabled(context.Background(), *app, *name, api.SetStoredWorkflowEnabledRequest{ExpectedVersion: *expectedVersion, Enabled: &enabled})
	if err != nil {
		return printErr("Could not "+verb+" stored workflow", err)
	}
	return printStoredWorkflowEnabled(action, result)
}

func cmdStoredWorkflowsHealth(args []string) int {
	fs := newFlagSet("workflow stored health", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	stallAfter := fs.Int("stall-after-seconds", api.WorkflowRecoveryHealthDefaultStallSeconds, "app recovery worker evaluation threshold")
	createdAfter := fs.String("created-after", "", "inclusive RFC3339 window start")
	createdBefore := fs.String("created-before", "", "inclusive RFC3339 window end")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" {
		PrintUsage(os.Stderr, "usage: gregale workflow stored health --app <slug> [--stall-after-seconds N] | --app <slug> --name <name> [--created-after RFC3339] [--created-before RFC3339]", "stored-workflows")
		return 1
	}
	if strings.TrimSpace(*name) == "" {
		if *createdAfter != "" || *createdBefore != "" {
			return printErr("Invalid recovery health options", errors.New("time windows require --name"))
		}
		return printStoredWorkflowRecoveryHealth(*app, *stallAfter)
	}
	if *stallAfter != api.WorkflowRecoveryHealthDefaultStallSeconds {
		return printErr("Invalid health options", errors.New("stall threshold requires app-wide health without --name"))
	}
	options, err := parseStoredWorkflowHealthOptions(*createdAfter, *createdBefore, time.Now().UTC())
	if err != nil {
		return printErr("Invalid stored workflow health time range", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetStoredWorkflowHealth(context.Background(), *app, *name, options)
	if err != nil {
		return printErr("Could not get stored workflow health", err)
	}
	return printStoredWorkflowHealth(result)
}

func parseStoredWorkflowHealthOptions(createdAfter, createdBefore string, now time.Time) (api.StoredWorkflowHealthOptions, error) {
	options := api.StoredWorkflowHealthOptions{}
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
	windowStart := windowEnd.Add(-api.StoredWorkflowHealthDefaultRange)
	if options.CreatedAfter != nil {
		windowStart = *options.CreatedAfter
	}
	if windowEnd.After(now) || windowStart.After(windowEnd) || windowEnd.Sub(windowStart) > api.StoredWorkflowHealthMaxRange {
		return options, errors.New("window must be non-future, ordered, and no longer than 30 days")
	}
	return options, nil
}

func cmdStoredWorkflowsRevisions(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored revisions <list|show>", "stored-workflows")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdStoredWorkflowsRevisionsList(args[1:])
	case "show":
		return cmdStoredWorkflowsRevisionsShow(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown stored workflow revisions subcommand: %s", args[0]), "stored-workflows")
		return 1
	}
}

func cmdStoredWorkflowsRevisionsList(args []string) int {
	fs := newFlagSet("workflow stored revisions list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	limit := fs.Int("limit", 50, "page size (1..100)")
	offset := fs.Int("offset", 0, "number of revisions to skip")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *limit < 1 || *limit > 100 || *offset < 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored revisions list --app <slug> --name <name> [--limit 1..100] [--offset <n>]", "stored-workflows")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.ListStoredWorkflowRevisions(context.Background(), *app, *name, *limit, *offset)
	if err != nil {
		return printErr("Could not list stored workflow revisions", err)
	}
	return printStoredWorkflowRevisionList(result)
}

func cmdStoredWorkflowsRevisionsShow(args []string) int {
	fs := newFlagSet("workflow stored revisions show", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	revision := fs.Int64("revision", 0, "published revision number")
	definitionOut := fs.String("definition-out", "", "export this definition as JSON to a new file")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *revision <= 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored revisions show --app <slug> --name <name> --revision <n> [--definition-out <path>]", "stored-workflows")
		return 1
	}
	if *definitionOut != "" && strings.TrimSpace(*definitionOut) == "" {
		return printErr("Invalid definition output path", errors.New("path cannot be blank"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetStoredWorkflowRevision(context.Background(), *app, *name, *revision)
	if err != nil {
		return printErr("Could not get stored workflow revision", err)
	}
	return printStoredWorkflowRevisionDetails(result, *definitionOut)
}

func cmdStoredWorkflowsRestore(args []string) int {
	fs := newFlagSet("workflow stored restore", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	revision := fs.Int64("revision", 0, "published revision number to restore")
	expectedVersion := fs.Int64("expected-version", -1, "current stored workflow version (0 if deleted)")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *revision <= 0 || *expectedVersion < 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored restore --app <slug> --name <name> --revision <n> --expected-version <n>", "stored-workflows")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.RestoreStoredWorkflowRevision(context.Background(), *app, *name, *revision, api.RestoreStoredWorkflowRevisionRequest{ExpectedVersion: *expectedVersion})
	if err != nil {
		return printErr("Could not restore stored workflow revision", err)
	}
	return printStoredWorkflowRevisionRestore(*revision, result)
}

func cmdStoredWorkflowsDelete(args []string) int {
	fs := newFlagSet("workflow stored delete", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	expectedVersion := fs.Int64("expected-version", 0, "current stored workflow version")
	yes := fs.Bool("yes", false, "confirm stored workflow deletion")
	restoreManifest := fs.Bool("restore-manifest", false, "allow the current YAML definition to own this stored workflow again")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *expectedVersion <= 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored delete --app <slug> --name <name> --expected-version <n> --yes [--restore-manifest]", "stored-workflows")
		return 1
	}
	if !*yes {
		return printErr("Confirmation required", errors.New("stored workflow deletion requires --yes"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteStoredWorkflow(context.Background(), *app, *name, *expectedVersion, *restoreManifest); err != nil {
		return printErr("Could not delete stored workflow", err)
	}
	return printStoredWorkflowDelete(*name, *expectedVersion, *restoreManifest)
}

func cmdStoredWorkflowsSimulate(args []string) int {
	fs := newFlagSet("workflow stored simulate", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	file := fs.String("file", "", "stored workflow definition YAML or JSON file")
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
		PrintUsage(os.Stderr, "usage: gregale workflow stored simulate --app <slug> --file <path> [--input-file <path>] [--mock-outputs-file <path>] [--mock-item-outputs-file <path>] [--mock-attempts-file <path>] [--mock-item-attempts-file <path>] [--require-complete]", "stored-workflows")
		return 1
	}
	definition, ok := readStoredWorkflowDefinition(*file)
	if !ok {
		return 1
	}
	request, err := loadStoredWorkflowSimulation(definition, *inputFile, *mockOutputsFile, *mockItemOutputsFile, *mockAttemptsFile, *mockItemAttemptsFile)
	if err != nil {
		return printErr("Invalid stored workflow simulation input", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.SimulateStoredWorkflow(context.Background(), *app, request)
	if err != nil {
		return printErr("Stored workflow simulation failed", err)
	}
	return printStoredWorkflowSimulation(definition.Name, result, *requireComplete)
}

func cmdStoredWorkflowsList(args []string) int {
	fs := newFlagSet("workflow stored list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" {
		PrintUsage(os.Stderr, "usage: gregale workflow stored list --app <slug>", "stored-workflows")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.ListStoredWorkflows(context.Background(), *app)
	if err != nil {
		return printErr("Could not list stored workflows", err)
	}
	return printStoredWorkflowList(result)
}

func cmdStoredWorkflowsGet(args []string) int {
	fs := newFlagSet("workflow stored get", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	definitionOut := fs.String("definition-out", "", "export the selected definition as JSON to a new file")
	published := fs.Bool("published", false, "export the published definition instead of the draft")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || (*published && *definitionOut == "") {
		PrintUsage(os.Stderr, "usage: gregale workflow stored get --app <slug> --name <name> [--definition-out <path> [--published]]", "stored-workflows")
		return 1
	}
	if *definitionOut != "" && strings.TrimSpace(*definitionOut) == "" {
		return printErr("Invalid definition output path", errors.New("path cannot be blank"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetStoredWorkflow(context.Background(), *app, *name)
	if err != nil {
		return printErr("Could not get stored workflow", err)
	}
	return printStoredWorkflowDetails(result, *definitionOut, *published)
}

func cmdStoredWorkflowsValidate(args []string) int {
	fs := newFlagSet("workflow stored validate", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	file := fs.String("file", "", "stored workflow definition YAML or JSON file")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*file) == "" {
		PrintUsage(os.Stderr, "usage: gregale workflow stored validate --app <slug> --file <path>", "stored-workflows")
		return 1
	}
	definition, ok := readStoredWorkflowDefinition(*file)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.ValidateStoredWorkflow(context.Background(), *app, api.ValidateStoredWorkflowRequest{Definition: definition})
	if err != nil {
		return printErr("Stored workflow validation failed", err)
	}
	return printStoredWorkflowValidation(definition.Name, result)
}

func cmdStoredWorkflowsApply(args []string) int {
	fs := newFlagSet("workflow stored apply", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	file := fs.String("file", "", "stored workflow definition YAML or JSON file")
	expectedVersion := fs.Int64("expected-version", -1, "current stored workflow version (0 for a new draft)")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*file) == "" || *expectedVersion < 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored apply --app <slug> --file <path> --expected-version <n>", "stored-workflows")
		return 1
	}
	definition, ok := readStoredWorkflowDefinition(*file)
	if !ok {
		return 1
	}
	if strings.TrimSpace(definition.Name) == "" {
		return printErr("Invalid stored workflow definition", errors.New("definition name is required"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.SaveStoredWorkflowDraft(context.Background(), *app, definition.Name, api.SaveStoredWorkflowDraftRequest{ExpectedVersion: *expectedVersion, Definition: definition})
	if err != nil {
		return printErr("Could not save stored workflow draft", err)
	}
	return printStoredWorkflowMutation("Saved draft", result)
}

func cmdStoredWorkflowsPublish(args []string) int {
	fs := newFlagSet("workflow stored publish", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "stored workflow name")
	expectedVersion := fs.Int64("expected-version", -1, "current stored workflow version")
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
		return printErr("Invalid stored workflow scenarios", errors.New("--scenarios requires a nonempty suite path"))
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *expectedVersion < 0 {
		PrintUsage(os.Stderr, "usage: gregale workflow stored publish --app <slug> --name <name> --expected-version <n> [--take-over-manifest] [--scenarios <path>] [--require-coverage]", "stored-workflows")
		return 1
	}
	var scenarios []loadedStoredWorkflowScenario
	if *scenarioPath != "" {
		var err error
		scenarios, err = loadStoredWorkflowScenarios(*scenarioPath)
		if err != nil {
			return printErr("Invalid stored workflow scenarios", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *scenarioPath != "" {
		return publishStoredWorkflowWithScenarios(client, *app, *name, *expectedVersion, *takeOverManifest, scenarios, *requireCoverage)
	}
	result, err := client.PublishStoredWorkflow(context.Background(), *app, *name, api.PublishStoredWorkflowRequest{ExpectedVersion: *expectedVersion, TakeOverManifest: *takeOverManifest})
	if err != nil {
		return printErr("Could not publish stored workflow", err)
	}
	return printStoredWorkflowMutation("Published", result)
}

func readStoredWorkflowDefinition(path string) (api.WorkflowSpec, bool) {
	file, err := openCustomerFile(path)
	if err != nil {
		printErr("Could not read stored workflow definition", err)
		return api.WorkflowSpec{}, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, api.StoredWorkflowDefinitionMaxBytes+1))
	if err != nil {
		printErr("Could not read stored workflow definition", err)
		return api.WorkflowSpec{}, false
	}
	if int64(len(data)) > api.StoredWorkflowDefinitionMaxBytes {
		printErr("Invalid stored workflow definition", fmt.Errorf("file exceeds the %d-byte stored workflow definition limit", api.StoredWorkflowDefinitionMaxBytes))
		return api.WorkflowSpec{}, false
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var definition api.WorkflowSpec
	if err := decoder.Decode(&definition); err != nil {
		printErr("Invalid stored workflow definition", err)
		return api.WorkflowSpec{}, false
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("definition must contain exactly one YAML document")
		}
		printErr("Invalid stored workflow definition", err)
		return api.WorkflowSpec{}, false
	}
	return definition, true
}

func loadStoredWorkflowSimulation(definition api.WorkflowSpec, inputPath, mockPath, itemMockPath, attemptMockPath string, itemAttemptPaths ...string) (api.SimulateStoredWorkflowRequest, error) {
	request := api.SimulateStoredWorkflowRequest{Definition: definition}
	var err error
	if inputPath != "" {
		request.Input, err = readSimulationJSONFile(inputPath, api.WorkflowRunInputMaxBytes)
		if err != nil {
			return request, fmt.Errorf("input file: %w", err)
		}
	}
	if mockPath != "" {
		request.MockOutputs, err = readStoredWorkflowMockOutputs(mockPath)
		if err != nil {
			return request, fmt.Errorf("mock outputs file: %w", err)
		}
	}
	if itemMockPath != "" {
		request.MockItemOutputs, err = readStoredWorkflowItemMockOutputs(itemMockPath)
		if err != nil {
			return request, fmt.Errorf("item mock outputs file: %w", err)
		}
	}
	if attemptMockPath != "" {
		request.MockAttempts, err = readStoredWorkflowMockAttempts(attemptMockPath)
		if err != nil {
			return request, fmt.Errorf("mock attempts file: %w", err)
		}
	}
	if len(itemAttemptPaths) > 0 && itemAttemptPaths[0] != "" {
		request.MockItemAttempts, err = readStoredWorkflowItemMockAttempts(itemAttemptPaths[0])
		if err != nil {
			return request, fmt.Errorf("item mock attempts file: %w", err)
		}
	}
	definitionJSON, err := json.Marshal(request.Definition)
	if err != nil {
		return request, fmt.Errorf("definition is not valid JSON: %w", err)
	}
	if int64(len(definitionJSON)) > api.StoredWorkflowDefinitionMaxBytes {
		return request, fmt.Errorf("definition exceeds the %d-byte limit", api.StoredWorkflowDefinitionMaxBytes)
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return request, fmt.Errorf("request is not valid JSON: %w", err)
	}
	if int64(len(requestJSON)) > api.StoredWorkflowSimulationRequestMaxBytes {
		return request, fmt.Errorf("simulation request exceeds the %d-byte limit", api.StoredWorkflowSimulationRequestMaxBytes)
	}
	return request, nil
}

func readStoredWorkflowMockAttempts(path string) (map[string][]api.StoredWorkflowSimulationMockAttempt, error) {
	raw, err := readSimulationJSONFile(path, api.StoredWorkflowSimulationRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	var attempts map[string][]api.StoredWorkflowSimulationMockAttempt
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

func readStoredWorkflowMockOutputs(path string) (map[string]json.RawMessage, error) {
	raw, err := readSimulationJSONFile(path, api.StoredWorkflowSimulationRequestMaxBytes)
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

func readStoredWorkflowItemMockOutputs(path string) (map[string][]json.RawMessage, error) {
	raw, err := readSimulationJSONFile(path, api.StoredWorkflowSimulationRequestMaxBytes)
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

func printStoredWorkflowValidation(name string, result api.ValidateStoredWorkflowResponse) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else if result.Valid {
		_, _ = fmt.Fprintf(osStdout, "Stored workflow %q is valid.\n", name)
		if len(result.StepOrder) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Step order: %s\n", strings.Join(result.StepOrder, " -> "))
		}
		if result.NextFireAt != "" {
			_, _ = fmt.Fprintf(osStdout, "Next scheduled run: %s\n", result.NextFireAt)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Stored workflow %q is invalid.\n", name)
		for _, issue := range result.Issues {
			_, _ = fmt.Fprintf(osStdout, "- %s\n", issue)
		}
	}
	if result.Valid {
		return 0
	}
	return 1
}

func printStoredWorkflowSimulation(name string, result api.SimulateStoredWorkflowResponse, requireComplete bool) int {
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
		_, _ = fmt.Fprintf(osStdout, "Stored workflow %q simulation: %s\n", name, status)
		if len(result.StepOrder) > 0 {
			_, _ = fmt.Fprintf(osStdout, "Step order: %s\n", strings.Join(result.StepOrder, " -> "))
		}
		for _, issue := range result.Issues {
			_, _ = fmt.Fprintf(osStdout, "Issue: %s\n", issue)
		}
		for _, warning := range result.Warnings {
			_, _ = fmt.Fprintf(osStdout, "Warning: %s\n", warning)
		}
		if err := writeStoredWorkflowSimulationTrace(osStdout, result.Trace); err != nil {
			return printErr("Could not render simulation trace", err)
		}
	}
	if !result.DefinitionValid || len(result.Issues) > 0 || (requireComplete && !result.Complete) {
		return 1
	}
	return 0
}

func writeStoredWorkflowSimulationTrace(w io.Writer, trace []api.StoredWorkflowSimulationStep) error {
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
		action := storedWorkflowSimulationAction(step)
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, step.Kind, step.State, reason, action, compactSimulationValue(step.Input), compactSimulationValue(step.Output)); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func storedWorkflowSimulationAction(step api.StoredWorkflowSimulationStep) string {
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

func printStoredWorkflowMutation(action string, result api.StoredWorkflowResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(summarizeStoredWorkflow(result)))
	}
	_, _ = fmt.Fprintf(osStdout, "%s stored workflow %q at version %d.\n", action, result.Name, result.Version)
	return 0
}

func printStoredWorkflowEnabled(action string, result api.StoredWorkflowResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(summarizeStoredWorkflow(result)))
	}
	_, _ = fmt.Fprintf(osStdout, "%s automatic starts for stored workflow %q at version %d.\n", action, result.Name, result.Version)
	if result.FailurePaused {
		_, _ = fmt.Fprintln(osStdout, "Failure pause remains active. Inspect failure-policy and use failure-resume to clear it.")
	}
	return 0
}

type storedWorkflowDeleteSummary struct {
	Name                     string `json:"name"`
	DeletedVersion           int64  `json:"deleted_version"`
	RestoreManifestRequested bool   `json:"restore_manifest_requested"`
}

func printStoredWorkflowDelete(name string, version int64, restoreManifest bool) int {
	summary := storedWorkflowDeleteSummary{Name: name, DeletedVersion: version, RestoreManifestRequested: restoreManifest}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted stored workflow %q at version %d.\n", name, version)
	if restoreManifest {
		_, _ = fmt.Fprintln(osStdout, "The current YAML definition may own this stored workflow again if one is present.")
	}
	return 0
}

type storedWorkflowRevisionSummary struct {
	Version        int64     `json:"version"`
	DefinitionHash string    `json:"definition_hash"`
	RecordedAt     time.Time `json:"recorded_at"`
	LegacySnapshot bool      `json:"legacy_snapshot"`
}

type storedWorkflowRevisionListSummary struct {
	Total     int                             `json:"total"`
	Limit     int                             `json:"limit"`
	Offset    int                             `json:"offset"`
	Revisions []storedWorkflowRevisionSummary `json:"revisions"`
}

type storedWorkflowRevisionGetSummary struct {
	CheckEvidence *api.StoredWorkflowCheckEvidence `json:"check_evidence,omitempty"`
	storedWorkflowRevisionSummary
	DefinitionExportedTo string `json:"definition_exported_to,omitempty"`
}

type storedWorkflowRevisionRestoreSummary struct {
	storedWorkflowMutationSummary
	RestoredFromRevision int64 `json:"restored_from_revision"`
}

func printStoredWorkflowRevisionList(result api.ListStoredWorkflowRevisionsResponse) int {
	summary := storedWorkflowRevisionListSummary{
		Total: result.Total, Limit: result.Limit, Offset: result.Offset,
		Revisions: make([]storedWorkflowRevisionSummary, 0, len(result.Revisions)),
	}
	for _, revision := range result.Revisions {
		summary.Revisions = append(summary.Revisions, summarizeStoredWorkflowRevision(revision))
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

func printStoredWorkflowRevisionDetails(result api.StoredWorkflowRevisionResponse, definitionOut string) int {
	path := ""
	if definitionOut != "" {
		if err := writeStoredWorkflowDefinitionFile(definitionOut, result.Definition); err != nil {
			return printErr("Could not export stored workflow revision definition", err)
		}
		path = definitionOut
	}
	summary := storedWorkflowRevisionGetSummary{storedWorkflowRevisionSummary: summarizeStoredWorkflowRevision(result), DefinitionExportedTo: path, CheckEvidence: result.CheckEvidence}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Stored workflow revision %d\n  Recorded at: %s\n  Definition hash: %s\n  Legacy snapshot: %t\n", result.Version, result.RecordedAt.Format(time.RFC3339), result.DefinitionHash, result.LegacySnapshot)
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

func printStoredWorkflowRevisionRestore(revision int64, result api.StoredWorkflowResponse) int {
	summary := storedWorkflowRevisionRestoreSummary{storedWorkflowMutationSummary: summarizeStoredWorkflow(result), RestoredFromRevision: revision}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Restored revision %d as draft stored workflow %q at version %d.\n", revision, result.Name, result.Version)
	return 0
}

func summarizeStoredWorkflowRevision(result api.StoredWorkflowRevisionResponse) storedWorkflowRevisionSummary {
	return storedWorkflowRevisionSummary{Version: result.Version, DefinitionHash: result.DefinitionHash, RecordedAt: result.RecordedAt, LegacySnapshot: result.LegacySnapshot}
}

func printStoredWorkflowHealth(result api.StoredWorkflowHealthResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Stored workflow health: %s/%s\nWindow: %s to %s\nRuns: %d (%d completed)\n", result.AppSlug, result.StoredWorkflowName, result.WindowStart.Format(time.RFC3339), result.WindowEnd.Format(time.RFC3339), result.RunCount, result.CompletedRunCount)
	_, _ = fmt.Fprintf(osStdout, "Active now: %d, queued: %d\n", result.ActiveRunCount, result.QueuedRunCount)
	if err := writeStoredWorkflowQueueHealth(osStdout, result.Queue); err != nil {
		return printErr("Output failed", err)
	}
	if result.CompletedRunCount == 0 {
		_, _ = fmt.Fprintln(osStdout, "Success rate: n/a (no completed runs)")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Success rate: %.1f%%\n", result.SuccessRate*100)
	}
	_, _ = fmt.Fprintf(osStdout, "Durations: p50 %s, p95 %s\n", formatStoredWorkflowDuration(result.P50DurationMS), formatStoredWorkflowDuration(result.P95DurationMS))
	if err := writeStoredWorkflowHealthStatuses(osStdout, result.StatusCounts); err != nil {
		return printErr("Output failed", err)
	}
	for _, latest := range []struct {
		label string
		run   *api.StoredWorkflowHealthRun
	}{{"Last run", result.LastRun}, {"Last success", result.LastSuccess}, {"Last failure", result.LastFailure}} {
		if err := writeStoredWorkflowHealthRun(osStdout, latest.label, latest.run); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err := writeStoredWorkflowHealthFailures(osStdout, result.FailedSteps); err != nil {
		return printErr("Output failed", err)
	}
	if err := writeStoredWorkflowHealthRunHints(osStdout, result); err != nil {
		return printErr("Output failed", err)
	}
	return 0
}

func writeStoredWorkflowHealthRunHints(w io.Writer, health api.StoredWorkflowHealthResponse) error {
	if !validStoredWorkflowRunSelection(health.AppSlug, health.StoredWorkflowName) || health.WindowStart.IsZero() || health.WindowEnd.IsZero() || health.WindowStart.After(health.WindowEnd) {
		return nil
	}
	app, name := storedWorkflowCommandArgument(health.AppSlug), storedWorkflowCommandArgument(health.StoredWorkflowName)
	base := fmt.Sprintf("gregale workflow stored runs --app %s --name %s --created-after %s --created-before %s", app, name, health.WindowStart.UTC().Format(time.RFC3339Nano), health.WindowEnd.UTC().Format(time.RFC3339Nano))
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
		_, err := fmt.Fprintf(w, "Diagnose last failure: gregale workflow stored diagnose --app %s --name %s --run %s\n", app, name, health.LastFailure.ID)
		return err
	}
	return nil
}

// Quote user-visible command arguments for POSIX shells without interpreting them.
func storedWorkflowCommandArgument(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.'
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeStoredWorkflowQueueHealth(w io.Writer, queue *api.StoredWorkflowQueueHealth) error {
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
	for _, reason := range []string{api.StoredWorkflowQueueReady, api.StoredWorkflowQueueScheduled, api.StoredWorkflowQueueRetryBackoff, api.StoredWorkflowQueueParkedWait,
		api.StoredWorkflowQueueAppCapacity, api.StoredWorkflowQueueTenantCapacity, api.StoredWorkflowQueueWorkflowCapacity} {
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

func formatStoredWorkflowDuration(milliseconds *int64) string {
	if milliseconds == nil {
		return "n/a"
	}
	return fmt.Sprintf("%dms", *milliseconds)
}

func writeStoredWorkflowHealthStatuses(w io.Writer, counts map[string]int64) error {
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

func writeStoredWorkflowHealthRun(w io.Writer, label string, run *api.StoredWorkflowHealthRun) error {
	if run == nil {
		_, err := fmt.Fprintf(w, "%s: none\n", label)
		return err
	}
	_, err := fmt.Fprintf(w, "%s: %s (%s, %s)\n", label, run.ID, run.Status, run.CreatedAt.Format(time.RFC3339))
	return err
}

func writeStoredWorkflowHealthFailures(w io.Writer, failures []api.StoredWorkflowHealthStepFailure) error {
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

type storedWorkflowMutationSummary struct {
	FailurePaused    bool   `json:"failure_paused"`
	Name             string `json:"name"`
	Version          int64  `json:"version"`
	Source           string `json:"source"`
	PublishedVersion int64  `json:"published_version,omitempty"`
	Enabled          bool   `json:"enabled"`
}

type storedWorkflowListSummary struct {
	AppSlug           string                          `json:"app_slug"`
	RuntimeEnabled    bool                            `json:"runtime_enabled"`
	UnavailableReason string                          `json:"unavailable_reason,omitempty"`
	MaxDefinitions    int                             `json:"max_definitions"`
	StoredWorkflows   []storedWorkflowMutationSummary `json:"stored_workflows"`
}

type storedWorkflowGetSummary struct {
	storedWorkflowMutationSummary
	DefinitionExportedTo string `json:"definition_exported_to,omitempty"`
}

func printStoredWorkflowList(result api.ListStoredWorkflowsResponse) int {
	summary := storedWorkflowListSummary{
		AppSlug: result.AppSlug, RuntimeEnabled: result.RuntimeEnabled,
		UnavailableReason: result.UnavailableReason, MaxDefinitions: result.MaxDefinitions,
		StoredWorkflows: make([]storedWorkflowMutationSummary, 0, len(result.StoredWorkflows)),
	}
	for _, storedWorkflow := range result.StoredWorkflows {
		summary.StoredWorkflows = append(summary.StoredWorkflows, summarizeStoredWorkflow(storedWorkflow))
	}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	if !result.RuntimeEnabled || result.UnavailableReason != "" {
		reason := result.UnavailableReason
		if reason == "" {
			reason = "runtime is disabled"
		}
		if _, err := fmt.Fprintf(osStdout, "Stored workflow runtime unavailable: %s\n", reason); err != nil {
			return printErr("Output failed", err)
		}
	}
	if len(summary.StoredWorkflows) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No stored workflows for app %s.\n", result.AppSlug)
		return 0
	}
	w := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tVERSION\tPUBLISHED\tSOURCE\tENABLED\tFAILURE PAUSED"); err != nil {
		return printErr("Output failed", err)
	}
	for _, storedWorkflow := range summary.StoredWorkflows {
		if _, err := fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%t\t%t\n", storedWorkflow.Name, storedWorkflow.Version, storedWorkflow.PublishedVersion, storedWorkflow.Source, storedWorkflow.Enabled, storedWorkflow.FailurePaused); err != nil {
			return printErr("Output failed", err)
		}
	}
	if err := w.Flush(); err != nil {
		return printErr("Output failed", err)
	}
	return 0
}

func printStoredWorkflowDetails(result api.StoredWorkflowResponse, definitionOut string, published bool) int {
	path := ""
	if definitionOut != "" {
		definition := &result.Draft
		if published {
			if result.Published == nil {
				return printErr("Could not export stored workflow definition", errors.New("stored workflow has no published definition"))
			}
			definition = result.Published
		}
		if err := writeStoredWorkflowDefinitionFile(definitionOut, *definition); err != nil {
			return printErr("Could not export stored workflow definition", err)
		}
		path = definitionOut
	}
	summary := storedWorkflowGetSummary{storedWorkflowMutationSummary: summarizeStoredWorkflow(result), DefinitionExportedTo: path}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	_, _ = fmt.Fprintf(osStdout, "Stored workflow %q\n  Version: %d\n  Source: %s\n  Published version: %d\n  Enabled: %t\n  Failure paused: %t\n", result.Name, result.Version, result.Source, result.PublishedVersion, result.Enabled, result.FailurePaused)
	if path != "" {
		kind := "draft"
		if published {
			kind = "published"
		}
		_, _ = fmt.Fprintf(osStdout, "  Exported %s definition to %s\n", kind, path)
	}
	return 0
}

func summarizeStoredWorkflow(result api.StoredWorkflowResponse) storedWorkflowMutationSummary {
	return storedWorkflowMutationSummary{
		Name: result.Name, Version: result.Version, Source: result.Source,
		PublishedVersion: result.PublishedVersion, Enabled: result.Enabled, FailurePaused: result.FailurePaused,
	}
}

func writeStoredWorkflowDefinitionFile(path string, definition api.WorkflowSpec) error {
	data, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if int64(len(data)) > api.StoredWorkflowDefinitionMaxBytes {
		return fmt.Errorf("export exceeds the %d-byte stored workflow definition limit", api.StoredWorkflowDefinitionMaxBytes)
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

func readStoredWorkflowItemMockAttempts(path string) (map[string]map[string][]api.StoredWorkflowSimulationMockAttempt, error) {
	raw, err := readSimulationJSONFile(path, api.StoredWorkflowSimulationRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	var items map[string]map[string][]api.StoredWorkflowSimulationMockAttempt
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

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
	"os/signal"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executioncontract"
	"github.com/onebox-faas/faas/pkg/runtask"
)

func readRunTaskFile(path string, limit int64) ([]byte, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return body, nil
}

func loadRunTaskManifest(path string) (api.PublishExecutionTaskRequest, error) {
	var task api.PublishExecutionTaskRequest
	if path == "" {
		return task, errors.New("use --manifest task.json")
	}
	body, err := readRunTaskFile(path, api.ExecutionTaskPublishMaxBytes)
	if err != nil {
		return task, err
	}
	if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || trimmed[0] != '{' {
		return task, errors.New("manifest must contain exactly one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&task); err != nil {
		return task, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return task, errors.New("manifest must contain exactly one JSON object")
	}
	return task, nil
}

func readRunTaskFixture(value string) (json.RawMessage, error) {
	var body []byte
	switch {
	case value == "-":
		return nil, errors.New("use inline JSON or @PATH for a fixture")
	case strings.HasPrefix(value, "@"):
		var err error
		body, err = readRunTaskFile(value[1:], api.ExecutionSealedPayloadMaxBytes)
		if err != nil {
			return nil, err
		}
	default:
		body = []byte(value)
	}
	if !json.Valid(body) {
		return nil, errors.New("fixture must contain one complete JSON value")
	}
	return body, nil
}

func cmdRunTaskLocal(verb string, args []string) int {
	fs := newFlagSet("run tasks "+verb, flag.ContinueOnError)
	manifest := fs.String("manifest", "", "publication JSON file")
	plan := fs.String("plan", string(api.PlanPro), "plan to use for local validation")
	var fixture, language, check *string
	if verb == "validate" {
		fixture = fs.String("input", "", "fixture as JSON or @file")
	} else {
		language = fs.String("language", "", "typescript or python")
		check = fs.String("check", "", "compare generated output with this file")
	}
	flags, positional := splitArgsForFlags(args)
	if fs.Parse(flags) != nil {
		return 1
	}
	if len(positional) != 0 {
		return printErr("Invalid task arguments", errors.New("use --manifest to select the draft"))
	}
	task, err := loadRunTaskManifest(*manifest)
	if err != nil {
		return printErr("Invalid task manifest", err)
	}
	normalized, metadata, err := runtask.Normalize(task, api.Plan(*plan))
	if err != nil {
		return printErr("Invalid task manifest", err)
	}
	if verb == "generate" {
		return outputRunTaskBindings(normalized, *language, *check)
	}
	fixtureProvided := flagWasSet(fs, "input")
	if fixtureProvided {
		input, err := readRunTaskFixture(*fixture)
		if err != nil {
			return printErr("Invalid fixture", err)
		}
		request := normalized.Definition
		request.Input = input
		if err := checkRunTaskFixture(request, api.Plan(*plan)); err != nil {
			return printErr("Invalid fixture", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"name": metadata.Name, "version": metadata.Version, "plan": *plan,
			"definition_digest": metadata.Digest, "fixture_validated": fixtureProvided,
		}))
	}
	PrintOK(osStdout, "%s@v%d is valid for the %s plan.", metadata.Name, metadata.Version, *plan)
	return 0
}

func outputRunTaskBindings(task api.PublishExecutionTaskRequest, language, check string) int {
	generated, err := runtask.GenerateBindings(task, language)
	if err != nil {
		return printErr("Could not generate task bindings", err)
	}
	if check != "" {
		existing, err := readRunTaskFile(check, int64(len(generated)))
		if err != nil || !bytes.Equal(existing, generated) {
			return printErr("Task bindings are out of date", fmt.Errorf("regenerate %s", check))
		}
		return 0
	}
	if _, err := osStdout.Write(generated); err != nil {
		return printErr("Could not write task bindings", err)
	}
	return 0
}

func checkRunTaskFixture(request api.CreateExecutionRequest, plan api.Plan) error {
	if _, problem := request.Resolve(plan); problem != nil {
		return problem
	}
	return executioncontract.CheckRequest(request)
}

func cmdRunTaskTest(args []string) int {
	fs := newFlagSet("run tasks test", flag.ContinueOnError)
	manifest := fs.String("manifest", "", "publication JSON file")
	input := fs.String("input", "null", "fixture as JSON or @file")
	key := fs.String("idempotency-key", "", "explicit stable key for this smoke test")
	customer := fs.String("customer-ref", "", "customer to attribute the test Run to")
	timeout := fs.Duration("wait-timeout", executionWaitTimeoutDefault, "maximum time to submit and wait")
	output := fs.String("output-dir", "", "save successful output artifacts")
	var inputFiles executionInputFileFlags
	fs.Var(&inputFiles, "input-file", "RELATIVE_INPUT_PATH=LOCAL_FILE (repeatable)")
	flags, positional := splitArgsForFlags(args)
	if fs.Parse(flags) != nil {
		return 1
	}
	if len(positional) != 0 {
		return printErr("Invalid task arguments", errors.New("use --manifest to select the draft"))
	}
	if *key == "" || strings.TrimSpace(*key) != *key {
		return printErr("Invalid idempotency key", errors.New("provide an explicit --idempotency-key without surrounding whitespace"))
	}
	if err := validateDeployIdempotencyKey(*key); err != nil {
		return printErr("Invalid idempotency key", err)
	}
	for _, character := range *key {
		if character > '~' {
			return printErr("Invalid idempotency key", errors.New("use printable ASCII characters only"))
		}
	}
	if *timeout <= 0 {
		return printErr("Invalid timeout", errors.New("wait-timeout must be positive"))
	}
	task, err := loadRunTaskManifest(*manifest)
	if err != nil {
		return printErr("Invalid task manifest", err)
	}
	fixture, err := readRunTaskFixture(*input)
	if err != nil {
		return printErr("Invalid fixture", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	request, err := prepareRunTaskTest(ctx, client, task, fixture, *customer, inputFiles)
	if err != nil {
		return printErr("Task test preflight failed", err)
	}
	return executeRunTaskTest(ctx, client, request, *key, *output, inputFiles)
}

func prepareRunTaskTest(ctx context.Context, client *api.Client, task api.PublishExecutionTaskRequest, fixture json.RawMessage, customer string, files []string) (api.CreateExecutionRequest, error) {
	capabilities, err := client.GetExecutionCapabilities(ctx)
	if err != nil {
		return api.CreateExecutionRequest{}, fmt.Errorf("read Run capabilities: %w", err)
	}
	plan := api.Plan(capabilities.Plan)
	if !plan.Valid() {
		return api.CreateExecutionRequest{}, errors.New("server did not report a recognized Run plan")
	}
	normalized, _, err := runtask.Normalize(task, plan)
	if err != nil {
		return api.CreateExecutionRequest{}, err
	}
	request := normalized.Definition
	request.Input = fixture
	request.CustomerRef = customer
	if err := checkRunTaskFixture(request, plan); err != nil {
		return api.CreateExecutionRequest{}, err
	}
	for i, value := range files {
		path, _, _ := strings.Cut(value, "=")
		for _, prior := range files[:i] {
			other, _, _ := strings.Cut(prior, "=")
			if path == other || strings.HasPrefix(path, other+"/") || strings.HasPrefix(other, path+"/") {
				return api.CreateExecutionRequest{}, errors.New("input file paths must be unique and must not overlap")
			}
		}
	}
	return request, nil
}

func executeRunTaskTest(ctx context.Context, client *api.Client, request api.CreateExecutionRequest, key, output string, files []string) int {
	inputs, err := prepareExecutionLocalInputs(ctx, client, files)
	if err != nil {
		return printErr("Could not prepare input files", err)
	}
	defer closeExecutionLocalInputs(inputs)
	receipt, err := submitExecutionWithLocalFiles(api.ContextWithIdempotencyKey(ctx, key), client, request, inputs)
	if err != nil {
		return printRunSubmissionError("Task test submission failed", err, recoveryCLI()+" sandbox list", true)
	}
	if receipt.ID == "" {
		return printErr("Task test returned no Run ID", errors.New("replay the same test with its original --idempotency-key"))
	}
	result, err := waitExecutionWorkflowRun(ctx, client, receipt, executionPollIntervalDefault)
	if err != nil {
		return printErr("Task test wait failed", fmt.Errorf("run %s: %w", receipt.ID, err))
	}
	if result.ID != receipt.ID {
		return printErr("Task test returned a different Run ID", fmt.Errorf("expected Run %s", receipt.ID))
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	}
	if err := checkRunTaskTestResult(result, request); err != nil {
		return printErr("Task test failed", fmt.Errorf("run %s: %w", result.ID, err))
	}
	if err := saveExecutionArtifactsWithClient(ctx, client, result, output); err != nil {
		return printErr("Artifact download failed", err)
	}
	if !jsonOutput {
		PrintOK(osStdout, "Task test passed (Run %s).", result.ID)
	}
	return 0
}

func checkRunTaskTestResult(result api.ExecutionResponse, request api.CreateExecutionRequest) error {
	if result.Status != api.ExecutionStatusSucceeded {
		return fmt.Errorf("execution ended with status %s", result.Status)
	}
	if result.ContractFailed() || len(request.ResultSchema) > 0 &&
		(result.ResultValidation == nil || result.ResultValidation.Status != "valid") {
		return errors.New("receipt does not confirm a valid result contract")
	}
	artifacts := make(map[string]bool, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		artifacts[artifact.Name] = true
	}
	for _, name := range request.OutputFiles {
		if !artifacts[name] {
			return fmt.Errorf("declared output %q is missing", name)
		}
	}
	return nil
}

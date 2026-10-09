package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

type automationScenarioSuite struct {
	CoverageExclusions []automationCoverageExclusion `yaml:"coverage_exclusions"`
	Definition         string                        `yaml:"definition"`
	Scenarios          []automationScenario          `yaml:"scenarios"`
}

type automationScenario struct {
	Name                 string                                          `yaml:"name"`
	InputFile            string                                          `yaml:"input_file"`
	MockOutputsFile      string                                          `yaml:"mock_outputs_file"`
	MockItemAttemptsFile string                                          `yaml:"mock_item_attempts_file"`
	MockItemOutputsFile  string                                          `yaml:"mock_item_outputs_file"`
	MockAttemptsFile     string                                          `yaml:"mock_attempts_file"`
	RequireComplete      *bool                                           `yaml:"require_complete"`
	ExpectItems          map[string]map[string]automationStepExpectation `yaml:"expect_items"`
	Expect               map[string]automationStepExpectation            `yaml:"expect"`
}

// A YAML node preserves the distinction between omitted output and explicit null.
type automationStepExpectation struct {
	AttemptCount *int                            `yaml:"attempt_count"`
	Attempts     *[]automationAttemptExpectation `yaml:"attempts"`
	State        string                          `yaml:"state"`
	Reason       *string                         `yaml:"reason"`
	WhenMatched  *bool                           `yaml:"when_matched"`
	Output       yaml.Node                       `yaml:"output"`
}

type loadedAutomationScenario struct {
	coverageExclusions []automationCoverageExclusion
	definitionPath     string
	scenario           automationScenario
	request            api.SimulateAutomationRequest
	itemOutputs        map[automationItemExpectationKey]json.RawMessage
	outputs            map[string]json.RawMessage
}

type automationScenarioResult struct {
	Name            string   `json:"name"`
	Passed          bool     `json:"passed"`
	DefinitionValid bool     `json:"definition_valid"`
	Complete        bool     `json:"complete"`
	Failures        []string `json:"failures"`
}

type automationCheckReport struct {
	Coverage      *automationCoverageGate      `json:"coverage,omitempty"`
	Suggestions   *automationSuggestionSummary `json:"suggestions,omitempty"`
	CoverageHints []automationCoverageHint     `json:"coverage_hints"`
	Passed        bool                         `json:"passed"`
	Scenarios     []automationScenarioResult   `json:"scenarios"`
}

func cmdAutomationsCheck(args []string) int {
	fs := newFlagSet("automations check", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	path := fs.String("scenarios", "", "scenario suite YAML or JSON file")
	requireCoverage := fs.Bool("require-coverage", false, "fail if coverage hints remain after reasoned exclusions")
	suggest := fs.Bool("suggest", false, "write coverage scenario starters to a new file")
	suggestOut := fs.String("suggest-out", "", "new suggestion file (defaults to scenario-suggestions.yaml beside the suite)")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*path) == "" {
		return printErr("Missing scenario options", errors.New("usage: gregale automations check --app <slug> --scenarios <path>"))
	}
	scenarios, err := loadAutomationScenarios(*path)
	if err != nil {
		return printErr("Invalid automation scenarios", err)
	}
	suggestionPath := ""
	hasSuggestOut := false
	fs.Visit(func(option *flag.Flag) {
		if option.Name == "suggest-out" {
			hasSuggestOut = true
		}
	})
	if hasSuggestOut && (!*suggest || strings.TrimSpace(*suggestOut) == "") {
		return printErr("Invalid suggestion options", errors.New("--suggest-out requires --suggest and a nonblank path"))
	}
	if *suggest {
		suggestionPath = *suggestOut
		if suggestionPath == "" {
			suggestionPath = filepath.Join(filepath.Dir(*path), "scenario-suggestions.yaml")
		}
		if _, err := os.Lstat(suggestionPath); err == nil {
			return printErr("Suggestion file already exists", errors.New("choose a new --suggest-out path"))
		} else if !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not inspect suggestion path", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report := runAutomationScenarios(ctx, client, *app, scenarios, *requireCoverage)
	if *suggest {
		summary, err := writeAutomationSuggestions(suggestionPath, scenarios[0].definitionPath, scenarios[0].request.Definition, unexcludedAutomationCoverageHints(report))
		if err != nil {
			return printErr("Could not write scenario suggestions", err)
		}
		report.Suggestions = &summary
	}
	return printAutomationCheckReport(report)
}

func runAutomationScenarios(ctx context.Context, client *api.Client, app string, scenarios []loadedAutomationScenario, requireCoverage ...bool) automationCheckReport {
	coverage := automationScenarioCoverage{}
	report := automationCheckReport{CoverageHints: []automationCoverageHint{}, Passed: true, Scenarios: make([]automationScenarioResult, 0, len(scenarios))}
	for _, scenario := range scenarios {
		response, err := client.SimulateAutomation(ctx, app, scenario.request)
		var result automationScenarioResult
		if err != nil {
			// Keep service response bodies and sample values out of assertion reports.
			result = automationScenarioResult{Name: scenario.scenario.Name, Failures: []string{"simulation request failed"}}
		} else {
			result = evaluateAutomationScenario(scenario, response)
			if result.Passed {
				coverage.observe(response)
			}
		}
		report.Passed = report.Passed && result.Passed
		report.Scenarios = append(report.Scenarios, result)
	}
	if len(scenarios) > 0 {
		report.CoverageHints = coverage.hints(scenarios[0].request.Definition)
		applyAutomationCoverageGate(&report, len(requireCoverage) > 0 && requireCoverage[0], scenarios[0].coverageExclusions)
	}
	return report
}

func printAutomationCheckReport(report automationCheckReport) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		for _, result := range report.Scenarios {
			status := "PASS"
			if !result.Passed {
				status = "FAIL"
			}
			if _, err := fmt.Fprintf(osStdout, "%s %s\n", status, result.Name); err != nil {
				return printErr("Could not write scenario report", err)
			}
			for _, failure := range result.Failures {
				if _, err := fmt.Fprintf(osStdout, "  %s\n", failure); err != nil {
					return printErr("Could not write scenario report", err)
				}
			}
		}
	}
	if !jsonOutput {
		for _, hint := range report.CoverageHints {
			if _, err := fmt.Fprintf(osStdout, "Coverage hint: step %q [%s]: %s\n", hint.Step, hint.Code, hint.Message); err != nil {
				return printErr("Could not write coverage hints", err)
			}
		}
	}
	if !jsonOutput && report.Suggestions != nil {
		if report.Suggestions.Count == 0 {
			if _, err := fmt.Fprintln(osStdout, "No scenario suggestions needed; no file created."); err != nil {
				return printErr("Could not write suggestion summary", err)
			}
		} else {
			if _, err := fmt.Fprintf(osStdout, "Wrote %d scenario starters to %q; review TODOs before running (%d hints remaining).\n", report.Suggestions.Count, report.Suggestions.Path, report.Suggestions.Remaining); err != nil {
				return printErr("Could not write suggestion summary", err)
			}
		}
	}
	if !jsonOutput && report.Coverage != nil {
		for _, exclusion := range report.Coverage.Exclusions {
			if _, err := fmt.Fprintf(osStdout, "Coverage exclusion: step %q [%s] loop %q: %s\n", exclusion.Step, exclusion.Code, exclusion.Loop, exclusion.Reason); err != nil {
				return printErr("Could not write coverage exclusions", err)
			}
		}
		if report.Coverage.Required {
			status := "PASS"
			if !report.Coverage.Passed {
				status = "FAIL"
			}
			if _, err := fmt.Fprintf(osStdout, "%s coverage gate: %d unexcluded hints remain.\n", status, report.Coverage.Remaining); err != nil {
				return printErr("Could not write coverage gate", err)
			}
		}
	}
	if !report.Passed {
		return 1
	}
	return 0
}

func loadAutomationScenarios(path string) ([]loadedAutomationScenario, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, api.AutomationSimulationRequestMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > api.AutomationSimulationRequestMaxBytes {
		return nil, errors.New("scenario suite exceeds the simulation request byte limit")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var suite automationScenarioSuite
	if err := decoder.Decode(&suite); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("suite must contain exactly one YAML document")
	}
	if strings.TrimSpace(suite.Definition) == "" || len(suite.Scenarios) == 0 || len(suite.Scenarios) > 32 {
		return nil, errors.New("suite requires definition and 1..32 scenarios")
	}
	resolve := func(name string) string {
		if name == "" || filepath.IsAbs(name) {
			return name
		}
		return filepath.Join(filepath.Dir(path), name)
	}
	definition, ok := readAutomationDefinition(resolve(suite.Definition))
	if !ok {
		return nil, errors.New("could not load suite definition")
	}
	if err := validateAutomationCoverageExclusions(definition, suite.CoverageExclusions); err != nil {
		return nil, err
	}
	loops := map[string]bool{}
	steps := map[string]bool{}
	for _, step := range definition.Steps {
		steps[step.Name] = true
		loops[step.Name] = step.ForEach != nil
	}
	names := map[string]bool{}
	loaded := make([]loadedAutomationScenario, 0, len(suite.Scenarios))
	var totalRequestBytes int64
	for _, scenario := range suite.Scenarios {
		if strings.TrimSpace(scenario.Name) == "" || len(scenario.Name) > 128 || strings.ContainsAny(scenario.Name, "\r\n\t") || names[scenario.Name] {
			return nil, errors.New("scenario names must be unique, nonempty, single-line and at most 128 bytes")
		}
		names[scenario.Name] = true
		if len(scenario.Expect) == 0 && len(scenario.ExpectItems) == 0 {
			return nil, fmt.Errorf("scenario %q requires step expectations", scenario.Name)
		}
		outputs := map[string]json.RawMessage{}
		for name, expect := range scenario.Expect {
			if !steps[name] {
				return nil, fmt.Errorf("scenario %q expects unknown root step %q", scenario.Name, name)
			}
			output, err := prepareAutomationExpectation(expect, fmt.Sprintf("step %q", name))
			if err != nil {
				return nil, fmt.Errorf("scenario %q: %w", scenario.Name, err)
			}
			if output != nil {
				outputs[name] = output
			}
		}
		itemOutputs := map[automationItemExpectationKey]json.RawMessage{}
		for loop, indexes := range scenario.ExpectItems {
			if !loops[loop] {
				return nil, fmt.Errorf("scenario %q expects items for unknown or non-loop step %q", scenario.Name, loop)
			}
			if len(indexes) == 0 || len(indexes) > api.WorkflowForEachMaxItems {
				return nil, errors.New("item expectations require 1..128 indexes per loop")
			}
			for value, expect := range indexes {
				index, err := automationExpectationItemIndex(value)
				if err != nil {
					return nil, err
				}
				output, err := prepareAutomationExpectation(expect, fmt.Sprintf("loop %q item %d", loop, index))
				if err != nil {
					return nil, fmt.Errorf("scenario %q: %w", scenario.Name, err)
				}
				if output != nil {
					itemOutputs[automationItemExpectationKey{loop, index}] = output
				}
			}
		}

		request, err := loadAutomationSimulation(definition, resolve(scenario.InputFile), resolve(scenario.MockOutputsFile), resolve(scenario.MockItemOutputsFile), resolve(scenario.MockAttemptsFile), resolve(scenario.MockItemAttemptsFile))
		if err != nil {
			return nil, fmt.Errorf("scenario %q: %w", scenario.Name, err)
		}
		requestJSON, err := json.Marshal(request)
		if err != nil {
			return nil, err
		}
		totalRequestBytes += int64(len(requestJSON))
		if totalRequestBytes > 8*api.AutomationSimulationRequestMaxBytes {
			return nil, errors.New("combined scenario requests exceed the suite byte limit")
		}
		loaded = append(loaded, loadedAutomationScenario{coverageExclusions: suite.CoverageExclusions, definitionPath: resolve(suite.Definition), scenario: scenario, request: request, outputs: outputs, itemOutputs: itemOutputs})
	}
	return loaded, nil
}

// Read expected numbers without YAML's float64 fallback for large integers.
func automationScenarioExpectedValue(node *yaml.Node, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("expected output exceeds 64 nesting levels")
	}
	switch node.Kind {
	case yaml.MappingNode:
		object := map[string]any{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Tag != "!!str" {
				return nil, errors.New("expected output object keys must be strings")
			}
			if _, duplicate := object[key.Value]; duplicate {
				return nil, errors.New("expected output has duplicate keys")
			}
			value, err := automationScenarioExpectedValue(node.Content[i+1], depth+1)
			if err != nil {
				return nil, err
			}
			object[key.Value] = value
		}
		return object, nil
	case yaml.SequenceNode:
		array := make([]any, len(node.Content))
		for i, item := range node.Content {
			value, err := automationScenarioExpectedValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			array[i] = value
		}
		return array, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return nil, nil
		case "!!str", "!!timestamp":
			return node.Value, nil
		case "!!bool":
			var value bool
			err := node.Decode(&value)
			return value, err
		case "!!int", "!!float":
			value := strings.TrimPrefix(strings.ReplaceAll(node.Value, "_", ""), "+")
			decoder := json.NewDecoder(strings.NewReader(value))
			decoder.UseNumber()
			var number any
			if err := decoder.Decode(&number); err == nil && json.Valid([]byte(value)) {
				if number, ok := number.(json.Number); ok {
					return number, nil
				}
			}
			if node.Tag == "!!int" {
				if integer, ok := new(big.Int).SetString(value, 0); ok {
					return json.Number(integer.String()), nil
				}
			}
			return nil, errors.New("expected output numbers must be finite JSON numbers")
		}
	}
	return nil, errors.New("expected output must contain JSON-compatible values without YAML aliases or custom tags")
}

func validAutomationScenarioState(state string) bool {
	switch state {
	case "would_execute", "mocked", "would_wait", "resolved", "expanded", "skipped", "blocked", "would_retry", "timed_out", "failed", "dead", "error":
		return true
	}
	return false
}

func evaluateAutomationScenario(loaded loadedAutomationScenario, response api.SimulateAutomationResponse) automationScenarioResult {
	result := automationScenarioResult{Name: loaded.scenario.Name, DefinitionValid: response.DefinitionValid, Complete: response.Complete, Failures: []string{}}
	if !response.DefinitionValid {
		result.Failures = append(result.Failures, "definition validation failed")
	}
	if len(response.Issues) > 0 {
		result.Failures = append(result.Failures, "simulation reported issues")
	}
	if (loaded.scenario.RequireComplete == nil || *loaded.scenario.RequireComplete) && !response.Complete {
		result.Failures = append(result.Failures, "trace is incomplete")
	}
	rows := map[string]api.AutomationSimulationStep{}
	for _, row := range response.Trace {
		if row.ParentStep == "" && row.ItemIndex == nil {
			rows[row.StepName] = row
		}
	}
	names := make([]string, 0, len(loaded.scenario.Expect))
	for name := range loaded.scenario.Expect {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		expect := loaded.scenario.Expect[name]
		row, ok := rows[name]
		if !ok {
			result.Failures = append(result.Failures, fmt.Sprintf("step %q is missing from the trace", name))
			continue
		}
		output, hasOutput := loaded.outputs[name]
		result.Failures = append(result.Failures, evaluateAutomationStepExpectation(fmt.Sprintf("step %q", name), expect, output, hasOutput, row)...)
	}
	itemRows := map[automationItemExpectationKey]api.AutomationSimulationStep{}
	duplicates := map[automationItemExpectationKey]bool{}
	for _, row := range response.Trace {
		if row.ParentStep == "" || row.ItemIndex == nil {
			continue
		}
		key := automationItemExpectationKey{row.ParentStep, *row.ItemIndex}
		if _, exists := itemRows[key]; exists {
			duplicates[key] = true
		}
		itemRows[key] = row
	}
	loops := make([]string, 0, len(loaded.scenario.ExpectItems))
	for loop := range loaded.scenario.ExpectItems {
		loops = append(loops, loop)
	}
	sort.Strings(loops)
	for _, loop := range loops {
		indexes := make([]int, 0, len(loaded.scenario.ExpectItems[loop]))
		for value := range loaded.scenario.ExpectItems[loop] {
			index, _ := automationExpectationItemIndex(value)
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			key := automationItemExpectationKey{loop, index}
			label := fmt.Sprintf("loop %q item %d", loop, index)
			row, exists := itemRows[key]
			if !exists {
				result.Failures = append(result.Failures, label+" is missing from the trace")
				continue
			}
			if duplicates[key] {
				result.Failures = append(result.Failures, label+" has duplicate trace entries")
				continue
			}
			output, hasOutput := loaded.itemOutputs[key]
			result.Failures = append(result.Failures, evaluateAutomationStepExpectation(label, loaded.scenario.ExpectItems[loop][strconv.Itoa(index)], output, hasOutput, row)...)
		}
	}

	result.Passed = len(result.Failures) == 0
	return result
}

// Compare JSON numbers exactly without losing integers above 2^53.
func equalAutomationScenarioJSON(a, b json.RawMessage) bool {
	normalize := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var number func(any) any
		number = func(v any) any {
			switch v := v.(type) {
			case json.Number:
				return canonicalAutomationScenarioNumber(string(v))
			case []any:
				for i := range v {
					v[i] = number(v[i])
				}
				return v
			case map[string]any:
				for k := range v {
					v[k] = number(v[k])
				}
				return v
			}
			return v
		}
		return number(value), nil
	}
	x, err := normalize(a)
	if err != nil {
		return false
	}
	y, err := normalize(b)
	return err == nil && reflect.DeepEqual(x, y)
}

// Store a decimal's significant digits and exponent without expanding large powers.
func canonicalAutomationScenarioNumber(value string) any {
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	exponent := new(big.Int)
	if i := strings.IndexAny(value, "eE"); i >= 0 {
		exponent.SetString(value[i+1:], 10)
		value = value[:i]
	}
	if i := strings.IndexByte(value, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(value)-i-1)))
		value = value[:i] + value[i+1:]
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		negative = false
		exponent.SetInt64(0)
		value = "0"
	} else {
		digits := strings.TrimRight(value, "0")
		exponent.Add(exponent, big.NewInt(int64(len(value)-len(digits))))
		value = digits
	}
	return struct {
		Negative         bool
		Digits, Exponent string
	}{negative, value, exponent.String()}
}

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	workflowInputRun        = "input"
	workflowInputStepOutput = "step_output"
	workflowInputFailure    = "failure"
)

// workflowInputReference is a statically discoverable reference embedded in
// a workflow step's JSON input template.
type workflowInputReference struct {
	Source   string
	StepName string
	Path     []string
}

// workflowInputReferences returns all references in a JSON input template.
// Templates can refer to workflow input, a dependency output, or failure
// context on an on_failure handler. A reference may omit its path to select
// the whole value. Step names are matched longest first, so names may contain
// dots.
func workflowInputReferences(template json.RawMessage, stepNames []string) ([]workflowInputReference, error) {
	if len(template) == 0 {
		return nil, nil
	}
	if !json.Valid(template) {
		return nil, errors.New("input template is not valid JSON")
	}
	value, err := decodeWorkflowJSON(template)
	if err != nil {
		return nil, err
	}
	names := append([]string(nil), stepNames...)
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) == len(names[j]) {
			return names[i] < names[j]
		}
		return len(names[i]) > len(names[j])
	})
	var references []workflowInputReference
	var visit func(any) error
	visit = func(current any) error {
		switch item := current.(type) {
		case string:
			found, err := workflowStringReferences(item, names)
			if err != nil {
				return err
			}
			references = append(references, found...)
		case []any:
			for _, child := range item {
				if err := visit(child); err != nil {
					return err
				}
			}
		case map[string]any:
			for key, child := range item {
				if strings.Contains(key, "{{") || strings.Contains(key, "}}") {
					return errors.New("templates are only supported in JSON values, not object keys")
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(value); err != nil {
		return nil, err
	}
	return references, nil
}

// ResolveWorkflowStepInput materializes a step's JSON input against the
// immutable run input and the outputs of its declared dependencies. A whole
// value reference retains its JSON type; references interpolated into a
// longer string must resolve to a scalar.
func ResolveWorkflowStepInput(template, runInput json.RawMessage, dependencyOutputs map[string]json.RawMessage, failureContext json.RawMessage) (json.RawMessage, error) {
	if len(template) == 0 {
		return cloneRawJSON(runInput), nil
	}
	if !json.Valid(template) {
		return nil, errors.New("input template is not valid JSON")
	}
	runValue, err := decodeWorkflowJSON(runInput)
	if err != nil {
		return nil, fmt.Errorf("decode run input: %w", err)
	}
	var failureValue any
	if len(failureContext) > 0 {
		failureValue, err = decodeWorkflowJSON(failureContext)
		if err != nil {
			return nil, fmt.Errorf("decode workflow failure context: %w", err)
		}
	}
	stepNames := make([]string, 0, len(dependencyOutputs))
	for name := range dependencyOutputs {
		stepNames = append(stepNames, name)
	}
	sort.Slice(stepNames, func(i, j int) bool {
		if len(stepNames[i]) == len(stepNames[j]) {
			return stepNames[i] < stepNames[j]
		}
		return len(stepNames[i]) > len(stepNames[j])
	})
	value, err := decodeWorkflowJSON(template)
	if err != nil {
		return nil, err
	}
	var decodedOutputs = make(map[string]any, len(dependencyOutputs))
	var outputErrors = make(map[string]error, len(dependencyOutputs))
	resolveReference := func(reference workflowInputReference) (any, error) {
		var root any
		switch reference.Source {
		case workflowInputRun:
			root = runValue
		case workflowInputStepOutput:
			raw, exists := dependencyOutputs[reference.StepName]
			if !exists {
				return nil, fmt.Errorf("step %q is not an available dependency", reference.StepName)
			}
			if len(raw) == 0 {
				return nil, fmt.Errorf("step %q has no output", reference.StepName)
			}
			if cached, ok := decodedOutputs[reference.StepName]; ok {
				root = cached
			} else if decodeErr, ok := outputErrors[reference.StepName]; ok {
				return nil, fmt.Errorf("step %q output: %w", reference.StepName, decodeErr)
			} else {
				decoded, decodeErr := decodeWorkflowJSON(raw)
				if decodeErr != nil {
					outputErrors[reference.StepName] = decodeErr
					return nil, fmt.Errorf("step %q output: %w", reference.StepName, decodeErr)
				}
				decodedOutputs[reference.StepName] = decoded
				root = decoded
			}
		case workflowInputFailure:
			if len(failureContext) == 0 {
				return nil, errors.New("failure context is not available")
			}
			root = failureValue
		default:
			return nil, fmt.Errorf("unknown workflow input source %q", reference.Source)
		}
		return workflowValueAtPath(root, reference.Path)
	}

	var resolveValue func(any) (any, error)
	resolveValue = func(current any) (any, error) {
		switch item := current.(type) {
		case string:
			return resolveWorkflowInputString(item, stepNames, resolveReference)
		case []any:
			resolved := make([]any, len(item))
			for i, child := range item {
				value, err := resolveValue(child)
				if err != nil {
					return nil, err
				}
				resolved[i] = value
			}
			return resolved, nil
		case map[string]any:
			resolved := make(map[string]any, len(item))
			for key, child := range item {
				if strings.Contains(key, "{{") || strings.Contains(key, "}}") {
					return nil, errors.New("templates are only supported in JSON values, not object keys")
				}
				value, err := resolveValue(child)
				if err != nil {
					return nil, err
				}
				resolved[key] = value
			}
			return resolved, nil
		default:
			return current, nil
		}
	}
	resolved, err := resolveValue(value)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("encode resolved step input: %w", err)
	}
	return encoded, nil
}

func decodeWorkflowJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func workflowStringReferences(value string, stepNames []string) ([]workflowInputReference, error) {
	var references []workflowInputReference
	for position := 0; position < len(value); {
		open := strings.Index(value[position:], "{{")
		close := strings.Index(value[position:], "}}")
		if close >= 0 && (open < 0 || close < open) {
			return nil, errors.New("unmatched template closing delimiter")
		}
		if open < 0 {
			break
		}
		open += position
		end := strings.Index(value[open+2:], "}}")
		if end < 0 {
			return nil, errors.New("unclosed template reference")
		}
		end += open + 2
		if nested := strings.Index(value[open+2:end], "{{"); nested >= 0 {
			return nil, errors.New("nested template references are not supported")
		}
		reference, err := parseWorkflowInputReference(strings.TrimSpace(value[open+2:end]), stepNames)
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
		position = end + 2
	}
	return references, nil
}

func parseWorkflowInputReference(expression string, stepNames []string) (workflowInputReference, error) {
	if expression == workflowInputRun {
		return workflowInputReference{Source: workflowInputRun}, nil
	}
	if strings.HasPrefix(expression, workflowInputRun+".") {
		path, err := workflowInputPath(expression[len(workflowInputRun)+1:])
		return workflowInputReference{Source: workflowInputRun, Path: path}, err
	}
	if expression == workflowInputFailure {
		return workflowInputReference{Source: workflowInputFailure}, nil
	}
	if strings.HasPrefix(expression, workflowInputFailure+".") {
		path, err := workflowInputPath(expression[len(workflowInputFailure)+1:])
		return workflowInputReference{Source: workflowInputFailure, Path: path}, err
	}
	for _, stepName := range stepNames {
		prefix := "steps." + stepName + ".output"
		if expression == prefix {
			return workflowInputReference{Source: workflowInputStepOutput, StepName: stepName}, nil
		}
		if strings.HasPrefix(expression, prefix+".") {
			path, err := workflowInputPath(expression[len(prefix)+1:])
			return workflowInputReference{Source: workflowInputStepOutput, StepName: stepName, Path: path}, err
		}
	}
	return workflowInputReference{}, fmt.Errorf("unknown or malformed reference %q", expression)
}

func workflowInputPath(value string) ([]string, error) {
	parts := strings.Split(value, ".")
	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part {
			return nil, fmt.Errorf("invalid JSON path %q", value)
		}
	}
	return parts, nil
}

func resolveWorkflowInputString(value string, stepNames []string, resolve func(workflowInputReference) (any, error)) (any, error) {
	references, err := workflowStringReferences(value, stepNames)
	if err != nil {
		return nil, err
	}
	if len(references) == 0 {
		return value, nil
	}
	firstOpen := strings.Index(value, "{{")
	firstClose := strings.Index(value[firstOpen+2:], "}}") + firstOpen + 2
	if len(references) == 1 && firstOpen == 0 && firstClose == len(value)-2 {
		return resolve(references[0])
	}
	var result strings.Builder
	position := 0
	for {
		open := strings.Index(value[position:], "{{")
		if open < 0 {
			result.WriteString(value[position:])
			break
		}
		open += position
		result.WriteString(value[position:open])
		end := strings.Index(value[open+2:], "}}") + open + 2
		resolved, err := resolve(references[0])
		if err != nil {
			return nil, err
		}
		text, err := workflowInputScalarString(resolved)
		if err != nil {
			return nil, err
		}
		result.WriteString(text)
		references = references[1:]
		position = end + 2
		if len(references) == 0 {
			result.WriteString(value[position:])
			break
		}
	}
	return result.String(), nil
}

func workflowInputScalarString(value any) (string, error) {
	switch item := value.(type) {
	case string:
		return item, nil
	case json.Number:
		return item.String(), nil
	case bool:
		return strconv.FormatBool(item), nil
	case nil:
		return "null", nil
	default:
		return "", errors.New("object or array references must occupy the whole JSON value")
	}
}

func workflowValueAtPath(value any, path []string) (any, error) {
	current := value
	for _, component := range path {
		switch item := current.(type) {
		case map[string]any:
			child, exists := item[component]
			if !exists {
				return nil, fmt.Errorf("JSON path component %q does not exist", component)
			}
			current = child
		case []any:
			index, err := strconv.Atoi(component)
			if err != nil || index < 0 || strconv.Itoa(index) != component || index >= len(item) {
				return nil, fmt.Errorf("JSON array index %q is invalid", component)
			}
			current = item[index]
		default:
			return nil, fmt.Errorf("JSON path component %q traverses a scalar", component)
		}
	}
	return current, nil
}

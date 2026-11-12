package api

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// FunctionCallablesEnv carries an app's function_callables to its guest as
// a JSON object (ADR-20261111150000); the SDK functions clients read it.
const FunctionCallablesEnv = "GREGALE_FUNCTIONS"

var functionCallableName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// ValidateFunctionCallables checks a function_callables map: at most
// ServiceBindingTargetsMax entries, each a function name or app slug mapped
// to a valid app slug other than the function's own.
func ValidateFunctionCallables(callables map[string]string, self string) error {
	if len(callables) > ServiceBindingTargetsMax {
		return fmt.Errorf("function_callables has %d entries; the limit is %d", len(callables), ServiceBindingTargetsMax)
	}
	for name, slug := range callables {
		if !functionCallableName.MatchString(name) {
			return fmt.Errorf("function_callables name %q must be lowercase letters, digits, - and _", name)
		}
		if !ValidAppSlug(slug) {
			return fmt.Errorf("function_callables %q: %q is not an app slug", name, slug)
		}
		if slug == self {
			return fmt.Errorf("function_callables %q names the function's own app", name)
		}
	}
	return nil
}

// FunctionInputSchemaEnv carries an app's function_input_schema to its
// guest; the function adapters check typed-client calls against it.
const FunctionInputSchemaEnv = "FAAS_FUNCTION_INPUT_SCHEMA"

// FunctionCallHeader marks a request from an SDK functions client; only
// such requests are checked against the input schema, so the function's
// other HTTP traffic is unaffected.
const FunctionCallHeader = "X-Gregale-Function-Call"

// FunctionInputSchemaMaxBytes caps a function's input schema.
const FunctionInputSchemaMaxBytes = 64 << 10

// ValidateFunctionInputSchema checks a function_input_schema: a JSON object
// within FunctionInputSchemaMaxBytes. An empty object clears the schema.
func ValidateFunctionInputSchema(raw json.RawMessage) error {
	if len(raw) > FunctionInputSchemaMaxBytes {
		return fmt.Errorf("function_input_schema is %d bytes; the limit is %d", len(raw), FunctionInputSchemaMaxBytes)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("function_input_schema must be a JSON object: %w", err)
	}
	return nil
}

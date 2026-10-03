// Package executionworkflow contains validation shared by local and
// server-managed Runs workflow execution.
package executionworkflow

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/jsonschemautil"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const ResultSchemaDraft2020 = "https://json-schema.org/draft/2020-12/schema"

// Schema is a compiled JSON Schema Draft 2020-12 contract for a Run result.
type Schema = jsonschema.Schema

type externalSchemaLoader struct{}

func (externalSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("external JSON Schema resource %q is not supported", location)
}

// CompileResultSchema compiles a local Draft 2020-12 schema. Remote resources
// are unavailable so validation cannot trigger network requests.
func CompileResultSchema(raw json.RawMessage) (*Schema, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("result_schema is invalid JSON: %w", err)
	}
	if object, ok := document.(map[string]any); ok {
		if draft, declared := object["$schema"]; declared {
			if draftURL, ok := draft.(string); !ok || draftURL != ResultSchemaDraft2020 {
				return nil, fmt.Errorf("result_schema must use JSON Schema Draft 2020-12")
			}
		}
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(externalSchemaLoader{})
	const location = "https://gregale.dev/schemas/run-result.json"
	if err := compiler.AddResource(location, document); err != nil {
		return nil, fmt.Errorf("result_schema could not be loaded: %w", err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("result_schema is invalid: %w", err)
	}
	return schema, nil
}

// ValidateResult returns an empty string when result matches schema. The
// detail is safe to persist as managed workflow status and is stable across
// worker restarts because it is derived from the immutable schema and receipt.
func ValidateResult(result json.RawMessage, schema *Schema) string {
	if schema == nil {
		return ""
	}
	if len(result) == 0 || !json.Valid(result) {
		return "Run did not return a valid JSON result"
	}
	var value any
	if err := json.Unmarshal(result, &value); err != nil {
		return fmt.Sprintf("Run result is invalid JSON: %s", err)
	}
	if err := schema.Validate(value); err != nil {
		return validationDetail(err)
	}
	return ""
}

func validationDetail(err error) string {
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return err.Error()
	}
	for len(validationErr.Causes) > 0 {
		validationErr = validationErr.Causes[0]
	}
	path := jsonschemautil.JoinInstanceLocation(validationErr.InstanceLocation)
	reason := "does not match the declared schema"
	if validationErr.ErrorKind != nil {
		if localized := validationErr.ErrorKind.LocalizedString(jsonschemautil.DefaultPrinter); localized != "" {
			reason = localized
		}
	}
	if path != "" {
		return fmt.Sprintf("result at %s %s", path, reason)
	}
	return reason
}

// Package executioncontract applies the workflow JSON Schema validator to
// standalone Runs without putting schemas or validation authority in a guest.
package executioncontract

import (
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionworkflow"
)

func Compile(raw json.RawMessage) (*executionworkflow.Schema, error) {
	if len(raw) > api.ExecutionContractSchemaMaxBytes {
		return nil, errors.New("run schema exceeds the 64 KiB limit")
	}
	return executionworkflow.CompileResultSchema(raw)
}

// CheckRequest runs before durable admission or quota/grant consumption.
// Error details never include schema constants or caller input values.
func CheckRequest(request api.CreateExecutionRequest) error {
	input, err := Compile(request.InputSchema)
	if err != nil {
		return errors.New("input_schema must be a bounded local JSON Schema Draft 2020-12 contract")
	}
	if _, err := Compile(request.ResultSchema); err != nil {
		return errors.New("result_schema must be a bounded local JSON Schema Draft 2020-12 contract")
	}
	value := request.Input
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	if executionworkflow.ValidateResult(value, input) != "" {
		return errors.New("input does not match input_schema")
	}
	return nil
}

// Validate records contract validity separately from execution status. The
// receipt keeps its original result and artifacts even when validation fails.
func Validate(result json.RawMessage, raw json.RawMessage) (*api.ExecutionResultValidation, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	schema, err := Compile(raw)
	if err != nil {
		return nil, errors.New("sealed result contract is invalid")
	}
	if executionworkflow.ValidateResult(result, schema) == "" {
		return &api.ExecutionResultValidation{Status: "valid"}, nil
	}
	return &api.ExecutionResultValidation{Status: "invalid", Message: "Run result does not match result_schema"}, nil
}

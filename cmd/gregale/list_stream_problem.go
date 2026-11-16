package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// Decorate the existing Problem only for an opted-in stream or typed local flag rule.
// RawMessage retains all existing field values, including numeric precision.
func writeJSONProblemForError(w io.Writer, problem api.Problem, code int, cause error) error {
	return writeJSONProblemForErrorWithCLI(w, problem, code, cause, nil)
}

func writeJSONProblemForErrorWithCLI(w io.Writer, problem api.Problem, code int, cause error, advice *cliFlagDiagnostic) error {
	var stream *listStreamError
	hasStream := errors.As(cause, &stream)
	rule := listFlagRuleDiagnosticForError(cause)
	var serverError *APIError
	if errors.As(cause, &serverError) {
		rule, advice = nil, nil
	}
	if !hasStream && rule == nil {
		return writeJSONProblemWithCLI(w, problem, code, advice)
	}
	var encoded bytes.Buffer
	if err := writeJSONProblemWithCLI(&encoded, problem, code, advice); err != nil {
		return err
	}
	if !hasStream {
		return writeListFlagRuleProblem(w, encoded.Bytes(), rule)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded.Bytes(), &body); err != nil {
		return err
	}
	progress, err := json.Marshal(stream.progress)
	if err != nil {
		return err
	}
	body["pagination"] = progress
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if rule != nil {
		return writeListFlagRuleProblem(w, data, rule)
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

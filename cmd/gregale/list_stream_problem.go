package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// Decorate the existing local Problem envelope only for an opted-in stream.
// RawMessage retains all existing field values, including numeric precision.
func writeJSONProblemForError(w io.Writer, problem api.Problem, code int, cause error) error {
	var stream *listStreamError
	if !errors.As(cause, &stream) {
		return writeJSONProblemWithExit(w, problem, code)
	}
	var encoded bytes.Buffer
	if err := writeJSONProblemWithExit(&encoded, problem, code); err != nil {
		return err
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
	_, err = w.Write(append(data, '\n'))
	return err
}

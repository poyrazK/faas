package main

import (
	"encoding/json"
	"errors"
	"io"
)

// Decorate the complete existing Problem without rounding numbers or losing
// correlation, codes, categories, hints, CLI extensions or stream progress.
func writeListFlagRuleProblem(w io.Writer, original []byte, diagnostic *cliFlagRuleDiagnostic) error {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(original, &body); err != nil {
		return err
	}
	if body == nil {
		return errors.New("flag rule Problem must be a JSON object")
	}
	metadata, err := json.Marshal(diagnostic)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &fields); err != nil {
		return err
	}
	if existing := body["cli"]; len(existing) != 0 && string(existing) != "null" {
		var previous map[string]json.RawMessage
		if err := json.Unmarshal(existing, &previous); err != nil {
			return err
		}
		for key, value := range previous {
			if _, replaced := fields[key]; !replaced {
				fields[key] = value
			}
		}
	}
	body["cli"], err = json.Marshal(fields)
	if err != nil {
		return err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

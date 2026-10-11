package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowRunInput(inline, path string, inlineSet, fileSet bool) (json.RawMessage, error) {
	return workflowJSONInput(inline, path, inlineSet, fileSet, "input", api.WorkflowRunInputMaxBytes)
}

func workflowJSONInput(inline, path string, inlineSet, fileSet bool, flagName string, maxBytes int64) (json.RawMessage, error) {
	if fileSet && inlineSet {
		return nil, fmt.Errorf("choose --%s or --%s-file, not both", flagName, flagName)
	}
	if fileSet {
		if strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("--%s-file requires a non-empty path or - for stdin", flagName)
		}
		if path != "-" {
			return readSimulationJSONFile(path, maxBytes)
		}
		data, err := io.ReadAll(io.LimitReader(osStdin, maxBytes+1))
		if err != nil {
			return nil, fmt.Errorf("could not read workflow %s from stdin: %w", flagName, err)
		}
		inline = string(data)
	}
	if int64(len(inline)) > maxBytes {
		return nil, fmt.Errorf("workflow %s exceeds the %d-byte limit", flagName, maxBytes)
	}
	if !json.Valid([]byte(inline)) {
		return nil, fmt.Errorf("workflow %s must be valid JSON containing exactly one value", flagName)
	}
	return json.RawMessage(inline), nil
}

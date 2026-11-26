package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

const maxMCPTaskGroupPlanBytes = 1 << 20

type mcpTaskGroupSubmission struct {
	tool           string
	args           map[string]any
	deferFormInput bool
	requestSHA256  string
}

func readMCPTaskGroupPlan(path string) ([]mcpTaskGroupSubmission, error) {
	path = filepath.Clean(path)
	dir, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, errors.New("could not open the Task group plan directory")
	}
	defer func() { _ = dir.Close() }()
	body, err := readCustomerOperationSourceFile(dir, filepath.Base(path), maxMCPTaskGroupPlanBytes)
	if err != nil {
		return nil, errors.New("the Task group plan must be a regular JSON file up to 1 MiB")
	}
	var entries []json.RawMessage
	if !utf8.Valid(body) ||
		json.Unmarshal(body, &entries) != nil || len(entries) == 0 || len(entries) > maxMCPTaskGroupMembers {
		return nil, errors.New("the Task group plan must contain an array of 1 to 64 tool calls")
	}
	result := make([]mcpTaskGroupSubmission, len(entries))
	for i, entry := range entries {
		result[i], err = parseMCPTaskGroupSubmission(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid Task group plan member %d: %w", i, err)
		}
	}
	return result, nil
}

func parseMCPTaskGroupSubmission(raw []byte) (mcpTaskGroupSubmission, error) {
	var wire struct {
		ToolName       string          `json:"toolName"`
		Arguments      json.RawMessage `json:"arguments"`
		DeferFormInput json.RawMessage `json:"deferFormInput"`
	}
	var entry mcpTaskGroupSubmission
	if decodeCustomerOperationReceipt(raw, &wire) != nil || strings.TrimSpace(wire.ToolName) == "" {
		return entry, errors.New("expected toolName, optional object arguments and optional boolean deferFormInput")
	}
	entry.tool, entry.args = wire.ToolName, make(map[string]any)
	if len(wire.Arguments) != 0 && (json.Unmarshal(wire.Arguments, &entry.args) != nil || entry.args == nil) {
		return entry, errors.New("arguments must be a JSON object")
	}
	switch string(wire.DeferFormInput) {
	case "true":
		entry.deferFormInput = true
	case "", "false":
	default:
		return entry, errors.New("deferFormInput must be a boolean")
	}
	// Hash the same values sent on the wire, including the input capabilities.
	var fingerprint strings.Builder
	err := appendMCPTaskGroupFingerprint(&fingerprint, map[string]any{
		"tool": entry.tool, "arguments": entry.args,
		"capabilities": mcphosting.TaskSubmissionCapabilities(entry.deferFormInput),
	})
	if err != nil {
		return entry, err
	}
	entry.requestSHA256 = toolpublication.Digest([]byte(fingerprint.String()))
	return entry, nil
}

// The Node SDK's jobValueHash orders object keys by UTF-16 and prefixes
// each IEEE-754 number with n. Strings use JSON.stringify escaping.
func appendMCPTaskGroupFingerprint(dst *strings.Builder, value any) error {
	switch value := value.(type) {
	case float64:
		if math.Trunc(value) == value && math.Abs(value) > (1<<53)-1 {
			return errors.New("arguments contain an integer outside the JavaScript safe range")
		}
		if value == 0 {
			value = 0 // Normalize negative zero.
		}
		_, _ = fmt.Fprintf(dst, "n%016x", math.Float64bits(value))
	case []any:
		dst.WriteByte('[')
		for i, item := range value {
			if i != 0 {
				dst.WriteByte(',')
			}
			if err := appendMCPTaskGroupFingerprint(dst, item); err != nil {
				return err
			}
		}
		dst.WriteByte(']')
	case map[string]any:
		return appendMCPTaskGroupFingerprintObject(dst, value)
	default:
		encoded, _ := json.Marshal(value) // The remaining values are JSON primitives.
		dst.WriteString(mcpTaskGroupStringify.Replace(string(encoded)))
	}
	return nil
}

func appendMCPTaskGroupFingerprintObject(dst *strings.Builder, object map[string]any) error {
	type objectKey struct {
		text  string
		units []uint16
	}
	keys := make([]objectKey, 0, len(object))
	for key := range object {
		keys = append(keys, objectKey{key, utf16.Encode([]rune(key))})
	}
	slices.SortFunc(keys, func(a, b objectKey) int { return slices.Compare(a.units, b.units) })
	dst.WriteByte('{')
	for i, key := range keys {
		if i != 0 {
			dst.WriteByte(',')
		}
		encoded, _ := json.Marshal(key.text)
		dst.WriteString(mcpTaskGroupStringify.Replace(string(encoded)))
		dst.WriteByte(':')
		if err := appendMCPTaskGroupFingerprint(dst, object[key.text]); err != nil {
			return err
		}
	}
	dst.WriteByte('}')
	return nil
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// edgeRuleMatchFlagMaxBytes bounds --match input (the server bounds the
// expression itself; this only stops an accidental huge file).
const edgeRuleMatchFlagMaxBytes = 64 << 10

// parseEdgeRuleMatchFlag reads an ADR-906 match condition from --match:
// inline JSON, @file, or - for stdin. Empty means no condition. Unknown
// fields are rejected and the condition is validated locally so mistakes
// surface before the request is sent.
func parseEdgeRuleMatchFlag(value string) (*api.EdgeRuleMatchExpr, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	raw := []byte(value)
	switch {
	case value == "-":
		var err error
		if raw, err = io.ReadAll(io.LimitReader(osStdin, edgeRuleMatchFlagMaxBytes+1)); err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
	case strings.HasPrefix(value, "@"):
		file, err := openCustomerFile(value[1:])
		if err != nil {
			return nil, err
		}
		defer func() { _ = file.Close() }()
		if raw, err = io.ReadAll(io.LimitReader(file, edgeRuleMatchFlagMaxBytes+1)); err != nil {
			return nil, fmt.Errorf("read %s: %w", value[1:], err)
		}
	}
	if len(raw) > edgeRuleMatchFlagMaxBytes {
		return nil, fmt.Errorf("condition exceeds %d bytes", edgeRuleMatchFlagMaxBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var expr api.EdgeRuleMatchExpr
	if err := dec.Decode(&expr); err != nil {
		return nil, fmt.Errorf("decode condition: %w", err)
	}
	if prob := api.ValidateEdgeRuleMatch(&expr); prob != nil {
		return nil, fmt.Errorf("%s", prob.Detail)
	}
	return &expr, nil
}

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// WorkflowGuardSpec is a bounded predicate over run input and direct dependency
// outputs. Exactly one of all, any, not, or a ref/op/value leaf is allowed.
// References use the input template paths without {{ }} delimiters.
type WorkflowGuardSpec struct {
	All   []WorkflowGuardSpec `json:"all,omitempty"`
	Any   []WorkflowGuardSpec `json:"any,omitempty"`
	Not   *WorkflowGuardSpec  `json:"not,omitempty"`
	Ref   string              `json:"ref,omitempty"`
	Op    string              `json:"op,omitempty"`
	Value json.RawMessage     `json:"value,omitempty"`
}

var ErrWorkflowGuardInvalid = errors.New("workflow: invalid when guard")

func (g *WorkflowGuardSpec) UnmarshalJSON(data []byte) error {
	if len(data) > WorkflowGuardMaxBytes {
		return fmt.Errorf("%w: exceeds %d bytes", ErrWorkflowGuardInvalid, WorkflowGuardMaxBytes)
	}
	nodes := 0
	decoded, err := decodeWorkflowGuard(data, 1, &nodes)
	if err == nil {
		*g = decoded
	}
	return err
}

func decodeWorkflowGuard(data []byte, depth int, nodes *int) (WorkflowGuardSpec, error) {
	(*nodes)++
	if depth > WorkflowGuardMaxDepth || *nodes > WorkflowGuardMaxNodes {
		return WorkflowGuardSpec{}, fmt.Errorf("%w: exceeds nesting or node limit", ErrWorkflowGuardInvalid)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return WorkflowGuardSpec{}, fmt.Errorf("%w: predicate must be an object", ErrWorkflowGuardInvalid)
	}
	var g WorkflowGuardSpec
	logical := 0
	for _, key := range []string{"all", "any", "not"} {
		if _, ok := fields[key]; ok {
			logical++
		}
	}
	if logical > 0 {
		if logical != 1 || len(fields) != 1 {
			return g, fmt.Errorf("%w: mixed predicate forms", ErrWorkflowGuardInvalid)
		}
	} else {
		if _, ok := fields["ref"]; !ok {
			return g, ErrWorkflowGuardInvalid
		}
		if _, ok := fields["op"]; !ok {
			return g, ErrWorkflowGuardInvalid
		}
		if _, ok := fields["value"]; !ok || len(fields) != 3 {
			return g, ErrWorkflowGuardInvalid
		}
	}
	for key, raw := range fields {
		switch key {
		case "all", "any":
			var children []json.RawMessage
			if err := json.Unmarshal(raw, &children); err != nil || len(children) == 0 || len(children) > WorkflowGuardMaxNodes {
				return g, fmt.Errorf("%w: %s requires a nonempty bounded array", ErrWorkflowGuardInvalid, key)
			}
			branch := make([]WorkflowGuardSpec, 0, len(children))
			for _, child := range children {
				decoded, err := decodeWorkflowGuard(child, depth+1, nodes)
				if err != nil {
					return g, err
				}
				branch = append(branch, decoded)
			}
			if key == "all" {
				g.All = branch
			} else {
				g.Any = branch
			}
		case "not":
			child, err := decodeWorkflowGuard(raw, depth+1, nodes)
			if err != nil {
				return g, err
			}
			g.Not = &child
		case "ref", "op":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return g, fmt.Errorf("%w: %s requires a string", ErrWorkflowGuardInvalid, key)
			}
			if key == "ref" {
				g.Ref = value
			} else {
				g.Op = value
			}
		case "value":
			g.Value = cloneRawJSON(raw)
		default:
			return g, fmt.Errorf("%w: unknown field %q", ErrWorkflowGuardInvalid, key)
		}
	}
	return g, nil
}

// ValidateWorkflowGuard checks structure, literal types and dependency access.
func ValidateWorkflowGuard(g *WorkflowGuardSpec, dependencies []string) error {
	if g == nil {
		return nil
	}
	names := workflowGuardNames(dependencies)
	nodes := 0
	var visit func(*WorkflowGuardSpec, int) error
	visit = func(p *WorkflowGuardSpec, depth int) error {
		nodes++
		if p == nil || depth > WorkflowGuardMaxDepth || nodes > WorkflowGuardMaxNodes {
			return fmt.Errorf("%w: exceeds nesting or node limit", ErrWorkflowGuardInvalid)
		}
		leaf := p.Ref != "" || p.Op != "" || len(p.Value) != 0
		if boolCount(p.All != nil, p.Any != nil, p.Not != nil, leaf) != 1 {
			return fmt.Errorf("%w: specify exactly one predicate form", ErrWorkflowGuardInvalid)
		}
		if p.Not != nil {
			return visit(p.Not, depth+1)
		}
		if p.All != nil || p.Any != nil {
			children := p.All
			if p.Any != nil {
				children = p.Any
			}
			if len(children) == 0 {
				return fmt.Errorf("%w: empty logical predicate", ErrWorkflowGuardInvalid)
			}
			for i := range children {
				if err := visit(&children[i], depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		ref, err := parseWorkflowInputReference(p.Ref, names)
		if err != nil || ref.Source == workflowInputFailure {
			return fmt.Errorf("%w: reference must select input or a direct dependency output", ErrWorkflowGuardInvalid)
		}
		value, err := decodeWorkflowJSON(p.Value)
		if err != nil {
			return fmt.Errorf("%w: value must be a JSON literal", ErrWorkflowGuardInvalid)
		}
		switch p.Op {
		case "eq", "ne":
			switch value.(type) {
			case nil, bool, string, json.Number:
			default:
				return fmt.Errorf("%w: equality requires a scalar literal", ErrWorkflowGuardInvalid)
			}
		case "gt", "gte", "lt", "lte":
			if _, ok := value.(json.Number); !ok {
				return fmt.Errorf("%w: numeric comparison requires a number", ErrWorkflowGuardInvalid)
			}
		case "exists":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%w: exists requires a boolean", ErrWorkflowGuardInvalid)
			}
		default:
			return fmt.Errorf("%w: unsupported operator %q", ErrWorkflowGuardInvalid, p.Op)
		}
		if number, ok := value.(json.Number); ok {
			if _, err := workflowGuardNumber(number); err != nil {
				return fmt.Errorf("%w: %w", ErrWorkflowGuardInvalid, err)
			}
		}
		return nil
	}
	if err := visit(g, 1); err != nil {
		return err
	}
	encoded, err := json.Marshal(g)
	if err != nil || len(encoded) > WorkflowGuardMaxBytes {
		return fmt.Errorf("%w: exceeds byte limit", ErrWorkflowGuardInvalid)
	}
	return nil
}

func workflowGuardNames(names []string) []string {
	result := append([]string(nil), names...)
	sort.Slice(result, func(i, j int) bool {
		if len(result[i]) == len(result[j]) {
			return result[i] < result[j]
		}
		return len(result[i]) > len(result[j])
	})
	return result
}

// EvaluateWorkflowGuard preserves JSON types and compares numbers exactly.
// Missing paths do not match comparisons (including ne). exists distinguishes
// missing paths from present null values. not negates that result normally.
func EvaluateWorkflowGuard(g *WorkflowGuardSpec, input json.RawMessage, outputs map[string]json.RawMessage) (bool, error) {
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	if err := ValidateWorkflowGuard(g, names); err != nil {
		return false, err
	}
	if g == nil {
		return true, nil
	}
	names = workflowGuardNames(names)
	var evaluate func(*WorkflowGuardSpec) (bool, error)
	evaluate = func(p *WorkflowGuardSpec) (bool, error) {
		if p.Not != nil {
			matched, err := evaluate(p.Not)
			return !matched, err
		}
		if p.All != nil || p.Any != nil {
			children, all := p.All, p.All != nil
			if !all {
				children = p.Any
			}
			for i := range children {
				matched, err := evaluate(&children[i])
				if err != nil || matched != all {
					return matched, err
				}
			}
			return all, nil
		}
		ref, _ := parseWorkflowInputReference(p.Ref, names) // validated above
		raw := input
		if ref.Source == workflowInputStepOutput {
			raw = outputs[ref.StepName]
		}
		var actual any
		exists := len(raw) > 0
		if exists {
			root, err := decodeWorkflowJSON(raw)
			if err != nil {
				return false, errors.New("workflow guard source is not valid JSON")
			}
			var errPath error
			actual, errPath = workflowValueAtPath(root, ref.Path)
			exists = errPath == nil
		}
		expected, _ := decodeWorkflowJSON(p.Value)
		if p.Op == "exists" {
			return exists == expected.(bool), nil
		}
		if !exists {
			return false, nil
		}
		if p.Op == "eq" || p.Op == "ne" {
			equal, err := workflowGuardEqual(actual, expected)
			return equal == (p.Op == "eq"), err
		}
		number, ok := actual.(json.Number)
		if !ok {
			return false, nil
		}
		a, err := workflowGuardNumber(number)
		if err != nil {
			return false, err
		}
		b, err := workflowGuardNumber(expected.(json.Number))
		if err != nil {
			return false, err
		}
		comparison := a.Cmp(b)
		switch p.Op {
		case "gt":
			return comparison > 0, nil
		case "gte":
			return comparison >= 0, nil
		case "lt":
			return comparison < 0, nil
		default:
			return comparison <= 0, nil
		}
	}
	return evaluate(g)
}

func workflowGuardNumber(number json.Number) (*big.Rat, error) {
	value := number.String()
	if len(value) > WorkflowGuardNumberMaxBytes {
		return nil, errors.New("workflow guard number exceeds size limit")
	}
	if pos := strings.IndexAny(value, "eE"); pos >= 0 {
		exponent, err := strconv.Atoi(value[pos+1:])
		if err != nil || exponent < -WorkflowGuardNumberMaxExponent || exponent > WorkflowGuardNumberMaxExponent {
			return nil, errors.New("workflow guard number exceeds exponent limit")
		}
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, errors.New("workflow guard number is invalid")
	}
	return ratio, nil
}

func workflowGuardEqual(a, b any) (bool, error) {
	switch left := a.(type) {
	case json.Number:
		right, ok := b.(json.Number)
		if !ok {
			return false, nil
		}
		x, err := workflowGuardNumber(left)
		if err != nil {
			return false, err
		}
		y, err := workflowGuardNumber(right)
		if err != nil {
			return false, err
		}
		return x.Cmp(y) == 0, nil
	case string:
		right, ok := b.(string)
		return ok && left == right, nil
	case bool:
		right, ok := b.(bool)
		return ok && left == right, nil
	case nil:
		return b == nil, nil
	default:
		return false, nil
	}
}

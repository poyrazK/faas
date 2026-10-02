package mcphosting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

const ContractFile = "gregale-mcp.lock.json"

// Contract records the caller-visible tool interface, not endpoint identity,
// credentials, timestamps or runtime behavior. Compare with the same permissions.
type Contract struct {
	Version         int    `json:"version"`
	ProtocolVersion string `json:"protocol_version"`
	Tools           []Tool `json:"tools"`
}

func NewContract(version string, tools []Tool) (Contract, error) {
	c := Contract{Version: 1, ProtocolVersion: version, Tools: append([]Tool{}, tools...)}
	if err := c.Validate(); err != nil {
		return Contract{}, err
	}
	slices.SortFunc(c.Tools, func(a, b Tool) int { return bytes.Compare([]byte(a.Name), []byte(b.Name)) })
	return c, nil
}

func (c Contract) Validate() error {
	if c.Version != 1 || (c.ProtocolVersion != ProtocolVersion && c.ProtocolVersion != LegacyProtocolVersion) {
		return fmt.Errorf("unsupported MCP contract format or protocol version")
	}
	if c.Tools == nil {
		return fmt.Errorf("MCP contract must include a tools array")
	}
	if _, err := json.Marshal(c); err != nil {
		return fmt.Errorf("encode MCP contract: %w", err)
	}
	seen := make(map[string]bool)
	for _, tool := range c.Tools {
		if tool.Name == "" || seen[tool.Name] {
			return fmt.Errorf("MCP contract contains an empty or duplicate tool name")
		}
		seen[tool.Name] = true
		if _, err := parameterHeaders(tool.InputSchema, nil); err != nil {
			return fmt.Errorf("tool %q input schema: %w", tool.Name, err)
		}
		for _, field := range []struct {
			name  string
			value json.RawMessage
		}{{"outputSchema", tool.OutputSchema}, {"annotations", tool.Annotations}} {
			if len(field.value) == 0 {
				continue
			}
			value, err := contractJSON(field.value)
			if err != nil {
				return fmt.Errorf("tool %q %s: %w", tool.Name, field.name, err)
			}
			if _, ok := value.(map[string]any); !ok {
				return fmt.Errorf("tool %q %s must be an object", tool.Name, field.name)
			}
		}
	}
	return nil
}

// MarshalContract sorts tool names and JSON keys without reducing schemas to a
// subset of JSON Schema. Raw schema numbers survive discovery and round trips.
func MarshalContract(c Contract) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	c, err := NewContract(c.ProtocolVersion, c.Tools)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encode MCP contract: %w", err)
	}
	value, err := contractJSON(data)
	if err != nil {
		return nil, err
	}
	data, err = json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("format MCP contract: %w", err)
	}
	if len(data)+1 > maxResponseBytes {
		return nil, fmt.Errorf("MCP contract exceeds diagnostic memory limit")
	}
	return append(data, '\n'), nil
}

func ReadContract(r io.Reader) (Contract, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return Contract{}, fmt.Errorf("read MCP contract: %w", err)
	}
	if len(data) > maxResponseBytes {
		return Contract{}, fmt.Errorf("MCP contract exceeds diagnostic memory limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	var c Contract
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("decode MCP contract: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("MCP contract must contain one JSON object")
	}
	return c, c.Validate()
}

func contractJSON(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode contract JSON: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("contract JSON must contain one value")
	}
	return value, nil
}

type ContractChange struct {
	Tool     string `json:"tool,omitempty"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"` // informational, breaking, needs_review
}

type ContractDiff struct {
	Compatible  bool             `json:"compatible"`
	Breaking    bool             `json:"breaking"`
	NeedsReview bool             `json:"needs_review"`
	Changes     []ContractChange `json:"changes"`
}

// CompareContracts is a conservative structural check, not a proof of arbitrary
// JSON Schema inclusion or tool behavior. Unknown changed keywords need review;
// references are preserved and never fetched. Input accepts must not narrow;
// output promises must not widen. Annotations never authorize execution.
func CompareContracts(before, after Contract) (ContractDiff, error) {
	for _, c := range []Contract{before, after} {
		if err := c.Validate(); err != nil {
			return ContractDiff{}, err
		}
	}
	d := ContractDiff{Compatible: true, Changes: make([]ContractChange, 0)}
	add := func(tool, path, kind, severity string) {
		d.Changes = append(d.Changes, ContractChange{Tool: tool, Path: path, Kind: kind, Severity: severity})
		d.Breaking = d.Breaking || severity == "breaking"
		d.NeedsReview = d.NeedsReview || severity == "needs_review"
		d.Compatible = !d.Breaking && !d.NeedsReview
	}
	if before.ProtocolVersion != after.ProtocolVersion {
		add("", "/protocol_version", "protocol_changed", "needs_review")
	}
	base, next := map[string]Tool{}, map[string]Tool{}
	for _, t := range before.Tools {
		base[t.Name] = t
	}
	for _, t := range after.Tools {
		next[t.Name] = t
	}
	for _, name := range contractKeys(base, next) {
		b, bok := base[name]
		a, aok := next[name]
		if !bok {
			add(name, "", "tool_added", "informational")
			continue
		}
		if !aok {
			add(name, "", "tool_removed", "breaking")
			continue
		}
		if b.Description != a.Description {
			add(name, "/description", "description_changed", "informational")
		}
		if !contractEqual(b.Annotations, a.Annotations) {
			add(name, "/annotations", "annotations_changed", "needs_review")
		}
		compareContractSchema(b.InputSchema, a.InputSchema, "/inputSchema", false, func(path, kind, severity string) { add(name, path, kind, severity) })
		if len(b.OutputSchema) == 0 && len(a.OutputSchema) != 0 {
			add(name, "/outputSchema", "output_schema_added", "informational")
		} else if len(b.OutputSchema) != 0 && len(a.OutputSchema) == 0 {
			add(name, "/outputSchema", "output_schema_removed", "breaking")
		} else if len(b.OutputSchema) != 0 {
			bv, _ := contractJSON(b.OutputSchema)
			av, _ := contractJSON(a.OutputSchema)
			compareContractSchema(bv, av, "/outputSchema", true, func(path, kind, severity string) { add(name, path, kind, severity) })
		}
	}
	return d, nil
}

func compareContractSchema(before, after any, path string, output bool, add func(string, string, string)) {
	compareContractSchemaDepth(before, after, path, output, 0, add)
}

// Bound work on deeply nested diagnostic input; never silently declare an
// unvisited changed subtree compatible. The snapshot still keeps the full schema.
func compareContractSchemaDepth(before, after any, path string, output bool, depth int, add func(string, string, string)) {
	if contractEqual(before, after) {
		return
	}
	if depth >= 64 {
		add(path, "schema_depth_requires_review", "needs_review")
		return
	}
	b, bok := before.(map[string]any)
	a, aok := after.(map[string]any)
	if !bok || !aok {
		add(path, "schema_changed", "needs_review")
		return
	}
	for _, key := range contractKeys(b, a) {
		bv, presentBefore := b[key]
		av, presentAfter := a[key]
		if presentBefore == presentAfter && contractEqual(bv, av) {
			continue
		}
		p := path + "/" + contractPointer(key)
		severity, kind := "needs_review", "schema_keyword_changed"
		if (presentBefore && bv == nil) || (presentAfter && av == nil) {
			add(p, kind, severity)
			continue
		}
		switch key {
		case "description", "title", "examples", "$comment":
			severity, kind = "informational", "schema_metadata_changed"
		case "type", "required", "enum":
			bs, bsok := contractSet(key, bv)
			as, asok := contractSet(key, av)
			if bsok && asok {
				if slices.Equal(bs, as) {
					continue
				}
				severity, kind = "informational", key+"_changed"
				// Required is the inverse of allowed type/enum sets.
				removedBreaks := output == (key == "required")
				if (removedBreaks && !contractSubset(bs, as)) || (!removedBreaks && !contractSubset(as, bs)) {
					severity = "breaking"
				}
			}
		case "properties":
			bp, bpok := contractProperties(bv)
			ap, apok := contractProperties(av)
			if bpok && apok {
				for _, name := range contractKeys(bp, ap) {
					v, existsBefore := bp[name]
					w, existsAfter := ap[name]
					pp := p + "/" + contractPointer(name)
					if !existsAfter {
						add(pp, "property_removed", "breaking")
						continue
					}
					if !existsBefore {
						level := "needs_review"
						// With closed input objects a new optional property only
						// expands accepted inputs. Open objects may gain constraints.
						if !output && b["additionalProperties"] == false {
							level = "informational"
						}
						add(pp, "property_added", level)
						continue
					}
					compareContractSchemaDepth(v, w, pp, output, depth+1, add)
				}
				continue
			}
		case "additionalProperties":
			bb, bbok := contractAdditional(bv)
			aa, aaok := contractAdditional(av)
			if bbok && aaok {
				if bb == aa {
					continue
				}
				severity, kind = "informational", "additional_properties_changed"
				if (!output && bb && !aa) || (output && !bb && aa) {
					severity = "breaking"
				}
			}
		case "items":
			if bv != nil && av != nil {
				compareContractSchemaDepth(bv, av, p, output, depth+1, add)
				continue
			}
		}
		add(p, kind, severity)
	}
}

func contractSet(kind string, value any) ([]string, bool) {
	if value == nil {
		if kind == "type" {
			return []string{"array", "boolean", "integer", "null", "number", "object", "string"}, true
		}
		if kind == "required" {
			return []string{}, true
		}
		return []string{"*"}, true // Unrestricted enum, handled by subset.
	}
	values, ok := value.([]any)
	if s, yes := value.(string); yes && kind == "type" {
		values, ok = []any{s}, true
	}
	if !ok {
		return nil, false
	}
	set := map[string]any{}
	for _, v := range values {
		if kind == "enum" {
			raw, err := json.Marshal(v)
			if err != nil {
				return nil, false
			}
			set["value:"+string(raw)] = nil
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		if kind == "type" && !slices.Contains([]string{"array", "boolean", "integer", "null", "number", "object", "string"}, s) {
			return nil, false
		}
		if kind == "required" {
			s = "property:" + s
		}
		set[s] = nil
		if kind == "type" && s == "number" {
			set["integer"] = nil
		}
	}
	return contractKeys(set), true
}

func contractSubset(a, b []string) bool {
	if slices.Contains(b, "*") {
		return true
	}
	allowed := make(map[string]bool, len(b))
	for _, v := range b {
		allowed[v] = true
	}
	for _, v := range a {
		if !allowed[v] {
			return false
		}
	}
	return true
}

func contractProperties(value any) (map[string]any, bool) {
	if value == nil {
		return map[string]any{}, true
	}
	v, ok := value.(map[string]any)
	return v, ok
}

func contractAdditional(value any) (bool, bool) {
	if value == nil {
		return true, true
	}
	v, ok := value.(bool)
	return v, ok
}

func contractKeys[V any](maps ...map[string]V) []string {
	seen := map[string]bool{}
	for _, m := range maps {
		for k := range m {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func contractEqual(a, b any) bool {
	canonical := func(v any) []byte {
		data, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		decoded, err := contractJSON(data)
		if err != nil {
			return nil
		}
		data, _ = json.Marshal(decoded)
		return data
	}
	return bytes.Equal(canonical(a), canonical(b))
}

func contractPointer(s string) string {
	var b bytes.Buffer
	for _, c := range s {
		switch c {
		case '~':
			b.WriteString("~0")
		case '/':
			b.WriteString("~1")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

package mcphosting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

const ContractFile = "gregale-mcp.lock.json"

// Contract records caller-visible catalog definitions, not endpoint identity,
// credentials, timestamps, resource contents or rendered prompt messages.
type Contract struct {
	Version           int                `json:"version"`
	ProtocolVersion   string             `json:"protocol_version"`
	Capabilities      []string           `json:"capabilities,omitempty"`
	Tools             []Tool             `json:"tools"`
	Resources         []Resource         `json:"resources,omitempty"`
	ResourceTemplates []ResourceTemplate `json:"resource_templates,omitempty"`
	Prompts           []Prompt           `json:"prompts,omitempty"`
}

func NewContract(version string, tools []Tool) (Contract, error) {
	return newContract(1, version, Catalog{Tools: tools})
}

func NewCatalogContract(version string, catalog Catalog) (Contract, error) {
	return newContract(2, version, catalog)
}

func newContract(formatVersion int, version string, catalog Catalog) (Contract, error) {
	capabilities := append([]string{}, catalog.Capabilities...)
	if formatVersion == 2 && catalog.Capabilities == nil {
		if len(catalog.Tools) != 0 || (len(catalog.Resources) == 0 && len(catalog.ResourceTemplates) == 0 && len(catalog.Prompts) == 0) {
			capabilities = append(capabilities, "tools")
		}
		if len(catalog.Resources) != 0 || len(catalog.ResourceTemplates) != 0 {
			capabilities = append(capabilities, "resources")
		}
		if len(catalog.Prompts) != 0 {
			capabilities = append(capabilities, "prompts")
		}
	}
	c := Contract{
		Version: formatVersion, ProtocolVersion: version, Capabilities: capabilities, Tools: append([]Tool{}, catalog.Tools...),
		Resources:         append([]Resource{}, catalog.Resources...),
		ResourceTemplates: append([]ResourceTemplate{}, catalog.ResourceTemplates...),
		Prompts:           append([]Prompt{}, catalog.Prompts...),
	}
	if err := c.Validate(); err != nil {
		return Contract{}, err
	}
	slices.SortFunc(c.Tools, func(a, b Tool) int { return bytes.Compare([]byte(a.Name), []byte(b.Name)) })
	slices.Sort(c.Capabilities)
	slices.SortFunc(c.Resources, func(a, b Resource) int { return bytes.Compare([]byte(a.URI), []byte(b.URI)) })
	slices.SortFunc(c.ResourceTemplates, func(a, b ResourceTemplate) int { return bytes.Compare([]byte(a.URITemplate), []byte(b.URITemplate)) })
	slices.SortFunc(c.Prompts, func(a, b Prompt) int { return bytes.Compare([]byte(a.Name), []byte(b.Name)) })
	return c, nil
}

func (c Contract) Validate() error {
	if (c.Version != 1 && c.Version != 2) || (c.ProtocolVersion != ProtocolVersion && c.ProtocolVersion != LegacyProtocolVersion) {
		return fmt.Errorf("unsupported MCP contract format or protocol version")
	}
	if c.Version == 1 && (len(c.Capabilities) != 0 || len(c.Resources) != 0 || len(c.ResourceTemplates) != 0 || len(c.Prompts) != 0) {
		return fmt.Errorf("MCP contract format 1 cannot contain capabilities, resources, templates or prompts")
	}
	if c.Version == 2 {
		if len(c.Capabilities) == 0 {
			return fmt.Errorf("MCP contract format 2 must include advertised capabilities")
		}
		seenCapabilities := make(map[string]bool, len(c.Capabilities))
		for _, capability := range c.Capabilities {
			if !slices.Contains([]string{"tools", "resources", "prompts"}, capability) || seenCapabilities[capability] {
				return fmt.Errorf("MCP contract contains an invalid or duplicate capability")
			}
			seenCapabilities[capability] = true
		}
		if (len(c.Tools) != 0 && !seenCapabilities["tools"]) || ((len(c.Resources) != 0 || len(c.ResourceTemplates) != 0) && !seenCapabilities["resources"]) || (len(c.Prompts) != 0 && !seenCapabilities["prompts"]) {
			return fmt.Errorf("MCP contract contains definitions for an unadvertised capability")
		}
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
	for _, resource := range c.Resources {
		if resource.URI == "" || seen["resource:"+resource.URI] {
			return fmt.Errorf("MCP contract contains an empty or duplicate resource URI")
		}
		seen["resource:"+resource.URI] = true
		if resource.Name == "" || (resource.Size != nil && *resource.Size < 0) {
			return fmt.Errorf("resource %q has invalid metadata", resource.URI)
		}
		if err := validateIcons("resource "+resource.URI, resource.Icons); err != nil {
			return err
		}
		if err := validateAnnotations("resource "+resource.URI, resource.Annotations); err != nil {
			return err
		}
	}
	for _, template := range c.ResourceTemplates {
		if template.URITemplate == "" || seen["template:"+template.URITemplate] {
			return fmt.Errorf("MCP contract contains an empty or duplicate resource template")
		}
		seen["template:"+template.URITemplate] = true
		if template.Name == "" {
			return fmt.Errorf("resource template %q has no name", template.URITemplate)
		}
		if err := validateIcons("resource template "+template.URITemplate, template.Icons); err != nil {
			return err
		}
		if err := validateAnnotations("resource template "+template.URITemplate, template.Annotations); err != nil {
			return err
		}
	}
	for _, prompt := range c.Prompts {
		if prompt.Name == "" || seen["prompt:"+prompt.Name] {
			return fmt.Errorf("MCP contract contains an empty or duplicate prompt name")
		}
		seen["prompt:"+prompt.Name] = true
		if err := validateAnnotations("prompt "+prompt.Name, prompt.Annotations); err != nil {
			return err
		}
		if err := validateIcons("prompt "+prompt.Name, prompt.Icons); err != nil {
			return err
		}
		arguments := map[string]bool{}
		for _, argument := range prompt.Arguments {
			if argument.Name == "" || arguments[argument.Name] {
				return fmt.Errorf("prompt %q contains an empty or duplicate argument", prompt.Name)
			}
			arguments[argument.Name] = true
		}
	}
	return nil
}

func validateAnnotations(item string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	value, err := contractJSON(raw)
	if err != nil {
		return fmt.Errorf("%s annotations: %w", item, err)
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("%s annotations must be an object", item)
	}
	return nil
}

func validateIcons(item string, icons []Icon) error {
	for _, icon := range icons {
		if icon.Src == "" {
			return fmt.Errorf("%s contains an icon without a source", item)
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
	var normalized Contract
	var err error
	if c.Version == 1 {
		normalized, err = NewContract(c.ProtocolVersion, c.Tools)
	} else {
		normalized, err = NewCatalogContract(c.ProtocolVersion, Catalog{Capabilities: c.Capabilities, Tools: c.Tools, Resources: c.Resources, ResourceTemplates: c.ResourceTemplates, Prompts: c.Prompts})
	}
	if err != nil {
		return nil, err
	}
	c = normalized
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
	Tool             string `json:"tool,omitempty"`
	Resource         string `json:"resource,omitempty"`
	ResourceTemplate string `json:"resource_template,omitempty"`
	Prompt           string `json:"prompt,omitempty"`
	Capability       string `json:"capability,omitempty"`
	Path             string `json:"path"`
	Kind             string `json:"kind"`
	Severity         string `json:"severity"` // informational, breaking, needs_review
}

type ContractDiff struct {
	Compatible    bool             `json:"compatible"`
	Breaking      bool             `json:"breaking"`
	NeedsReview   bool             `json:"needs_review"`
	StrictCatalog bool             `json:"strict_catalog,omitempty"`
	Changes       []ContractChange `json:"changes"`
}

type ContractDiffOptions struct {
	// StrictCatalog requires review when a caller gains visibility of a tool.
	// It does not verify permission to execute that tool.
	StrictCatalog bool
}

// CompareContracts is a conservative structural check, not a proof of arbitrary
// JSON Schema inclusion or tool behavior. Unknown changed keywords need review;
// references are preserved and never fetched. Input accepts must not narrow;
// output promises must not widen. Annotations never authorize execution.
func CompareContracts(before, after Contract) (ContractDiff, error) {
	return CompareContractsWithOptions(before, after, ContractDiffOptions{})
}

// CompareContractsWithOptions optionally treats catalog expansion as requiring
// review. Compare captures made with the same caller permissions and protocol.
func CompareContractsWithOptions(before, after Contract, options ContractDiffOptions) (ContractDiff, error) {
	for _, c := range []Contract{before, after} {
		if err := c.Validate(); err != nil {
			return ContractDiff{}, err
		}
	}
	d := ContractDiff{Compatible: true, StrictCatalog: options.StrictCatalog, Changes: make([]ContractChange, 0)}
	add := func(tool, path, kind, severity string) {
		d.Changes = append(d.Changes, ContractChange{Tool: tool, Path: path, Kind: kind, Severity: severity})
		d.Breaking = d.Breaking || severity == "breaking"
		d.NeedsReview = d.NeedsReview || severity == "needs_review"
		d.Compatible = !d.Breaking && !d.NeedsReview
	}
	addCatalog := func(kind, id, path, change, severity string) {
		entry := ContractChange{Path: path, Kind: change, Severity: severity}
		switch kind {
		case "resource":
			entry.Resource = id
		case "resource_template":
			entry.ResourceTemplate = id
		case "prompt":
			entry.Prompt = id
		case "capability":
			entry.Capability = id
		}
		d.Changes = append(d.Changes, entry)
		d.Breaking = d.Breaking || severity == "breaking"
		d.NeedsReview = d.NeedsReview || severity == "needs_review"
		d.Compatible = !d.Breaking && !d.NeedsReview
	}
	if before.ProtocolVersion != after.ProtocolVersion {
		add("", "/protocol_version", "protocol_changed", "needs_review")
	}
	baseCapabilities := contractCapabilities(before)
	nextCapabilities := contractCapabilities(after)
	baseCapabilitySet, nextCapabilitySet := map[string]bool{}, map[string]bool{}
	for _, capability := range baseCapabilities {
		baseCapabilitySet[capability] = true
	}
	for _, capability := range nextCapabilities {
		nextCapabilitySet[capability] = true
	}
	for _, capability := range contractKeys(baseCapabilitySet, nextCapabilitySet) {
		if !baseCapabilitySet[capability] {
			severity := "informational"
			if options.StrictCatalog {
				severity = "needs_review"
			}
			addCatalog("capability", capability, "", "capability_added", severity)
		} else if !nextCapabilitySet[capability] {
			addCatalog("capability", capability, "", "capability_removed", "breaking")
		}
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
			severity := "informational"
			if options.StrictCatalog {
				severity = "needs_review"
			}
			add(name, "", "tool_added", severity)
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
	compareResources := func(kind string, before, after map[string]any, strict bool) {
		for _, id := range contractKeys(before, after) {
			b, bok := before[id]
			a, aok := after[id]
			if !bok {
				severity := "informational"
				if strict {
					severity = "needs_review"
				}
				addCatalog(kind, id, "", kind+"_added", severity)
				continue
			}
			if !aok {
				addCatalog(kind, id, "", kind+"_removed", "breaking")
				continue
			}
			compareCatalogMetadata(kind, id, b, a, addCatalog)
		}
	}
	resourceMap := make(map[string]any, len(before.Resources))
	nextResourceMap := make(map[string]any, len(after.Resources))
	for _, v := range before.Resources {
		resourceMap[v.URI] = v
	}
	for _, v := range after.Resources {
		nextResourceMap[v.URI] = v
	}
	compareResources("resource", resourceMap, nextResourceMap, options.StrictCatalog)
	templateMap := make(map[string]any, len(before.ResourceTemplates))
	nextTemplateMap := make(map[string]any, len(after.ResourceTemplates))
	for _, v := range before.ResourceTemplates {
		templateMap[v.URITemplate] = v
	}
	for _, v := range after.ResourceTemplates {
		nextTemplateMap[v.URITemplate] = v
	}
	compareResources("resource_template", templateMap, nextTemplateMap, options.StrictCatalog)
	promptMap := make(map[string]any, len(before.Prompts))
	nextPromptMap := make(map[string]any, len(after.Prompts))
	for _, v := range before.Prompts {
		promptMap[v.Name] = v
	}
	for _, v := range after.Prompts {
		nextPromptMap[v.Name] = v
	}
	for _, name := range contractKeys(promptMap, nextPromptMap) {
		b, bok := promptMap[name].(Prompt)
		a, aok := nextPromptMap[name].(Prompt)
		if !bok {
			severity := "informational"
			if options.StrictCatalog {
				severity = "needs_review"
			}
			addCatalog("prompt", name, "", "prompt_added", severity)
			continue
		}
		if !aok {
			addCatalog("prompt", name, "", "prompt_removed", "breaking")
			continue
		}
		comparePrompt(name, b, a, addCatalog)
	}
	return d, nil
}

func compareCatalogMetadata(kind, id string, before, after any, add func(string, string, string, string, string)) {
	var metadata map[string]any
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	decodedBefore, _ := contractJSON(b)
	metadata, _ = decodedBefore.(map[string]any)
	var next map[string]any
	decodedAfter, _ := contractJSON(a)
	next, _ = decodedAfter.(map[string]any)
	for _, field := range contractKeys(metadata, next) {
		if contractEqual(metadata[field], next[field]) {
			continue
		}
		severity := "needs_review"
		change := "metadata_changed"
		switch field {
		case "title", "description", "size":
			severity = "informational"
		case "name", "mimeType", "annotations", "icons":
			severity = "needs_review"
		}
		add(kind, id, "/"+contractPointer(field), kind+"_"+change, severity)
	}
}

func contractCapabilities(contract Contract) []string {
	if contract.Version == 1 {
		return []string{"tools"}
	}
	return contract.Capabilities
}

func comparePrompt(name string, before, after Prompt, add func(string, string, string, string, string)) {
	beforeIcons, afterIcons := before.Icons, after.Icons
	if len(beforeIcons) == 0 {
		beforeIcons = nil
	}
	if len(afterIcons) == 0 {
		afterIcons = nil
	}
	for _, field := range []struct {
		name          string
		before, after any
		severity      string
	}{
		{"title", before.Title, after.Title, "informational"},
		{"description", before.Description, after.Description, "informational"},
		{"icons", beforeIcons, afterIcons, "needs_review"},
		{"annotations", before.Annotations, after.Annotations, "needs_review"},
	} {
		if !contractEqual(field.before, field.after) {
			add("prompt", name, "/"+field.name, "prompt_metadata_changed", field.severity)
		}
	}
	base, next := map[string]PromptArgument{}, map[string]PromptArgument{}
	for _, arg := range before.Arguments {
		base[arg.Name] = arg
	}
	for _, arg := range after.Arguments {
		next[arg.Name] = arg
	}
	for _, argName := range contractKeys(base, next) {
		b, bok := base[argName]
		a, aok := next[argName]
		path := "/arguments/" + contractPointer(argName)
		if !bok {
			severity := "informational"
			if a.Required {
				severity = "breaking"
			}
			add("prompt", name, path, "prompt_argument_added", severity)
			continue
		}
		if !aok {
			add("prompt", name, path, "prompt_argument_removed", "breaking")
			continue
		}
		if !b.Required && a.Required {
			add("prompt", name, path+"/required", "prompt_argument_required", "breaking")
		} else if b.Required && !a.Required {
			add("prompt", name, path+"/required", "prompt_argument_optional", "informational")
		}
		if b.Title != a.Title {
			add("prompt", name, path+"/title", "prompt_argument_metadata_changed", "informational")
		}
		if b.Description != a.Description {
			add("prompt", name, path+"/description", "prompt_argument_metadata_changed", "informational")
		}
	}
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

package mcphosting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func contractTool(t *testing.T, schema string, output bool) Tool {
	t.Helper()
	v, err := contractJSON([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	tool := Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}}
	if output {
		tool.OutputSchema = json.RawMessage(schema)
	} else {
		tool.InputSchema = v.(map[string]any)
	}
	return tool
}

func TestMCPContractSchemaDirectionAndReview(t *testing.T) {
	// adr: 426 — a local conservative gate, independent of promotion and runtime.
	for _, tc := range []struct {
		name, before, after, severity string
		output                        bool
	}{
		{"required input added", `{"type":"object"}`, `{"type":"object","required":["id"]}`, "breaking", false},
		{"required input removed", `{"required":["id"]}`, `{}`, "informational", false},
		{"literal wildcard property is not unrestricted", `{"required":["*"]}`, `{"required":["id"]}`, "breaking", false},
		{"required output removed", `{"required":["id"]}`, `{}`, "breaking", true},
		{"required output added", `{}`, `{"required":["id"]}`, "informational", true},
		{"input type narrowed", `{"type":["string","null"]}`, `{"type":"string"}`, "breaking", false},
		{"input type widened", `{"type":"string"}`, `{"type":["string","null"]}`, "informational", false},
		{"output type widened", `{"type":"string"}`, `{"type":["string","null"]}`, "breaking", true},
		{"output type narrowed", `{"type":["string","null"]}`, `{"type":"string"}`, "informational", true},
		{"integer to number input", `{"type":"integer"}`, `{"type":"number"}`, "informational", false},
		{"number to integer input", `{"type":"number"}`, `{"type":"integer"}`, "breaking", false},
		{"input enum narrowed", `{"enum":["a","b"]}`, `{"enum":["a"]}`, "breaking", false},
		{"input enum widened", `{"enum":["a"]}`, `{"enum":["a","b"]}`, "informational", false},
		{"input enum introduced", `{}`, `{"enum":["a"]}`, "breaking", false},
		{"input enum removed", `{"enum":["a"]}`, `{}`, "informational", false},
		{"output enum widened", `{"enum":["a"]}`, `{"enum":["a","b"]}`, "breaking", true},
		{"output enum removed", `{"enum":["a"]}`, `{}`, "breaking", true},
		{"open input closed", `{}`, `{"additionalProperties":false}`, "breaking", false},
		{"closed input opened", `{"additionalProperties":false}`, `{}`, "informational", false},
		{"closed output opened", `{"additionalProperties":false}`, `{}`, "breaking", true},
		{"input property removed", `{"properties":{"id":{"type":"string"}}}`, `{}`, "breaking", false},
		{"output property removed", `{"properties":{"id":{"type":"string"}}}`, `{}`, "breaking", true},
		{"new property constrains open input", `{}`, `{"properties":{"id":{"type":"string"}}}`, "needs_review", false},
		{"optional property expands closed input", `{"additionalProperties":false}`, `{"additionalProperties":false,"properties":{"id":{"type":"string"}}}`, "informational", false},
		{"new output property", `{}`, `{"properties":{"id":{"type":"string"}}}`, "needs_review", true},
		{"nested input narrowed", `{"properties":{"id":{"type":"string"}}}`, `{"properties":{"id":{"type":"integer"}}}`, "breaking", false},
		{"array output widened", `{"items":{"type":"string"}}`, `{"items":{"type":["string","integer"]}}`, "breaking", true},
		{"numeric constraints need review", `{"minimum":1}`, `{"minimum":2}`, "needs_review", false},
		{"combinators need review", `{"oneOf":[{"type":"string"}]}`, `{"oneOf":[{"type":"integer"}]}`, "needs_review", false},
		{"changed referenced definition", `{"$ref":"#/$defs/item","$defs":{"item":{"type":"string"}}}`, `{"$ref":"#/$defs/item","$defs":{"item":{"type":"integer"}}}`, "needs_review", false},
		{"reference not fetched", `{"$ref":"https://invalid.example/a"}`, `{"$ref":"https://invalid.example/b"}`, "needs_review", false},
		{"schema prose only", `{"description":"old"}`, `{"description":"new"}`, "informational", false},
		{"null keyword not mistaken for absent", `{}`, `{"enum":null}`, "needs_review", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := NewContract(ProtocolVersion, []Tool{contractTool(t, tc.before, tc.output)})
			if err != nil {
				t.Fatal(err)
			}
			a, err := NewContract(ProtocolVersion, []Tool{contractTool(t, tc.after, tc.output)})
			if err != nil {
				t.Fatal(err)
			}
			d, err := CompareContracts(b, a)
			if err != nil || len(d.Changes) != 1 || d.Changes[0].Severity != tc.severity {
				t.Fatalf("diff=%+v err=%v", d, err)
			}
			if d.Compatible != (tc.severity == "informational") || d.Breaking != (tc.severity == "breaking") || d.NeedsReview != (tc.severity == "needs_review") {
				t.Fatalf("incorrect gate flags: %+v", d)
			}
		})
	}
}

func TestMCPContractReorderingAndMetadata(t *testing.T) {
	b, _ := NewContract(ProtocolVersion, []Tool{contractTool(t, `{"required":["a","b"],"enum":[{"b":2,"a":1},"x"],"type":["object","null"]}`, false)})
	a, _ := NewContract(ProtocolVersion, []Tool{contractTool(t, `{"type":["null","object"],"enum":["x",{"a":1,"b":2}],"required":["b","a"]}`, false)})
	d, err := CompareContracts(b, a)
	if err != nil || !d.Compatible || len(d.Changes) != 0 {
		t.Fatalf("ordering noise: %+v %v", d, err)
	}
	a.Tools[0].Annotations = json.RawMessage(`{"readOnlyHint":true}`)
	a.Tools[0].Description = "New description"
	d, err = CompareContracts(b, a)
	if err != nil || !d.NeedsReview || d.Breaking || len(d.Changes) != 2 {
		t.Fatalf("metadata=%+v err=%v", d, err)
	}
}

func TestMCPContractToolAndOutputChanges(t *testing.T) {
	tool := contractTool(t, `{"type":"object"}`, false)
	b, _ := NewContract(ProtocolVersion, []Tool{tool})
	a, _ := NewContract(ProtocolVersion, []Tool{})
	d, err := CompareContracts(b, a)
	if err != nil || !d.Breaking || d.Changes[0].Kind != "tool_removed" {
		t.Fatalf("removed=%+v %v", d, err)
	}
	d, err = CompareContracts(a, b)
	if err != nil || !d.Compatible || d.Changes[0].Kind != "tool_added" {
		t.Fatalf("added=%+v %v", d, err)
	}
	a, _ = NewContract(ProtocolVersion, []Tool{tool})
	a.Tools[0].OutputSchema = json.RawMessage(`{"type":"object"}`)
	d, err = CompareContracts(b, a)
	if err != nil || !d.Compatible || d.Changes[0].Kind != "output_schema_added" {
		t.Fatalf("output added=%+v %v", d, err)
	}
	d, err = CompareContracts(a, b)
	if err != nil || !d.Breaking || d.Changes[0].Kind != "output_schema_removed" {
		t.Fatalf("output removed=%+v %v", d, err)
	}
	a.ProtocolVersion = LegacyProtocolVersion
	d, err = CompareContracts(b, a)
	if err != nil || !d.NeedsReview {
		t.Fatalf("protocol=%+v %v", d, err)
	}
}

func TestMCPContractStrictCatalog(t *testing.T) {
	tool := contractTool(t, `{"type":"object"}`, false)
	added := tool
	added.Name = "new_sensitive_tool"
	metadata := tool
	metadata.Description = "Updated description"
	narrowed := contractTool(t, `{"type":"object","required":["id"]}`, false)
	for _, tc := range []struct {
		name          string
		before, after []Tool
		strict        bool
		severity      string
	}{
		{"default additions stay informational", []Tool{tool}, []Tool{tool, added}, false, "informational"},
		{"new visibility needs review", []Tool{tool}, []Tool{tool, added}, true, "needs_review"},
		{"empty catalog gains visibility", []Tool{}, []Tool{added}, true, "needs_review"},
		{"unchanged strict catalog", []Tool{tool}, []Tool{tool}, true, ""},
		{"metadata stays informational", []Tool{tool}, []Tool{metadata}, true, "informational"},
		{"removal stays breaking", []Tool{tool}, []Tool{}, true, "breaking"},
		{"narrowed input stays breaking", []Tool{tool}, []Tool{narrowed}, true, "breaking"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := NewContract(ProtocolVersion, tc.before)
			after, _ := NewContract(ProtocolVersion, tc.after)
			diff, err := CompareContractsWithOptions(before, after, ContractDiffOptions{StrictCatalog: tc.strict})
			if err != nil || diff.StrictCatalog != tc.strict || diff.Breaking != (tc.severity == "breaking") || diff.NeedsReview != (tc.severity == "needs_review") || diff.Compatible != (tc.severity == "" || tc.severity == "informational") {
				t.Fatalf("diff=%+v err=%v", diff, err)
			}
			if tc.severity == "" {
				if len(diff.Changes) != 0 {
					t.Fatalf("unchanged catalog=%+v", diff)
				}
			} else if len(diff.Changes) != 1 || diff.Changes[0].Severity != tc.severity {
				t.Fatalf("changes=%+v", diff)
			}
			data, _ := json.Marshal(diff)
			if bytes.Contains(data, []byte(`"strict_catalog"`)) != tc.strict {
				t.Fatalf("receipt policy=%s", data)
			}
		})
	}
}

func TestMCPContractCanonicalSnapshotPreservesNumbers(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"z","inputSchema":{"minimum":9007199254740993},"outputSchema":{"type":"object","properties":{"n":{"const":9007199254740993}}}},{"name":"a","inputSchema":{"type":"object"}}]}}`)
	})
	tools, _, err := c.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	contract, err := NewContract(ProtocolVersion, tools)
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("9007199254740993")) || bytes.Contains(data, []byte("9007199254740992")) || bytes.Contains(data, []byte(c.Endpoint)) {
		t.Fatalf("lossy snapshot: %s", data)
	}
	parsed, err := ReadContract(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	again, err := MarshalContract(parsed)
	if err != nil || !bytes.Equal(data, again) || parsed.Tools[0].Name != "a" {
		t.Fatalf("unstable roundtrip: %s %v", again, err)
	}
	d, err := CompareContracts(contract, parsed)
	if err != nil || len(d.Changes) != 0 {
		t.Fatalf("roundtrip diff=%+v %v", d, err)
	}
	if tools[0].Name != "z" {
		t.Fatal("snapshot reordered caller's slice")
	}
}

func TestMCPContractRejectsMalformedSnapshots(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"version":3,"protocol_version":"2026-07-28","tools":[]}`,
		`{"version":1,"protocol_version":"2026-07-28","tools":null}`,
		`{"version":1,"protocol_version":"2026-07-28","tools":[],"token":"do-not-store"}`,
		`{"version":1,"protocol_version":"2026-07-28","tools":[]} {}`,
		`{"version":1,"protocol_version":"2026-07-28","tools":[{"name":"x","inputSchema":null}]}`,
		`{"version":1,"protocol_version":"2026-07-28","tools":[{"name":"x","inputSchema":{}},{"name":"x","inputSchema":{}}]}`,
		`{"version":1,"protocol_version":"2026-07-28","tools":[{"name":"x","inputSchema":{},"outputSchema":null}]}`,
		strings.Repeat(" ", maxResponseBytes+1),
	} {
		if _, err := ReadContract(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted invalid snapshot: %.120s", body)
		}
	}
	invalid := Contract{Version: 3, ProtocolVersion: ProtocolVersion, Tools: []Tool{}}
	if _, err := MarshalContract(invalid); err == nil {
		t.Fatal("marshal accepted unknown version")
	}
}

func TestMCPContractEscapesChangePaths(t *testing.T) {
	b, _ := NewContract(ProtocolVersion, []Tool{contractTool(t, `{"properties":{"a/b~c":{"type":"string"}}}`, false)})
	a, _ := NewContract(ProtocolVersion, []Tool{contractTool(t, `{"properties":{"a/b~c":{"type":"integer"}}}`, false)})
	d, err := CompareContracts(b, a)
	if err != nil || len(d.Changes) != 1 || d.Changes[0].Path != "/inputSchema/properties/a~1b~0c/type" {
		t.Fatalf("path=%+v %v", d, err)
	}
}

func TestMCPContractDeepChangesRequireReview(t *testing.T) {
	before, after := `{"type":"string"}`, `{"type":"integer"}`
	for i := 0; i < 70; i++ {
		before = `{"properties":{"child":` + before + `}}`
		after = `{"properties":{"child":` + after + `}}`
	}
	b, _ := NewContract(ProtocolVersion, []Tool{contractTool(t, before, false)})
	a, _ := NewContract(ProtocolVersion, []Tool{contractTool(t, after, false)})
	d, err := CompareContracts(b, a)
	if err != nil || !d.NeedsReview || d.Compatible || len(d.Changes) != 1 || d.Changes[0].Kind != "schema_depth_requires_review" {
		t.Fatalf("deep diff=%+v err=%v", d, err)
	}
}

func TestMCPResourceAndPromptContractChanges(t *testing.T) {
	baseline, err := NewCatalogContract(ProtocolVersion, Catalog{
		Resources:         []Resource{{URI: "file:///report", Name: "report", Description: "Current report"}},
		ResourceTemplates: []ResourceTemplate{{URITemplate: "file:///users/{id}", Name: "user"}},
		Prompts:           []Prompt{{Name: "summarize", Arguments: []PromptArgument{{Name: "period", Description: "Window"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Version != 2 {
		t.Fatalf("catalog snapshot format version=%d, want 2", baseline.Version)
	}
	changed, err := NewCatalogContract(ProtocolVersion, Catalog{
		Resources:         []Resource{{URI: "file:///report", Name: "report", Description: "Latest report"}},
		ResourceTemplates: []ResourceTemplate{{URITemplate: "file:///users/{id}", Name: "user"}},
		Prompts:           []Prompt{{Name: "summarize", Arguments: []PromptArgument{{Name: "period", Description: "Window", Required: true}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := CompareContracts(baseline, changed)
	if err != nil || !diff.Breaking || diff.Compatible || len(diff.Changes) != 2 {
		t.Fatalf("diff=%+v err=%v", diff, err)
	}
	if diff.Changes[0].Kind != "resource_metadata_changed" || diff.Changes[1].Kind != "prompt_argument_required" || diff.Changes[1].Severity != "breaking" {
		t.Fatalf("unexpected catalog findings: %+v", diff.Changes)
	}

	withoutResource, _ := NewCatalogContract(ProtocolVersion, Catalog{Resources: []Resource{}, ResourceTemplates: baseline.ResourceTemplates, Prompts: baseline.Prompts})
	diff, err = CompareContracts(baseline, withoutResource)
	if err != nil || !diff.Breaking || diff.Changes[0].Kind != "resource_removed" {
		t.Fatalf("resource removal diff=%+v err=%v", diff, err)
	}
	strictAdded, _ := NewCatalogContract(ProtocolVersion, Catalog{Resources: []Resource{{URI: "file:///new", Name: "new"}}})
	strict, err := CompareContractsWithOptions(baseline, strictAdded, ContractDiffOptions{StrictCatalog: true})
	if err != nil || !strict.NeedsReview || !strict.StrictCatalog {
		t.Fatalf("strict catalog diff=%+v err=%v", strict, err)
	}

	body, err := MarshalContract(baseline)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ReadContract(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if !contractEqual(parsed.Resources, baseline.Resources) || !contractEqual(parsed.ResourceTemplates, baseline.ResourceTemplates) || !contractEqual(parsed.Prompts, baseline.Prompts) {
		t.Fatalf("catalog metadata changed in round trip: %+v", parsed)
	}
	legacy, err := ReadContract(strings.NewReader(`{"version":1,"protocol_version":"2026-07-28","tools":[]}`))
	if err != nil || legacy.Version != 1 {
		t.Fatalf("legacy tool-only lock is not readable: %+v %v", legacy, err)
	}
	if _, err := ReadContract(strings.NewReader(`{"version":1,"protocol_version":"2026-07-28","tools":[],"resources":[{"uri":"x","name":"x"}]}`)); err == nil {
		t.Fatal("format 1 accepted an unsupported catalog")
	}
	emptyResources, _ := NewCatalogContract(ProtocolVersion, Catalog{Capabilities: []string{"resources"}})
	noResources, _ := NewCatalogContract(ProtocolVersion, Catalog{Capabilities: []string{"tools"}})
	diff, err = CompareContracts(emptyResources, noResources)
	if err != nil || !diff.Breaking || len(diff.Changes) != 2 || diff.Changes[0].Kind != "capability_removed" || diff.Changes[0].Capability != "resources" {
		t.Fatalf("empty advertised capability removal was lost: %+v err=%v", diff, err)
	}
}

func TestTaskExtensionIsCapturedAndRemovalIsBreaking(t *testing.T) {
	withTasks, err := NewCatalogContract(ProtocolVersion, Catalog{
		Capabilities: []string{"tools"},
		Extensions:   []string{TasksExtensionID},
		Tools:        []Tool{},
	})
	if err != nil {
		t.Fatal(err)
	}
	withoutTasks, err := NewCatalogContract(ProtocolVersion, Catalog{
		Capabilities: []string{"tools"},
		Tools:        []Tool{},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalContract(withTasks)
	if err != nil {
		t.Fatalf("marshal task extension snapshot: %v", err)
	}
	parsed, err := ReadContract(bytes.NewReader(encoded))
	if err != nil || !contractEqual(parsed.Extensions, []string{TasksExtensionID}) {
		t.Fatalf("extension did not round trip: %+v err=%v", parsed, err)
	}
	diff, err := CompareContracts(withTasks, withoutTasks)
	if err != nil || !diff.Breaking || len(diff.Changes) != 1 || diff.Changes[0].Kind != "capability_removed" || diff.Changes[0].Capability != "extension:"+TasksExtensionID {
		t.Fatalf("extension removal diff=%+v err=%v", diff, err)
	}
}

func TestCompletionsCapabilityIsCapturedAndRemovalIsBreaking(t *testing.T) {
	withCompletions, err := NewCatalogContract(ProtocolVersion, Catalog{
		Capabilities: []string{"tools", "completions"},
		Tools:        []Tool{},
	})
	if err != nil {
		t.Fatal(err)
	}
	withoutCompletions, err := NewCatalogContract(ProtocolVersion, Catalog{
		Capabilities: []string{"tools"},
		Tools:        []Tool{},
	})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := CompareContracts(withCompletions, withoutCompletions)
	if err != nil || !diff.Breaking || len(diff.Changes) != 1 || diff.Changes[0].Capability != "completions" {
		t.Fatalf("completion capability removal diff=%+v err=%v", diff, err)
	}
}

package runtask

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBindingsPreserveWireNamesAndOptionalFields(t *testing.T) {
	task := api.PublishExecutionTaskRequest{
		Name: "customer-report", Version: 3,
		Definition: api.CreateExecutionRequest{
			InputSchema: json.RawMessage(`{
				"type":"object","additionalProperties":false,"required":["page-count"],
				"properties":{"page-count":{"type":"integer"},"notes":{"type":"array","items":{"type":["string","null"]}}}
			}`),
			ResultSchema: json.RawMessage(`{"type":"object","additionalProperties":{"type":"boolean"}}`),
		},
	}
	for _, tc := range []struct {
		language string
		contains []string
	}{
		{"typescript", []string{`"page-count": number;`, `"notes"?: Array<string | null>;`, "Record<string, boolean>", `("customer-report", 3, decodeResult)`}},
		{"python", []string{`"page-count": int,`, `"notes": typing.NotRequired[list[str | None]],`, "dict[str, bool]", `("customer-report", 3, decode_result)`}},
	} {
		t.Run(tc.language, func(t *testing.T) {
			first, err := GenerateBindings(task, tc.language)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(string(first), want) {
					t.Errorf("missing %q in %s", want, first)
				}
			}
			reordered := task
			reordered.Definition.InputSchema = json.RawMessage(`{
				"properties":{"notes":{"type":"array","items":{"type":["string","null"]}},"page-count":{"type":"integer"}},
				"required":["page-count"],"additionalProperties":false,"type":"object"
			}`)
			second, err := GenerateBindings(reordered, tc.language)
			if err != nil || !bytes.Equal(first, second) {
				t.Fatalf("reordered schema changed generated source: %s, %v", second, err)
			}
		})
	}
}

func TestBindingsRefuseUnsupportedShapesWithoutInvalidatingManifest(t *testing.T) {
	task := api.PublishExecutionTaskRequest{
		Name: "report", Version: 1,
		Definition: api.CreateExecutionRequest{
			Runtime: api.ExecutionRuntimeNode24, Source: "export default () => 3",
			ResultSchema: json.RawMessage(`{"$defs":{"count":{"type":"integer"}},"$ref":"#/$defs/count"}`),
		},
	}
	if _, _, err := Normalize(task, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if source, err := GenerateBindings(task, "typescript"); err == nil ||
		!strings.Contains(err.Error(), "result_schema") || len(source) > 0 {
		t.Fatalf("unsupported shape generated ambiguous source: %s %v", source, err)
	}
}

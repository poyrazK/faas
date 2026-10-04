// adr: 531
package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemTrafficRuleSchemaProjectionMatchesJSONEncoder(t *testing.T) {
	for _, raw := range []string{`""`, `"plain"`, `"<>&"`, "\"é\u2028\u2029\"", `"escaped\"quote"`, `{"number":1e130000}`, `"invalid`, "\"control\x01\"", "\"control\x00\"", "\"control\x1f\"", "\"allowed\x7f\"", `"` + strings.Repeat("<&>", 10000) + `"`} {
		t.Run(raw[:min(len(raw), 24)], func(t *testing.T) {
			original := EdgeRule{Action: EdgeRuleAction{Kind: EdgeRuleKindValidate, Validate: &EdgeRuleValidateAction{Schema: json.RawMessage(raw)}}}
			projected, allowance := compactMemTrafficRuleSchema(original)
			before, beforeErr := json.Marshal(original)
			after, afterErr := json.Marshal(projected)
			if (beforeErr != nil) != (afterErr != nil) {
				t.Fatalf("invalid schema changed encoder verdict: %v/%v", beforeErr, afterErr)
			}
			if beforeErr == nil && int64(len(after))+allowance != int64(len(before)) {
				t.Fatalf("compiled size differs from encoder: projected=%d allowance=%d actual=%d", len(after), allowance, len(before))
			}
			canonicalBefore, err := memTrafficProjectionSize("reference", original)
			if err != nil && beforeErr == nil {
				t.Fatal(err)
			}
			canonicalAfter, afterSizeErr := memTrafficProjectionSize("projection", projected)
			if (err != nil) != (afterSizeErr != nil) || err == nil && canonicalAfter+allowance != canonicalBefore {
				t.Fatalf("canonical size differs from encoder: %d+%d/%v, actual=%d/%v", canonicalAfter, allowance, afterSizeErr, canonicalBefore, err)
			}
			if string(original.Action.Validate.Schema) != raw {
				t.Fatal("measuring a schema changed stored intent")
			}
		})
	}
}

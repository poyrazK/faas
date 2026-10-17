package behaviorcompare

import (
	"encoding/json"
	"strings"
	"testing"
)

func evidence(t *testing.T, values string) *Evidence {
	t.Helper()
	specs := []Spec{{Key: "export.response", Kind: "http"}, {Key: "notification.successes", Kind: "delivery"}}
	observations, err := Decode([]byte(values), specs)
	if err != nil {
		t.Fatal(err)
	}
	return &Evidence{Version: 1, Status: "complete", Provenance: Provenance,
		ContractSHA256: strings.Repeat("a", 64), FixtureSHA256: strings.Repeat("b", 64), Specs: specs, Observations: observations}
}

func TestCompareFindsDuplicateNotificationWithUnchangedResponse(t *testing.T) {
	before := evidence(t, `{"version":1,"observations":[{"key":"export.response","value":{"status":202,"created":true}},{"key":"notification.successes","value":1}]}`)
	after := evidence(t, `{"version":1,"observations":[{"key":"notification.successes","value":2},{"key":"export.response","value":{"created":true,"status":202}}]}`)
	result := Compare(before, after)
	if result.Status != "failed" || len(result.Changes) != 2 || result.Changes[0].Status != "unchanged" || result.Changes[1].Status != "changed" || string(result.Changes[1].Before) != "1" || string(result.Changes[1].After) != "2" {
		t.Fatalf("comparison = %+v", result)
	}
}

func TestJSONComparisonPreservesMeaningAndPrecision(t *testing.T) {
	for _, tc := range []struct {
		name, before, after, status string
	}{
		{"object ordering and number spelling", `{"b":2,"a":[1,1]}`, `{"a":[1.0,1e0],"b":2e0}`, "passed"},
		{"large integer precision", `9007199254740992`, `9007199254740993`, "failed"},
		{"array multiplicity", `[1,1]`, `[1]`, "failed"},
		{"array order", `[1,2]`, `[2,1]`, "failed"},
		{"null differs from empty object", `null`, `{}`, "failed"},
		{"string differs from number", `"1"`, `1`, "failed"},
		{"negative zero", `-0.00`, `0`, "passed"},
		{"decimal precision", `0.10000000000000000001`, `0.1`, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrap := func(value string) *Evidence {
				return evidence(t, `{"version":1,"observations":[{"key":"export.response","value":`+value+`},{"key":"notification.successes","value":1}]}`)
			}
			if got := Compare(wrap(tc.before), wrap(tc.after)); got.Status != tc.status {
				t.Fatalf("comparison = %+v", got)
			}
		})
	}
}

func TestDecodeRejectsAmbiguousIncompleteAndOversizedEvidence(t *testing.T) {
	specs := []Spec{{Key: "export.response", Kind: "http"}}
	for _, body := range []string{
		`{"version":2,"observations":[{"key":"export.response","value":1}]}`,
		`{"version":1,"observations":[]}`,
		`{"version":1,"observations":[{"key":"other","value":1}]}`,
		`{"version":1,"observations":[{"key":"export.response"}]}`,
		`{"version":1,"observations":[{"key":"export.response","value":1,"provenance":"platform"}]}`,
		`{"version":1,"observations":[{"key":"export.response","value":{"a":1,"a":2}}]}`,
		`{"version":1,"observations":[{"key":"export.response","value":{"a":1,"\u0061":2}}]}`,
		`{"version":1,"observations":[{"key":"export.response","value":1}],"version":2}`,
		`{"version":1,"observations":[{"key":"export.response","value":1},{"key":"export.response","value":2}]}`,
		`{"version":1,"observations":[{"key":"export.response","value":1}]} {}`,
		`{"version":1,"observations":[{"key":"export.response","value":1}],"private":"do-not-print"}`,
		strings.Repeat(" ", MaxDocumentBytes+1),
		`{"version":1,"observations":[{"key":"export.response","value":` + strings.Repeat("[", 66) + `1` + strings.Repeat("]", 66) + `}]}`,
	} {
		if _, err := Decode([]byte(body), specs); err == nil || strings.Contains(err.Error(), "do-not-print") {
			t.Fatalf("invalid evidence was accepted or echoed: %v", err)
		}
	}
	if _, err := Decode([]byte(`{"version":1,"observations":[{"key":"export.response","value":null}]}`), specs); err != nil {
		t.Fatalf("explicit null must remain observable: %v", err)
	}
}

func TestCompareRequiresCompleteMatchingEvidence(t *testing.T) {
	const document = `{"version":1,"observations":[{"key":"export.response","value":{}},{"key":"notification.successes","value":1}]}`
	for _, tc := range []struct {
		name   string
		mutate func(*Evidence)
	}{
		{"fixture changed", func(e *Evidence) { e.FixtureSHA256 = strings.Repeat("c", 64) }},
		{"observer changed", func(e *Evidence) { e.ContractSHA256 = strings.Repeat("d", 64) }},
		{"invalid digest", func(e *Evidence) { e.ContractSHA256 = "unknown" }},
		{"incomplete", func(e *Evidence) { e.Status = "incomplete" }},
		{"contradictory error", func(e *Evidence) { e.Error = "collection failed" }},
		{"missing observation", func(e *Evidence) { e.Observations = e.Observations[:1] }},
		{"changed kind", func(e *Evidence) { e.Specs[0].Kind = "application" }},
		{"invented provenance", func(e *Evidence) { e.Provenance = "platform_verified" }},
		{"future version", func(e *Evidence) { e.Version = 2 }},
		{"duplicate spec", func(e *Evidence) { e.Specs[1] = e.Specs[0] }},
		{"malformed raw value", func(e *Evidence) { e.Observations[0].Value = json.RawMessage(`{"bad":`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := evidence(t, document), evidence(t, document)
			tc.mutate(after)
			if got := Compare(before, after); got.Status != "inconclusive" || len(got.Changes) != 0 {
				t.Fatalf("comparison = %+v", got)
			}
		})
	}
	if Compare(nil, nil) != nil || Compare(evidence(t, document), nil).Status != "inconclusive" {
		t.Fatal("legacy or missing behavior handling changed")
	}
}

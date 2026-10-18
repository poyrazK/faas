// adr: 1661
package evaluation

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCorpusRejectsAmbiguousOrUnboundedJSON(t *testing.T) {
	for _, raw := range []string{
		`[{"id":"a","id":"b"}]`,
		`[{"id":"a","event":{"message":"a","message":"b"}}]`,
		`[{"event":{"unsupported":"private-value"}}]`,
		`[] []`, `{"cases":[]}`, `[`,
		strings.Repeat("[", api.IssueGroupingEvaluationJSONDepthMax+1) + strings.Repeat("]", api.IssueGroupingEvaluationJSONDepthMax+1),
		string([]byte{'[', '"', 0xff, '"', ']'}),
	} {
		if _, err := DecodeCorpus([]byte(raw)); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("unsafe corpus accepted or echoed: %v", err)
		}
	}
	if _, err := DecodeCorpus(bytes.Repeat([]byte(" "), MaxCorpusBytes+1)); err == nil {
		t.Fatal("oversized corpus accepted")
	}
	for _, id := range []string{"", strings.Repeat("x", api.IssueGroupingEvaluationCaseIDMaxBytes+1), "two words", "control\n", string([]byte{0xff})} {
		cases := testCases()
		cases[0].ID = id
		if _, err := Snapshot(cases, "legacy", time.Now().UTC()); err == nil {
			t.Fatal("unbounded or nonprintable case id accepted")
		}
	}
	if _, err := Snapshot(make([]Case, MaxCases+1), "legacy", time.Now().UTC()); err == nil {
		t.Fatal("excess cases accepted")
	}
}

func TestBaselineStrictDecodeRoundTrip(t *testing.T) {
	before, err := Snapshot(testCases(), "cause-chain-v1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	after, err := DecodeBaseline(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compare(testCases(), after, "cause-chain-v1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{"version":1,"version":2}`, `{"unknown":"private-value"}`, `{} {}`,
		`{"assignments":[{"decision":{"basis":"fallback","basis":"override"}}]}`,
	} {
		if _, err := DecodeBaseline([]byte(invalid)); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("unsafe baseline JSON accepted or echoed: %v", err)
		}
	}
	if _, err := DecodeBaseline(bytes.Repeat([]byte(" "), api.IssueGroupingEvaluationBaselineMaxBytes+1)); err == nil {
		t.Fatal("oversized baseline accepted")
	}
}

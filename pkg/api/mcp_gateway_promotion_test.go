package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPGatewayPromotionExpectation(t *testing.T) {
	for _, revision := range []int64{-1, 0, 42} {
		expected := MCPAccessPolicyExpectation{Resource: "https://reports.example/mcp", Fingerprint: strings.Repeat("a", 64), AccountRevision: &revision}
		raw, err := json.Marshal(expected)
		if err != nil {
			t.Fatal(err)
		}
		var decoded MCPAccessPolicyExpectation
		if err = json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.AccountRevision == nil || *decoded.AccountRevision != revision {
			t.Fatal("revision not preserved")
		}
		if (decoded.Validate() == nil) != (revision > 0) {
			t.Fatalf("unexpected validation for revision %d", revision)
		}
	}
	for _, code := range []string{CodeMCPAdmissionEvidenceUnavailable, CodeMCPAccessPolicyChanged, CodeMCPAccountRevisionChanged, CodeMCPGatewayEvidenceUnavailable, CodeMCPGatewayEvidenceStale, CodeMCPGatewayReplayPending} {
		if !IsMCPPromotionConditionFailure(code) {
			t.Fatalf("condition rejection %s could be accepted as a lost response", code)
		}
	}
	if IsMCPPromotionConditionFailure(CodeTrafficServingChanged) {
		t.Fatal("ordinary serving CAS lost response behavior changed")
	}
}

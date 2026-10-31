package api

import (
	"net/http"
	"strings"
	"testing"
)

// adr: 20261022105846 — defaults, bounds and the kinds that may box.
func TestEdgeRulePenaltyValidate(t *testing.T) {
	p := EdgeRulePenalty{Seconds: 600}
	if prob := p.Validate(); prob != nil || p.After != EdgePenaltyDefaultAfter || p.WithinSeconds != EdgePenaltyDefaultWithinSeconds {
		t.Fatalf("defaults: %+v, %v", p, prob)
	}
	for _, bad := range []EdgeRulePenalty{
		{Seconds: 59}, {Seconds: MaxEdgePenaltySeconds + 1},
		{Seconds: 600, After: MaxEdgePenaltyAfter + 1}, {Seconds: 600, After: -1},
		{Seconds: 600, WithinSeconds: MaxEdgePenaltyWithinSeconds + 1},
	} {
		if prob := bad.Validate(); prob == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	for kind, mode := range map[string]string{"ip": "", "geo": "", "throttle": "", "waf": EdgeWAFModeBlock} {
		if prob := ValidateEdgeRulePenaltyKind(kind, mode); prob != nil {
			t.Errorf("%s/%s refused: %s", kind, mode, prob.Detail)
		}
	}
	for kind, mode := range map[string]string{"challenge": "", "route": "", "cors": "", "waf": EdgeWAFModeWarn} {
		if prob := ValidateEdgeRulePenaltyKind(kind, mode); prob == nil {
			t.Errorf("%s/%s accepted", kind, mode)
		}
	}
	prob := ErrPenaltyBox("r1", 42)
	if prob.Status != http.StatusForbidden || prob.Code != CodePenaltyBox || !strings.Contains(prob.Detail, "42 more seconds") {
		t.Fatalf("problem = %+v", prob)
	}
}

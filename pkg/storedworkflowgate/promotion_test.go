// adr: 1302
package storedworkflowgate_test

import (
	"github.com/onebox-faas/faas/pkg/storedworkflowgate"
	"testing"
	"time"
)

func TestBaselinePromotionPolicyKeepsGateStrict(t *testing.T) {
	s, v := gateFixture()
	s.BaselineID = s.CandidateID
	v.Baseline = v.Candidate
	for i := range v.Cases {
		v.Cases[i].Baseline = v.Cases[i].Candidate
		v.Cases[i].Change = "unchanged"
	}
	recount(&v)
	if _, err := storedworkflowgate.Evaluate(s, v); err == nil {
		t.Fatal("gate accepted self-comparison")
	}
	r, err := storedworkflowgate.EvaluatePromotionAt(s, v, time.Now())
	if err != nil || r.ExitCode != 0 {
		t.Fatal("passing promotion rejected", err, r)
	}
}

// adr: 1302
package automationgate_test

import (
	"github.com/onebox-faas/faas/pkg/automationgate"
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
	if _, err := automationgate.Evaluate(s, v); err == nil {
		t.Fatal("gate accepted self-comparison")
	}
	r, err := automationgate.EvaluatePromotionAt(s, v, time.Now())
	if err != nil || r.ExitCode != 0 {
		t.Fatal("passing promotion rejected", err, r)
	}
}

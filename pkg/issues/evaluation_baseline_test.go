// adr: 1661
package issues_test

import (
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/issues/evaluation"
)

// Keep the reviewed corpus gate in the normalizer's suite as well: light CI
// tests changed packages, so a grouping-only edit must run this regression check.
func TestIssueReviewedGroupingBaseline(t *testing.T) {
	raw, err := os.ReadFile("evaluation/testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := evaluation.DecodeCorpus(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("evaluation/testdata/cause-chain-v1.baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := evaluation.DecodeBaseline(raw)
	if err != nil {
		t.Fatal(err)
	}
	report, err := evaluation.Compare(cases, baseline, "cause-chain-v1", time.Now().UTC())
	if err != nil || report.Regressed {
		t.Fatalf("grouping baseline regressed; inspect make issues-grouping-check: %+v %v", report, err)
	}
}

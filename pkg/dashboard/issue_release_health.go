package dashboard

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

type IssueReleaseHealthRow struct {
	Label  string
	Health api.IssueReleaseHealth
	Rate   string
}
type IssueReleaseComparisonView struct {
	Comparison api.IssueReleaseComparison
	Rows       []IssueReleaseHealthRow
	Delta      string
}

func IssueReleaseComparisonPresentation(in api.IssueReleaseComparison) *IssueReleaseComparisonView {
	out := &IssueReleaseComparisonView{Comparison: in, Delta: "Unknown"}
	for i, h := range []api.IssueReleaseHealth{in.Baseline, in.Candidate} {
		label := "Baseline"
		if i == 1 {
			label = "Candidate"
		}
		rate := "Unknown"
		if h.HTTP5xxRate != nil {
			rate = fmt.Sprintf("%.2f%%", 100**h.HTTP5xxRate)
		}
		out.Rows = append(out.Rows, IssueReleaseHealthRow{Label: label, Health: h, Rate: rate})
	}
	if in.HTTP5xxRateDelta != nil {
		out.Delta = fmt.Sprintf("%+.2f percentage points", 100**in.HTTP5xxRateDelta)
	}
	return out
}

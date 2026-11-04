package dashboard

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/api"
	"html/template"
	"strings"
	"testing"
)

func TestIssueReleaseHealthUnknownAndEscapedPresentation(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	in := api.IssueReleaseComparison{Candidate: api.IssueReleaseHealth{CommitSHA: "<script>attack</script>"}}
	view := IssueReleaseComparisonPresentation(in)
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "issue-release-health", view); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Unknown") || strings.Contains(b.String(), "<script>") || !strings.Contains(b.String(), "&lt;script&gt;") {
		t.Fatal("unknown coverage or escaping lost")
	}
	rate, delta := .25, -.05
	in.Candidate.HTTP5xxRate = &rate
	in.HTTP5xxRateDelta = &delta
	view = IssueReleaseComparisonPresentation(in)
	if view.Rows[1].Rate != "25.00%" || view.Delta != "-5.00 percentage points" {
		t.Fatalf("incorrect units: %+v", view)
	}
}

func TestIssueReleaseHealthShowsLegacyWeightsAsLowerBounds(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	view := IssueReleaseComparisonPresentation(api.IssueReleaseComparison{
		Baseline:  api.IssueReleaseHealth{ObservedIssueEvents: 5, UnknownHTTPBatches: 1},
		Candidate: api.IssueReleaseHealth{ObservedIssueEvents: 7},
	})
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "issue-release-health", view); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "≥5") || strings.Contains(b.String(), "≥7") {
		t.Fatalf("legacy and exact event totals are not distinguished: %s", b.String())
	}
}

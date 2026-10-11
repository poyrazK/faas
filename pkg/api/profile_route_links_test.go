package api

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func TestProfileRouteComparisonURL(t *testing.T) {
	now := time.Now().UTC()
	a := ProfileQuery{DeploymentID: "baseline", Runtime: "node24", Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)}
	b := ProfileQuery{DeploymentID: "candidate", Runtime: "node24", Start: now.Add(-time.Hour), End: now}
	check := ProfileRouteRegression{Route: "POST /checkout", CodeEvidence: []ProfileRegressionEvidence{{Kind: "call_path", Frames: []ProfileCallPathFrame{{Name: "all"}, {Name: "checkout", File: "app.js", CandidateSource: &ProfileSourceLocation{URL: "https://example.com/private"}}}}}}
	u, err := url.Parse(ProfileRouteComparisonURL("demo", a, b, check))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("route") != check.Route || q.Get("baseline_start") != a.Start.Format(time.RFC3339Nano) || q.Get("end") != b.End.Format(time.RFC3339Nano) || u.Fragment != "diff-flamegraph" {
		t.Fatal(u)
	}
	var finding struct {
		Frames []ProfileCallPathFrame `json:"frames"`
	}
	if json.Unmarshal([]byte(q.Get("route_finding")), &finding) != nil || len(finding.Frames) != 2 || finding.Frames[1].CandidateSource != nil {
		t.Fatal(q)
	}
}

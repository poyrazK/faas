// adr: 1301
package automationgate_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/automationgate"
)

func TestEvidenceAgePolicyParsing(t *testing.T) {
	legacy := automationgate.CoveragePolicy{Version: 1, RequiredCases: []string{"b", "a"}}
	if legacy.Hash() != "6558b2620c30d7d8e7eca5fce6fb49129a3ef20735cd67c2a892ced6a48cee22" {
		t.Fatal("version 1 hash changed")
	}

	for _, input := range []string{
		`{"version":1,"required_cases":["a"],"max_candidate_age_seconds":60}`,
		`{"version":1,"required_cases":["a"],"max_candidate_age_seconds":null}`,
		`{"version":2,"required_cases":["a"]}`,
		`{"version":2,"required_cases":["a"],"max_candidate_age_seconds":0}`,
		`{"version":2,"required_cases":["a"],"max_candidate_age_seconds":-1}`,
		`{"version":2,"required_cases":["a"],"max_candidate_age_seconds":1.5}`,
		`{"version":2,"required_cases":["a"],"max_candidate_age_seconds":"60"}`,
		`{"version":2,"required_cases":["a"],"max_candidate_age_seconds":9223372037}`,
		`{"version":2,"required_cases":["a"],"max_candidate_age_seconds":null}`,
		`{"version":2,"required_cases":["a"],"max_candidate_age_seconds":60,"max_candidate_age_seconds":120}`,
	} {
		if _, err := automationgate.ParseCoveragePolicy([]byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	a, err := automationgate.ParseCoveragePolicy([]byte(`{"version":2,"required_cases":["a"],"max_candidate_age_seconds":60}`))
	if err != nil {
		t.Fatal(err)
	}
	b := a
	maxAge := int64(120)
	b.MaxCandidateAgeSeconds = &maxAge
	if a.Hash() == b.Hash() {
		t.Fatal("age limit excluded from policy hash")
	}
}

func TestEvidenceAgeDecisions(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		age    time.Duration
		failed bool
		code   int
		reason string
	}{
		{"fresh", 30 * time.Second, false, 0, ""},
		{"exact boundary", 60 * time.Second, false, 0, ""},
		{"over boundary", 60*time.Second + time.Nanosecond, false, 7, "candidate_evidence_stale"},
		{"future", -time.Nanosecond, false, 7, "candidate_evidence_future"},
		{"stale failure", 61 * time.Second, true, 7, "candidate_evidence_stale"},
		{"fresh failure", 30 * time.Second, true, 1, "candidate_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, v := gateFixture()
			limit := int64(60)
			s.Coverage = &automationgate.CoveragePolicy{Version: 2, RequiredCases: []string{"steady"}, MaxCandidateAgeSeconds: &limit}
			created := now.Add(-tc.age)
			for i := range v.Cases {
				if v.Cases[i].Candidate != nil {
					v.Cases[i].Candidate.CreatedAt = &created
				}
			}
			v.Candidate.CreatedAt = now.Add(-tc.age)
			v.Candidate.UpdatedAt = v.Candidate.CreatedAt
			// A freshly checked/observed suite cannot refresh old execution evidence.
			if tc.age >= 0 {
				v.Candidate.UpdatedAt = now
			}
			v.ObservedAt = now
			if tc.failed {
				v.Cases[0].Candidate.Result = "failed"
				v.Cases[0].Change = "regression"
				recount(&v)
			}
			r, err := automationgate.EvaluateAt(s, v, now)
			if err != nil {
				t.Fatal(err)
			}
			if r.ExitCode != tc.code || (tc.reason != "" && !strings.Contains(strings.Join(r.Reasons, ","), tc.reason)) {
				t.Fatalf("result: %+v", r)
			}
			if r.EvidenceAge == nil || math.Abs(r.EvidenceAge.AgeSeconds-tc.age.Seconds()) > 1e-12 || r.EvidenceAge.MaxAgeSeconds != 60 || !r.EvidenceAge.EvaluatedAt.Equal(now) {
				t.Fatalf("age: %+v", r.EvidenceAge)
			}
		})
	}
	s, v := gateFixture()
	v.Candidate.CreatedAt = now.Add(-24 * time.Hour)
	v.Candidate.UpdatedAt = now
	s.Coverage = &automationgate.CoveragePolicy{Version: 1, RequiredCases: []string{"steady"}}
	r, err := automationgate.EvaluateAt(s, v, now)
	if err != nil || r.ExitCode != 0 || r.EvidenceAge != nil {
		t.Fatalf("v1 changed: %+v %v", r, err)
	}
}

func TestEvidenceAgeUsesOriginalRuns(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for _, mode := range []string{"regrouped-old", "missing", "future-second"} {
		t.Run(mode, func(t *testing.T) {
			s, v := gateFixture()
			limit := int64(60)
			s.Coverage = &automationgate.CoveragePolicy{Version: 2, RequiredCases: []string{"steady"}, MaxCandidateAgeSeconds: &limit}
			// A newly grouped suite and a new response must not reset original run age.
			v.Candidate.CreatedAt = now
			v.Candidate.UpdatedAt = now
			v.ObservedAt = now
			fresh := now.Add(-time.Second)
			for i := range v.Cases {
				v.Cases[i].Candidate.CreatedAt = &fresh
			}
			want := "candidate_evidence_stale"
			switch mode {
			case "regrouped-old":
				old := now.Add(-time.Hour)
				v.Cases[1].Candidate.CreatedAt = &old
			case "missing":
				v.Cases[1].Candidate.CreatedAt = nil
				want = "candidate_evidence_age_unavailable"
			case "future-second":
				future := now.Add(time.Second)
				v.Cases[1].Candidate.CreatedAt = &future
				want = "candidate_evidence_future"
			}
			r, err := automationgate.EvaluateAt(s, v, now)
			if err != nil || r.ExitCode != 7 || !strings.Contains(strings.Join(r.Reasons, ","), want) {
				t.Fatalf("result %+v: %v", r, err)
			}
		})
	}
}

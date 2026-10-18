// adr: 1301
package automationgate

import (
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

// EvidenceAge measures the oldest original candidate verification, so grouping
// or checking old runs cannot refresh evidence. Negative age indicates clock skew.
type EvidenceAge struct {
	EvaluatedAt        time.Time `json:"evaluated_at"`
	OldestRunCreatedAt time.Time `json:"oldest_run_created_at"`
	AgeSeconds         float64   `json:"age_seconds"`
	MaxAgeSeconds      int64     `json:"max_age_seconds"`
}

func evaluateAge(policy *CoveragePolicy, cases []api.AutomationLiveSuiteCaseComparison, now time.Time, result *Result) bool {
	if policy == nil || policy.Version != 2 {
		return false
	}
	var oldest time.Time
	var future, missing bool
	for _, entry := range cases {
		if entry.Candidate == nil {
			continue
		}
		created := entry.Candidate.CreatedAt
		if created == nil || created.IsZero() {
			missing = true
			continue
		}
		if oldest.IsZero() || created.Before(oldest) {
			oldest = *created
		}
		future = future || created.After(now)
	}
	if !oldest.IsZero() {
		age := float64(now.Unix()-oldest.Unix()) + float64(now.Nanosecond()-oldest.Nanosecond())/float64(time.Second)
		result.EvidenceAge = &EvidenceAge{EvaluatedAt: now.UTC(), OldestRunCreatedAt: oldest.UTC(), AgeSeconds: age, MaxAgeSeconds: *policy.MaxCandidateAgeSeconds}
	}
	blocked := false
	if missing || oldest.IsZero() {
		result.Reasons = append(result.Reasons, "candidate_evidence_age_unavailable")
		blocked = true
	}
	if now.IsZero() || future {
		result.Reasons = append(result.Reasons, "candidate_evidence_future")
		blocked = true
	}
	if !oldest.IsZero() && oldest.Before(now.Add(-time.Duration(*policy.MaxCandidateAgeSeconds)*time.Second)) {
		result.Reasons = append(result.Reasons, "candidate_evidence_stale")
		blocked = true
	}
	return blocked
}

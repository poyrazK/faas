package api

import (
	"net/url"
	"strconv"
	"time"
)

// FindingURL identifies retained evidence without copying symbols into a URL.
func (s CanaryProfileSignal) FindingURL(index int) string {
	if s.ComparisonURL == "" || s.Candidate == nil || index < 0 || index >= len(s.Evidence) {
		return ""
	}
	u, err := url.Parse(s.ComparisonURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("canary_deployment", s.Candidate.DeploymentID)
	q.Set("canary_step", strconv.Itoa(s.CanaryStep))
	q.Set("canary_started_at", s.CanaryStepStartedAt.Format(time.RFC3339Nano))
	q.Set("canary_revision", strconv.FormatInt(s.PolicyRevision, 10))
	q.Set("canary_finding", strconv.Itoa(index))
	u.RawQuery = q.Encode()
	return u.String()
}

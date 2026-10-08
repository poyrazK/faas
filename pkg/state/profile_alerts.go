package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

type profileAlertState struct {
	PendingStatus string    `json:"pending_status,omitempty"`
	PendingCount  int       `json:"pending_count,omitempty"`
	IncidentID    string    `json:"incident_id"`
	Regressed     bool      `json:"regressed"`
	WindowEnd     time.Time `json:"window_end"`
	CheckedAt     time.Time `json:"checked_at"`
}
type profileAlertObservation struct {
	AppID, AccountID, Slug, Scope, Source, EvidencePath, InvestigationID string
	Revision                                                             int64
	Config                                                               api.ProfileDeploymentPolicyConfig
	Assessment                                                           api.ProfileRegressionAssessment
	Confirmations                                                        int
}
type profileAlertPlan struct {
	Key      string
	State    profileAlertState
	Event    AppWebhookEvent
	SourceID string
	Payload  []byte
}

func profileAlertKey(o profileAlertObservation, route string) string {
	body, _ := json.Marshal([]any{o.AppID, normalizedDeploymentScope(o.Scope), o.Assessment.Baseline.DeploymentID, o.Assessment.Candidate.DeploymentID, o.Revision, route})
	if o.Source == "periodic" {
		body = append(body, []byte(o.Assessment.Baseline.Start.UTC().Format(time.RFC3339Nano)+o.Assessment.Baseline.End.UTC().Format(time.RFC3339Nano)+"periodic")...)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func profileAlertEligible(o profileAlertObservation, r api.ProfileRouteRegression) bool {
	a := o.Assessment
	options := o.Config.Options
	if r.Metric == nil || r.BaselineRequests == nil || r.CandidateRequests == nil || *r.BaselineRequests < options.MinimumRequests || *r.CandidateRequests < options.MinimumRequests {
		return false
	}
	if a.Attribution == nil || !a.Attribution.Available || a.Attribution.SubstantialChange {
		return false
	}
	for _, c := range []*api.ProfileCoverage{a.BaselineCoverage, a.CandidateCoverage} {
		if c == nil || !c.Available || c.ReceivedProfiles < options.MinimumProfiles || c.WindowSeconds <= 0 || c.CoveredSeconds/c.WindowSeconds < options.MinimumCoverageRatio || c.RecordedFailedUploads > 0 {
			return false
		}
	}
	labels := r.LabelCoverage
	if labels == nil || !labels.Available || !labels.Consistent || labels.Baseline == nil || labels.Candidate == nil {
		return false
	}
	for _, v := range []*api.ProfileRouteLabelCoverage{labels.Baseline, labels.Candidate} {
		if !v.Available || v.Percent == nil || *v.Percent < api.ProfileRouteMinimumLabelCoveragePercent {
			return false
		}
	}
	if math.Abs(*labels.Baseline.Percent-*labels.Candidate.Percent) >= api.ProfileRouteLabelMaxChangePercentagePoints {
		return false
	}
	return true
}

// Unknown observations advance the watermark without resolving a confirmed
// incident. Context changes create independent incidents rather than recovery.
func profileAlertTransition(previous profileAlertState, o profileAlertObservation, r api.ProfileRouteRegression) (profileAlertPlan, error) {
	next := profileAlertPlan{Key: profileAlertKey(o, r.Route), State: previous}
	a := o.Assessment
	if o.Source == "periodic" && !a.Candidate.End.After(previous.WindowEnd) {
		return next, nil
	}
	if a.Candidate.End.Before(previous.WindowEnd) || a.Candidate.End.Equal(previous.WindowEnd) && !a.CheckedAt.After(previous.CheckedAt) {
		return next, nil
	}
	next.State.WindowEnd, next.State.CheckedAt = a.Candidate.End, a.CheckedAt
	if !profileAlertEligible(o, r) || (r.Status != "regressed" && r.Status != "no_regression_detected") {
		next.State.PendingStatus, next.State.PendingCount = "", 0
		return next, nil
	}
	if o.Confirmations > 1 {
		if r.Status != previous.PendingStatus {
			next.State.PendingCount = 0
		}
		next.State.PendingStatus = r.Status
		next.State.PendingCount++
		if next.State.PendingCount > o.Confirmations {
			next.State.PendingCount = o.Confirmations
		}
		if next.State.PendingCount < o.Confirmations {
			return next, nil
		}
	}
	switch r.Status {
	case "regressed":
		if previous.Regressed || !r.Metric.ExceedsThreshold {
			return next, nil
		}
		next.State.Regressed = true
		next.State.IncidentID = uuid.NewString()
		next.Event = AppWebhookEventProfileRouteRegressed
	case "no_regression_detected":
		if !previous.Regressed || r.Metric.ExceedsThreshold {
			return next, nil
		}
		next.State.Regressed = false
		next.Event = AppWebhookEventProfileRouteRecovered
	default:
		return next, nil
	}
	status := "regressed"
	if next.Event == AppWebhookEventProfileRouteRecovered {
		status = "recovered"
	}
	payload := api.ProfileRouteAlertPayload{Version: 1, AppID: o.AppID, DeploymentID: a.Candidate.DeploymentID, BaselineDeploymentID: a.Baseline.DeploymentID, PolicyRevision: o.Revision, IncidentID: next.State.IncidentID, Status: status, Source: o.Source, CheckedAt: a.CheckedAt, Baseline: a.Baseline, Candidate: a.Candidate, RouteCheck: r, EvidencePath: o.EvidencePath, ComparisonURL: api.ProfileRouteComparisonURL(o.Slug, a.Baseline, a.Candidate, r)}
	if o.InvestigationID != "" {
		payload.InvestigationPath = "/dashboard/apps/" + url.PathEscape(o.Slug) + "/profiles?investigation_id=" + url.QueryEscape(o.InvestigationID)
	}
	for _, e := range a.Evidence {
		if e.Kind != "call_path" && e.Kind != "function" {
			continue
		}
		for _, f := range e.Frames {
			if len(payload.ApplicationFrames) >= api.ProfileAlertMaxFrames {
				break
			}
			payload.ApplicationFrames = append(payload.ApplicationFrames, api.ProfileCallPathFrame{Name: safetext.Truncate(f.Name, api.ProfileAlertMaxSymbolBytes), File: safetext.Truncate(f.File, api.ProfileAlertMaxSymbolBytes), Line: f.Line})
		}
		if len(payload.ApplicationFrames) > 0 {
			break
		}
	}
	next.SourceID = uuid.NewString()
	body, err := json.Marshal(payload)
	next.Payload = body
	return next, err
}

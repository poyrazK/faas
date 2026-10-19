package api

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var collectionDiagnosticHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var collectionDiagnosticIdentity = regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`)
var collectionDiagnosticRuleName = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
var collectionDiagnosticRunbook = regexp.MustCompile(`^[a-zA-Z0-9:/_.%#~-]+$`)

// ValidObsAlertCollectionRejection enforces the persisted allowlist as well as
// the CLI contract. Only typed, bounded, sanitized observations may be retained.
func ValidObsAlertCollectionRejection(at time.Time, d ObsAlertCollectionRejection) bool {
	t := d.Totals
	if at.IsZero() || d.Scope != "rejected_snapshot" || d.Items == nil || d.Limit != ObsAlertCollectionRejectionItemsMax {
		return false
	}
	for _, n := range []int{t.Groups, t.Rules, t.Healthy, t.Failed, t.Stale, t.InvalidClock, t.Unavailable, t.Invalid, t.InvalidGroups} {
		if n < 0 || n > ObsAlertSnapshotMaxRules {
			return false
		}
	}
	if t.Rules > 0 && t.Groups == 0 || t.InvalidGroups > t.Groups || t.Healthy+t.Failed+t.Stale+t.InvalidClock+t.Unavailable+t.Invalid != t.Rules || d.TotalItems < t.Rules-t.Healthy || d.TotalItems > t.Rules-t.Healthy+t.InvalidGroups || len(d.Items) > min(d.TotalItems, d.Limit) || d.TotalItems > 0 && len(d.Items) == 0 || d.Truncated != (d.TotalItems > len(d.Items)) {
		return false
	}
	reason := "collector_validation_failed"
	if d.TotalItems > 0 {
		reason = "rule_diagnostics_present"
	} else if t.Rules == 0 && t.InvalidGroups == 0 {
		reason = "no_alert_rules_observed"
	}
	if d.Reason != reason {
		return false
	}
	for _, item := range d.Items {
		if !validCollectionDiagnostic(at, item) {
			return false
		}
	}
	encoded, err := json.Marshal(d)
	return err == nil && len(encoded) <= ObsAlertCollectionRejectionMaxBytes
}

func validCollectionDiagnostic(at time.Time, r ObsRuleHealthItem) bool {
	if !collectionDiagnosticHash.MatchString(r.ID) || !collectionDiagnosticHash.MatchString(r.GroupID) || len(r.Issues) == 0 || r.Pending != nil || r.Firing != nil {
		return false
	}
	for _, name := range []string{r.GroupName, r.FileName} {
		if name != "" && (len(name) > ObsRuleHealthIdentityMaxBytes || !collectionDiagnosticIdentity.MatchString(name)) {
			return false
		}
	}
	if r.RuleName != "" && (len(r.RuleName) > ObsAlertNameMaxBytes || !collectionDiagnosticRuleName.MatchString(r.RuleName)) || !validCollectionDiagnosticRunbook(r.RunbookURL) {
		return false
	}
	state, priority := "invalid", 5
	seen := map[string]bool{}
	for _, issue := range r.Issues {
		if seen[issue] {
			return false
		}
		seen[issue] = true
		s, p := "invalid", 0
		switch issue {
		case "invalid_group_identity", "duplicate_group_identity", "unexpected_rule_type", "invalid_rule_definition", "invalid_alert_observation", "inconsistent_rule_state", "duplicate_rule_identity":
		case "evaluation_clock_in_future":
			s, p = "invalid_clock", 1
		case "evaluation_failed":
			s, p = "failed", 2
		case "evaluation_stale":
			s, p = "stale", 3
		case "missing_evaluation", "evaluation_unavailable":
			s, p = "unavailable", 4
		default:
			return false
		}
		if p < priority {
			state, priority = s, p
		}
	}
	if r.State != state || r.ReportedState != "unknown" && r.ReportedState != "inactive" && r.ReportedState != "pending" && r.ReportedState != "firing" {
		return false
	}
	if r.Kind == "group" {
		return r.ID == r.GroupID && r.State == "invalid" && r.RuleName == "" && r.LastEvaluatedAt == nil && r.EvaluationAgeSeconds == nil && !r.HasEvaluationError && r.RunbookURL == "" && r.ReportedState == "unknown" && (seen["invalid_group_identity"] || seen["duplicate_group_identity"])
	}
	if r.Kind != "rule" || r.HasEvaluationError && !seen["evaluation_failed"] {
		return false
	}
	if r.LastEvaluatedAt == nil {
		return r.EvaluationAgeSeconds == nil && seen["missing_evaluation"]
	}
	evaluated := *r.LastEvaluatedAt
	if evaluated.IsZero() || evaluated.Year() < 1 || evaluated.Year() > 9999 || seen["missing_evaluation"] {
		return false
	}
	if evaluated.After(at) {
		return r.EvaluationAgeSeconds == nil
	}
	age := at.Unix() - evaluated.Unix()
	if at.Nanosecond() < evaluated.Nanosecond() {
		age--
	}
	return r.EvaluationAgeSeconds != nil && *r.EvaluationAgeSeconds == age
}

func validCollectionDiagnosticRunbook(book string) bool {
	if book == "" {
		return true
	}
	if len(book) > ObsRuleHealthRunbookMaxBytes || !collectionDiagnosticRunbook.MatchString(book) {
		return false
	}
	u, err := url.Parse(book)
	if err != nil || u.User != nil || u.RawQuery != "" || strings.Contains(u.Path, "..") {
		return false
	}
	return u.Scheme == "" && u.Host == "" && strings.HasPrefix(u.Path, "/docs/") || u.Scheme == "https" && u.Host == "github.com" && strings.HasPrefix(u.Path, "/poyrazK/faas/blob/main/docs/")
}

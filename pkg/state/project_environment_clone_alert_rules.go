package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A full environment clone copies the source's alert rules into the stage
// paused, with their notification channels (ADR-20261104120000 amendment
// 2026-11-28). A rule that names one of production's own objects (an SLO, a
// synthetic check, an edge rule or an event subscription) or reads an
// environment's anomaly baseline stays with the source: the stage has none
// of those to point at.

type projectCloneAlertRule struct {
	// Rule is the rule's configuration: identity, state and history are
	// cleared, and Enabled records whether the source ran it.
	Rule     AlertRule `json:"rule"`
	Channels []string  `json:"channels"`
}

// projectCloneAlertRules is one workload's alert rules in the source
// environment. A snapshot without it predates alert rule capture and copies
// none.
type projectCloneAlertRules struct {
	AppID string                  `json:"app_id"`
	Rules []projectCloneAlertRule `json:"rules"`
	// LeftWithSource counts the rules the clone does not copy.
	LeftWithSource int `json:"left_with_source,omitempty"`
}

// cloneableAlertRule reports whether a rule's copy can stand in a stage on
// its own.
func cloneableAlertRule(r AlertRule) bool {
	return r.AppID != "" && r.SLOID == "" && r.SyntheticCheckID == "" && r.EdgeRuleID == "" &&
		r.EventSubscriptionID == "" && r.EnvironmentScope == ""
}

// cloneAlertRuleConfig clears what a copy does not carry over.
func cloneAlertRuleConfig(r AlertRule) AlertRule {
	r.ID, r.AccountID, r.AppID, r.EnvironmentID = "", "", "", ""
	r.State, r.LastFiredAt, r.LastEvaluatedAt = "", time.Time{}, time.Time{}
	r.AnomalyEvaluation, r.ObservedIncidents, r.observationChecks = api.AlertAnomalyEvaluation{}, nil, nil
	r.CreatedAt, r.UpdatedAt = time.Time{}, time.Time{}
	r.CustomMetricLabels = cloneCustomMetricLabels(r.CustomMetricLabels)
	if len(r.CustomMetricLabels) == 0 {
		r.CustomMetricLabels = nil
	}
	r.WebhookSecretSealed = bytes.Clone(r.WebhookSecretSealed)
	if len(r.WebhookSecretSealed) == 0 {
		r.WebhookSecretSealed = nil
	}
	return r
}

func normalizeCloneAlertRules(in projectCloneAlertRules) (projectCloneAlertRules, error) {
	out := projectCloneAlertRules{AppID: in.AppID, LeftWithSource: in.LeftWithSource, Rules: make([]projectCloneAlertRule, 0, len(in.Rules))}
	names := map[string]bool{}
	for _, r := range in.Rules {
		if r.Rule.Name == "" || names[r.Rule.Name] {
			return out, ErrConflict
		}
		names[r.Rule.Name] = true
		enabled := r.Rule.Enabled
		r.Rule = cloneAlertRuleConfig(r.Rule)
		r.Rule.Enabled = enabled
		r.Channels = slices.Sorted(slices.Values(r.Channels))
		if r.Channels == nil {
			r.Channels = []string{}
		}
		out.Rules = append(out.Rules, r)
	}
	sort.Slice(out.Rules, func(i, j int) bool { return out.Rules[i].Rule.Name < out.Rules[j].Rule.Name })
	return out, nil
}

// capturedCloneAlertRules returns each captured workload's rules by app.
func capturedCloneAlertRules(records []projectCloneWorkloadRecord) map[string]projectCloneAlertRules {
	out := make(map[string]projectCloneAlertRules, len(records))
	for _, record := range records {
		if record.snapshot.AlertRules != nil {
			out[record.AppID] = *record.snapshot.AlertRules
		}
	}
	return out
}

// validateCloneAlertRulePublication proves the target holds exactly the
// captured rules, each paused, with the captured channels. actual holds the
// target's rules as stored, so their Enabled flags must all be false.
func validateCloneAlertRulePublication(record projectCloneWorkloadRecord, actual []projectCloneAlertRule) error {
	want := projectCloneAlertRules{AppID: record.AppID}
	if record.snapshot.AlertRules != nil {
		want.Rules = record.snapshot.AlertRules.Rules
	}
	want, err := normalizeCloneAlertRules(want)
	if err != nil {
		return err
	}
	for _, r := range actual {
		if r.Rule.Enabled {
			return fmt.Errorf("clone workload %q has an enabled alert rule before publication: %w", record.WorkloadSlug, ErrConflict)
		}
	}
	got, err := normalizeCloneAlertRules(projectCloneAlertRules{AppID: record.AppID, Rules: actual})
	if err != nil || len(got.Rules) != len(want.Rules) {
		return fmt.Errorf("clone workload %q alert rules differ from its capture: %w", record.WorkloadSlug, ErrConflict)
	}
	for i := range want.Rules {
		w, g := want.Rules[i], got.Rules[i]
		w.Rule.Enabled = false
		if !slices.Equal(w.Channels, g.Channels) || !bytes.Equal(alertRuleCloneJSON(w.Rule), alertRuleCloneJSON(g.Rule)) {
			return fmt.Errorf("clone workload %q alert rules differ from its capture: %w", record.WorkloadSlug, ErrConflict)
		}
	}
	return nil
}

func alertRuleCloneJSON(r AlertRule) []byte {
	b, _ := json.Marshal(cloneAlertRuleConfig(r))
	return b
}

// cloneAlertRuleCounts counts the rules a copy adds per app.
func cloneAlertRuleCounts(rules map[string]projectCloneAlertRules) map[string]int {
	out := map[string]int{}
	for appID, captured := range rules {
		out[appID] += len(captured.Rules)
	}
	return out
}

// checkCloneAlertRuleQuota refuses a copy that takes an app or its billing
// subject past the plan's alert rule caps. count returns an app's rules now.
func checkCloneAlertRuleQuota(added map[string]int, count func(appID string) (int, error), subjectRules int, limits api.Limits) error {
	total := 0
	for _, appID := range slices.Sorted(maps.Keys(added)) {
		if added[appID] == 0 {
			continue
		}
		total += added[appID]
		n, err := count(appID)
		if err != nil {
			return err
		}
		if observed := n + added[appID]; observed > limits.AlertRuleLimitPerApp {
			return &AlertRuleQuotaError{Scope: AlertRuleQuotaScopeApp, Limit: limits.AlertRuleLimitPerApp, Observed: observed}
		}
	}
	if total > 0 && subjectRules+total > limits.AlertRuleLimitPerAccount {
		return &AlertRuleQuotaError{Scope: AlertRuleQuotaScopeAccount, Limit: limits.AlertRuleLimitPerAccount, Observed: subjectRules + total}
	}
	return nil
}

func (m *MemStore) captureCloneAlertRulesLocked(appID, environmentID string) (projectCloneAlertRules, error) {
	captured := projectCloneAlertRules{AppID: appID}
	for _, r := range m.alertRules {
		if !sameMemUUID(r.AppID, appID) || r.EnvironmentID != environmentID {
			continue
		}
		if !cloneableAlertRule(r) {
			captured.LeftWithSource++
			continue
		}
		captured.Rules = append(captured.Rules, projectCloneAlertRule{Rule: r, Channels: slices.Clone(m.alertRuleChannels[r.ID])})
	}
	return normalizeCloneAlertRules(captured)
}

// requireCloneAlertRulesCapturedLocked refuses a source whose environment
// holds rules on project apps outside the captured workloads.
func (m *MemStore) requireCloneAlertRulesCapturedLocked(projectID, environmentID string, captured map[string]bool) error {
	for _, r := range m.alertRules {
		app, ok := m.apps[r.AppID]
		if r.AppID == "" || !ok || app.ProjectID != projectID || app.Status == AppDeleted || r.EnvironmentID != environmentID || captured[app.ID] {
			continue
		}
		return ErrProjectEnvironmentCloneAlertRuleCaptureUnavailable
	}
	return nil
}

// ErrProjectEnvironmentCloneAlertRuleCaptureUnavailable refuses a source
// whose alert rules the clone would not copy: rules of an app that has no
// workload in the source environment.
var ErrProjectEnvironmentCloneAlertRuleCaptureUnavailable = fmt.Errorf("clone alert rule capture is unavailable: %w", ErrConflict)

func (m *MemStore) checkCloneAlertRuleQuotaLocked(rules map[string]projectCloneAlertRules, limits api.Limits) error {
	added := cloneAlertRuleCounts(rules)
	var first string
	for _, appID := range slices.Sorted(maps.Keys(added)) {
		if added[appID] > 0 {
			first = appID
			break
		}
	}
	if first == "" {
		return nil
	}
	app := m.apps[first]
	subject := m.appBillingSubjectLocked(app.AccountID, app.ID)
	perApp, subjectRules := map[string]int{}, 0
	for _, r := range m.alertRules {
		if r.AppID != "" {
			perApp[r.AppID]++
		}
		if m.countsTowardQuotaSubjectLocked(r.AccountID, r.AppID, subject) {
			subjectRules++
		}
	}
	return checkCloneAlertRuleQuota(added, func(appID string) (int, error) { return perApp[appID], nil }, subjectRules, limits)
}

func (m *MemStore) copyCapturedAlertRulesLocked(clone ProjectEnvironmentClone, target ProjectEnvironment, now time.Time, result *ProjectEnvironmentCloneResult) {
	if m.alertRules == nil {
		m.alertRules = map[string]AlertRule{}
	}
	for _, appID := range slices.Sorted(maps.Keys(clone.capturedAlertRules)) {
		app, ok := m.apps[appID]
		if !ok {
			continue
		}
		captured := clone.capturedAlertRules[appID]
		result.AlertRulesLeftWithSource += captured.LeftWithSource
		for _, r := range captured.Rules {
			rule := cloneAlertRuleConfig(r.Rule)
			rule.ID, rule.AccountID, rule.AppID, rule.EnvironmentID = uuid.NewString(), app.AccountID, appID, target.ID
			rule.Enabled, rule.State, rule.CreatedAt, rule.UpdatedAt = false, AlertStateOk, now, now
			m.alertRules[rule.ID] = rule
			m.storeAlertRuleChannelsLocked(rule.ID, slices.Clone(r.Channels))
			result.AlertRulesCopied++
		}
	}
}

func (m *MemStore) verifyCloneAlertRulePublicationLocked(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) error {
	environmentID := m.cloneEnvironmentIDLocked(op.ProjectID, op.TargetEnvironment)
	for _, record := range records {
		var actual []projectCloneAlertRule
		for _, r := range m.alertRules {
			if !sameMemUUID(r.AppID, record.AppID) || environmentID == "" || r.EnvironmentID != environmentID {
				continue
			}
			actual = append(actual, projectCloneAlertRule{Rule: r, Channels: slices.Clone(m.alertRuleChannels[r.ID])})
		}
		if err := validateCloneAlertRulePublication(record, actual); err != nil {
			return err
		}
	}
	return nil
}

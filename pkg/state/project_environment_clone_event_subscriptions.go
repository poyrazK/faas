package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// A full environment clone copies the source's event subscriptions into the
// stage paused, as it does crons (ADR-20261104120000 amendment 2026-11-14).
// Resuming the stage's automation enables the ones the source ran.

// ErrProjectEnvironmentCloneEventSubscriptionCaptureUnavailable refuses a
// source whose event subscriptions the clone would not copy: subscriptions of
// an app that has no workload in the source environment.
var ErrProjectEnvironmentCloneEventSubscriptionCaptureUnavailable = fmt.Errorf("clone event subscription capture is unavailable: %w", ErrConflict)

type projectCloneEventSubscription struct {
	Source                     string                       `json:"source"`
	Type                       string                       `json:"type"`
	Filter                     json.RawMessage              `json:"filter"`
	Enabled                    bool                         `json:"enabled"`
	RoutingRetryPolicy         *api.EventRoutingRetryPolicy `json:"routing_retry_policy,omitempty"`
	SchemaVersions             []string                     `json:"schema_versions,omitempty"`
	AcknowledgmentLeaseSeconds int                          `json:"acknowledgment_lease_seconds,omitempty"`
}

// projectCloneEventSubscriptions is one workload's event subscriptions in the
// source environment. A snapshot without it predates subscription capture
// and copies none.
type projectCloneEventSubscriptions struct {
	AppID         string                          `json:"app_id"`
	SourceScope   string                          `json:"source_scope"`
	Subscriptions []projectCloneEventSubscription `json:"subscriptions"`
}

func eventSubscriptionCloneKey(s projectCloneEventSubscription) string {
	return s.Source + "\x00" + s.Type + "\x00" + string(s.Filter)
}

func normalizeCloneEventSubscriptions(in projectCloneEventSubscriptions) (projectCloneEventSubscriptions, error) {
	out := projectCloneEventSubscriptions{AppID: in.AppID, SourceScope: in.SourceScope,
		Subscriptions: make([]projectCloneEventSubscription, 0, len(in.Subscriptions))}
	for _, s := range in.Subscriptions {
		if s.Source == "" || s.Type == "" {
			return out, ErrConflict
		}
		filter, err := normalizeEventSubscriptionFilter(s.Filter)
		if err != nil {
			return out, ErrConflict
		}
		s.Filter = filter
		s.RoutingRetryPolicy = cloneEventRoutingRetryPolicy(s.RoutingRetryPolicy)
		if len(s.SchemaVersions) == 0 {
			s.SchemaVersions = nil
		} else {
			s.SchemaVersions = slices.Clone(s.SchemaVersions)
		}
		out.Subscriptions = append(out.Subscriptions, s)
	}
	sort.Slice(out.Subscriptions, func(i, j int) bool {
		return eventSubscriptionCloneKey(out.Subscriptions[i]) < eventSubscriptionCloneKey(out.Subscriptions[j])
	})
	for i := 1; i < len(out.Subscriptions); i++ {
		if eventSubscriptionCloneKey(out.Subscriptions[i-1]) == eventSubscriptionCloneKey(out.Subscriptions[i]) {
			return out, ErrConflict
		}
	}
	return out, nil
}

// capturedCloneEventSubscriptions returns each captured workload's
// subscriptions by app.
func capturedCloneEventSubscriptions(records []projectCloneWorkloadRecord) map[string]projectCloneEventSubscriptions {
	out := make(map[string]projectCloneEventSubscriptions, len(records))
	for _, record := range records {
		if record.snapshot.EventSubscriptions != nil {
			out[record.AppID] = *record.snapshot.EventSubscriptions
		}
	}
	return out
}

// validateCloneEventSubscriptionPublication proves the target holds exactly
// the captured subscriptions, each paused: disabled, marked clone_paused when
// the source ran it.
func validateCloneEventSubscriptionPublication(record projectCloneWorkloadRecord, actual []projectCloneEventSubscription, storedEnabled []bool) error {
	want := projectCloneEventSubscriptions{AppID: record.AppID}
	if record.snapshot.EventSubscriptions != nil {
		want.Subscriptions = record.snapshot.EventSubscriptions.Subscriptions
	}
	want, err := normalizeCloneEventSubscriptions(want)
	if err != nil {
		return err
	}
	for _, enabled := range storedEnabled {
		if enabled {
			return fmt.Errorf("clone workload %q has an enabled event subscription before publication: %w", record.WorkloadSlug, ErrConflict)
		}
	}
	got, err := normalizeCloneEventSubscriptions(projectCloneEventSubscriptions{AppID: record.AppID, Subscriptions: actual})
	if err != nil || !slices.EqualFunc(want.Subscriptions, got.Subscriptions, sameCloneEventSubscription) {
		return fmt.Errorf("clone workload %q event subscriptions differ from its capture: %w", record.WorkloadSlug, ErrConflict)
	}
	return nil
}

func sameCloneEventSubscription(a, b projectCloneEventSubscription) bool {
	return eventSubscriptionCloneKey(a) == eventSubscriptionCloneKey(b) && a.Enabled == b.Enabled &&
		a.AcknowledgmentLeaseSeconds == b.AcknowledgmentLeaseSeconds && slices.Equal(a.SchemaVersions, b.SchemaVersions) &&
		bytes.Equal(retryPolicyCloneJSON(a.RoutingRetryPolicy), retryPolicyCloneJSON(b.RoutingRetryPolicy))
}

func retryPolicyCloneJSON(v *api.EventRoutingRetryPolicy) []byte {
	b, _ := json.Marshal(v)
	return b
}

// CloneEventSubscriptionResumeStore enables the event subscriptions a full
// clone paused in one environment. Enabled subscriptions count against the
// plan's caps (ADR-20261031130000): a resume that would take an app or its
// billing subject past one changes nothing and returns an
// *EventSubscriptionQuotaError.
type CloneEventSubscriptionResumeStore interface {
	ResumeEnvironmentClonePausedEventSubscriptions(ctx context.Context, accountID, projectID, environment string, limits api.Limits) ([]EventSubscription, error)
}

// EventSubscriptionQuotaError reports the cap a change of enabled event
// subscriptions would exceed. Scope is api.PlanQuotaScopeApp or
// api.PlanQuotaScopeAccount.
type EventSubscriptionQuotaError struct {
	Scope    string
	Limit    int
	Observed int
}

func (e *EventSubscriptionQuotaError) Error() string {
	return fmt.Sprintf("state: event subscription quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

// checkEventSubscriptionQuota refuses counts past the plan's caps.
func checkEventSubscriptionQuota(perApp, perAccount int, limits api.Limits) error {
	if perApp > limits.EventSubscriptionsPerApp {
		return &EventSubscriptionQuotaError{Scope: api.PlanQuotaScopeApp, Limit: limits.EventSubscriptionsPerApp, Observed: perApp}
	}
	if perAccount > limits.EventSubscriptionsPerAccount {
		return &EventSubscriptionQuotaError{Scope: api.PlanQuotaScopeAccount, Limit: limits.EventSubscriptionsPerAccount, Observed: perAccount}
	}
	return nil
}

func (m *MemStore) captureCloneEventSubscriptionsLocked(appID, scope string) (projectCloneEventSubscriptions, error) {
	subscriptionScope := NormalizeCronDeploymentScope(scope)
	captured := projectCloneEventSubscriptions{AppID: appID, SourceScope: subscriptionScope}
	for _, s := range m.eventSubscriptions {
		if !sameMemUUID(s.AppID, appID) || s.DeploymentScope != subscriptionScope {
			continue
		}
		captured.Subscriptions = append(captured.Subscriptions, cloneEventSubscriptionFrom(s))
	}
	return normalizeCloneEventSubscriptions(captured)
}

func cloneEventSubscriptionFrom(s EventSubscription) projectCloneEventSubscription {
	return projectCloneEventSubscription{Source: s.Source, Type: s.Type, Filter: append(json.RawMessage(nil), s.Filter...),
		Enabled: s.Enabled || s.ClonePaused, RoutingRetryPolicy: cloneEventRoutingRetryPolicy(s.RoutingRetryPolicy),
		SchemaVersions: slices.Clone(s.SchemaVersions), AcknowledgmentLeaseSeconds: s.AcknowledgmentLeaseSeconds}
}

// requireCloneEventSubscriptionsCapturedLocked refuses a source whose
// environment holds subscriptions on project apps outside the captured
// workloads.
func (m *MemStore) requireCloneEventSubscriptionsCapturedLocked(projectID, source string, captured map[string]bool) error {
	scope := NormalizeCronDeploymentScope(source)
	for _, s := range m.eventSubscriptions {
		app, ok := m.eventSubscriptionAppLocked(s.AppID)
		if !ok || app.ProjectID != projectID || app.Status == "deleted" || s.DeploymentScope != scope || captured[app.ID] {
			continue
		}
		return ErrProjectEnvironmentCloneEventSubscriptionCaptureUnavailable
	}
	return nil
}

func (m *MemStore) copyCapturedEventSubscriptionsLocked(clone ProjectEnvironmentClone, target ProjectEnvironment, now time.Time, result *ProjectEnvironmentCloneResult) {
	if m.eventSubscriptions == nil {
		m.eventSubscriptions = make(map[string]EventSubscription)
	}
	for appID, captured := range clone.capturedEventSubscriptions {
		app, ok := m.eventSubscriptionAppLocked(appID)
		if !ok {
			continue
		}
		for _, s := range captured.Subscriptions {
			key, err := eventSubscriptionKey(s.Source, s.Type, s.Filter)
			if err != nil {
				continue
			}
			subscription := EventSubscription{ID: uuid.NewString(), AccountID: canonicalMemUUID(app.AccountID), AppID: canonicalMemUUID(appID),
				Source: s.Source, Type: s.Type, Filter: append(json.RawMessage(nil), s.Filter...), Enabled: false, ClonePaused: s.Enabled,
				RoutingRetryPolicy: cloneEventRoutingRetryPolicy(s.RoutingRetryPolicy), SchemaVersions: slices.Clone(s.SchemaVersions),
				AcknowledgmentLeaseSeconds: s.AcknowledgmentLeaseSeconds, DeploymentScope: target.Slug, EnvironmentID: target.ID,
				CreatedAt: now, UpdatedAt: now}
			m.eventSubscriptions[canonicalMemUUID(appID)+"\x00"+key+"\x00"+target.Slug] = subscription
			if m.eventExecutionCoverage == nil {
				m.eventExecutionCoverage = map[string]executionCoverage{}
			}
			m.eventExecutionCoverage[subscription.ID] = executionCoverage{app: subscription.AppID, started: now, baseline: true, ledgerStarted: now, ledgerBaseline: true}
			result.EventSubscriptionsCopied++
		}
	}
}

func (m *MemStore) verifyCloneEventSubscriptionPublicationLocked(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) error {
	for _, record := range records {
		var actual []projectCloneEventSubscription
		var stored []bool
		for _, s := range m.eventSubscriptions {
			if !sameMemUUID(s.AppID, record.AppID) || s.DeploymentScope != op.TargetEnvironment {
				continue
			}
			actual = append(actual, cloneEventSubscriptionFrom(s))
			stored = append(stored, s.Enabled)
		}
		if err := validateCloneEventSubscriptionPublication(record, actual, stored); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) ResumeEnvironmentClonePausedEventSubscriptions(_ context.Context, accountID, projectID, environment string, limits api.Limits) ([]EventSubscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if IsProductionScope(environment) {
		return nil, ErrInvalidArgument
	}
	environmentID := ""
	for _, env := range m.projectEnvironments {
		if env.AccountID == accountID && env.ProjectID == projectID && env.Slug == environment {
			environmentID = env.ID
			break
		}
	}
	if environmentID == "" {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	before := make(map[string]EventSubscription)
	var resumed []EventSubscription
	for key, s := range m.eventSubscriptions {
		if s.EnvironmentID != environmentID || !s.ClonePaused || s.Enabled {
			continue
		}
		before[key] = s
		s.Enabled, s.ClonePaused, s.UpdatedAt = true, false, now
		m.eventSubscriptions[key] = s
		resumed = append(resumed, cloneEventSubscription(s))
	}
	for _, s := range resumed {
		perApp, perAccount := m.eventSubscriptionQuotaCountsLocked(s.AppID)
		if err := checkEventSubscriptionQuota(perApp, perAccount, limits); err != nil {
			for key, s := range before {
				m.eventSubscriptions[key] = s
			}
			return nil, err
		}
	}
	sort.Slice(resumed, func(i, j int) bool { return resumed[i].ID < resumed[j].ID })
	return resumed, nil
}

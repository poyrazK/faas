package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrStoredWorkflowFailurePolicyConflict = errors.New("stored workflow failure policy or pause generation changed; reload before changing it")

// StoredWorkflowFailurePolicyBatchError reports individual policy failures after
// the entire page was visited. Its accompanying cursor may be advanced; a
// later full pass must retry the failed policies.
type StoredWorkflowFailurePolicyBatchError struct{ Err error }

func (e *StoredWorkflowFailurePolicyBatchError) Error() string {
	return "one or more stored workflow failure policies could not be evaluated"
}
func (e *StoredWorkflowFailurePolicyBatchError) Unwrap() error { return e.Err }

type StoredWorkflowFailurePolicyStore interface {
	GetStoredWorkflowFailurePolicy(context.Context, string, string) (api.StoredWorkflowFailurePolicyResponse, error)
	SetStoredWorkflowFailurePolicy(context.Context, string, string, api.SetStoredWorkflowFailurePolicyRequest) (api.StoredWorkflowFailurePolicyResponse, error)
	ResumeStoredWorkflowFailurePause(context.Context, string, string, string, api.ResumeStoredWorkflowFailurePauseRequest) (api.StoredWorkflowFailurePolicyResponse, error)
	ListStoredWorkflowFailurePauses(context.Context, string) ([]string, error)
	EvaluateStoredWorkflowFailurePolicies(context.Context, string, string, int, time.Time) (string, error)
}
type storedWorkflowFailureGuard struct {
	Generation      int64
	MonitoringSince time.Time
	PausedAt        *time.Time
}

func storedWorkflowFailureBlocksAdmission(out api.StoredWorkflowFailurePolicyResponse) bool {
	return out.Paused || out.Policy.Enabled && out.ObservedFailures >= int64(out.Policy.FailureThreshold) && out.ObservedCompletedRuns >= int64(out.Policy.MinCompletedRuns)
}

func (m *MemStore) storedWorkflowFailureBlocksAdmissionLocked(appID, name string, now time.Time) bool {
	key := appID + "/" + name
	if m.storedWorkflowFailureGuards[key].PausedAt != nil {
		return true
	}
	if !m.storedWorkflowFailurePolicies[key].Enabled {
		return false
	}
	out := m.failurePolicyResponseLocked(appID, name, now)
	if storedWorkflowFailureBlocksAdmission(out) {
		m.latchStoredWorkflowFailureLocked(appID, name, out, now)
		return true
	}
	return false
}

func (m *MemStore) latchStoredWorkflowFailureLocked(appID, name string, out api.StoredWorkflowFailurePolicyResponse, now time.Time) {
	key := appID + "/" + name
	if m.storedWorkflowFailureGuards == nil {
		m.storedWorkflowFailureGuards = map[string]storedWorkflowFailureGuard{}
	}
	g := m.storedWorkflowFailureGuards[key]
	if g.PausedAt != nil {
		return
	}
	at := now.UTC()
	if g.MonitoringSince.IsZero() {
		g.MonitoringSince = at
	}
	g.Generation++
	g.PausedAt = &at
	m.storedWorkflowFailureGuards[key] = g
	if m.storedWorkflowFailureHistory == nil {
		m.storedWorkflowFailureHistory = map[string][]api.StoredWorkflowFailureTransition{}
	}
	m.storedWorkflowFailureHistory[key] = append(m.storedWorkflowFailureHistory[key], api.StoredWorkflowFailureTransition{Generation: g.Generation, State: "paused", Reason: "failure_threshold", RecordedAt: at, Failures: out.ObservedFailures, CompletedRuns: out.ObservedCompletedRuns, PolicyVersion: out.Policy.Version})
	m.enqueueStoredWorkflowFailurePauseLocked(appID, name, g.Generation, out, at)
}

func defaultStoredWorkflowFailurePolicy() api.StoredWorkflowFailurePolicy {
	return api.StoredWorkflowFailurePolicy{FailureThreshold: api.StoredWorkflowFailurePolicyDefaultThreshold, MinCompletedRuns: api.StoredWorkflowFailurePolicyDefaultMinRuns, WindowSeconds: api.StoredWorkflowFailurePolicyDefaultWindowSeconds}
}
func validStoredWorkflowFailurePolicy(name string, r api.SetStoredWorkflowFailurePolicyRequest) bool {
	return name != "" && strings.TrimSpace(name) == name && !strings.ContainsAny(name, "/\\\x00") && len(name) <= api.StoredWorkflowNameMaxBytes && r.Enabled != nil && r.ExpectedVersion >= 0 && r.FailureThreshold > 0 && r.FailureThreshold <= api.StoredWorkflowFailurePolicyMaxCount && r.MinCompletedRuns > 0 && r.MinCompletedRuns <= api.StoredWorkflowFailurePolicyMaxCount && r.WindowSeconds >= api.StoredWorkflowFailurePolicyMinWindowSeconds && r.WindowSeconds <= api.StoredWorkflowFailurePolicyMaxWindowSeconds
}
func (m *MemStore) failurePolicyTargetLocked(appID, name string) error {
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return ErrNotFound
	}
	dep := m.storedWorkflowDeploymentLocked(appID)
	raw, err := mergeStoredWorkflowDefinitions(dep.Workflows, m.storedWorkflowRecordsLocked(appID))
	if err != nil {
		return err
	}
	var definitions []api.WorkflowSpec
	if err = json.Unmarshal(raw, &definitions); err != nil {
		return err
	}
	for _, d := range definitions {
		if d.Name == name {
			return nil
		}
	}
	return ErrNotFound
}
func (m *MemStore) failurePolicyResponseLocked(appID, name string, now time.Time) api.StoredWorkflowFailurePolicyResponse {
	key := appID + "/" + name
	p, exists := m.storedWorkflowFailurePolicies[key]
	if !exists {
		p = defaultStoredWorkflowFailurePolicy()
	}
	g := m.storedWorkflowFailureGuards[key]
	out := api.StoredWorkflowFailurePolicyResponse{Policy: p, Generation: g.Generation, History: []api.StoredWorkflowFailureTransition{}}
	if !g.MonitoringSince.IsZero() {
		at := g.MonitoringSince
		out.MonitoringSince = &at
	}
	if g.PausedAt != nil {
		at := *g.PausedAt
		out.PausedAt = &at
		out.Paused = true
	}
	since := now.Add(-time.Duration(p.WindowSeconds) * time.Second)
	if g.MonitoringSince.After(since) {
		since = g.MonitoringSince
	}
	for _, r := range m.workflowRuns {
		if r.AppID != appID || r.WorkflowName != name {
			continue
		}
		switch r.Status {
		case WorkflowRunStatusPending:
			out.PendingRuns++
		case WorkflowRunStatusRunning:
			out.RunningRuns++
		case WorkflowRunStatusAwaitingEvent:
			out.WaitingRuns++
		}
		if r.CancelledAt != nil || r.FinishedAt == nil || r.FinishedAt.Before(since) || r.FinishedAt.After(now) {
			continue
		}
		if r.Status == WorkflowRunStatusSucceeded || r.Status == WorkflowRunStatusFailed || r.Status == WorkflowRunStatusDead {
			out.ObservedCompletedRuns++
		}
		if r.Status == WorkflowRunStatusFailed || r.Status == WorkflowRunStatusDead {
			out.ObservedFailures++
		}
	}
	for _, work := range m.eventFanout {
		if work.Delivered {
			continue
		}
		for _, recipient := range work.RecipientSnapshot {
			if recipient.AppID != appID || len(recipient.Workflow) == 0 {
				continue
			}
			var d api.WorkflowSpec
			if json.Unmarshal(recipient.Workflow, &d) == nil && d.Name == name {
				if _, exists := m.eventWorkflowReceipts[eventWorkflowReceiptKey(work.ID, recipient.ID)]; !exists {
					out.RetainedEvents++
				}
			}
		}
	}
	history := m.storedWorkflowFailureHistory[key]
	for i := len(history) - 1; i >= 0 && len(out.History) < api.StoredWorkflowFailurePolicyHistoryLimit; i-- {
		out.History = append(out.History, history[i])
	}
	return out
}
func (m *MemStore) GetStoredWorkflowFailurePolicy(ctx context.Context, appID, name string) (api.StoredWorkflowFailurePolicyResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowFailurePolicyResponse{}, err
	}
	if err := m.failurePolicyTargetLocked(appID, name); err != nil {
		return api.StoredWorkflowFailurePolicyResponse{}, err
	}
	return m.failurePolicyResponseLocked(appID, name, time.Now().UTC()), nil
}
func (m *MemStore) SetStoredWorkflowFailurePolicy(ctx context.Context, appID, name string, r api.SetStoredWorkflowFailurePolicyRequest) (api.StoredWorkflowFailurePolicyResponse, error) {
	if !validStoredWorkflowFailurePolicy(name, r) {
		return api.StoredWorkflowFailurePolicyResponse{}, ErrStoredWorkflowInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowFailurePolicyResponse{}, err
	}
	if err := m.failurePolicyTargetLocked(appID, name); err != nil {
		return api.StoredWorkflowFailurePolicyResponse{}, err
	}
	account := m.accounts[m.apps[appID].AccountID]
	if !account.Active() || *r.Enabled && !account.Plan.WorkflowsAllowed() {
		return api.StoredWorkflowFailurePolicyResponse{}, ErrWorkflowEventTargetUnavailable
	}
	key := appID + "/" + name
	p := m.storedWorkflowFailurePolicies[key]
	if p.Version != r.ExpectedVersion {
		return api.StoredWorkflowFailurePolicyResponse{}, ErrStoredWorkflowFailurePolicyConflict
	}
	if m.storedWorkflowFailurePolicies == nil {
		m.storedWorkflowFailurePolicies = map[string]api.StoredWorkflowFailurePolicy{}
	}
	if m.storedWorkflowFailureGuards == nil {
		m.storedWorkflowFailureGuards = map[string]storedWorkflowFailureGuard{}
	}
	m.storedWorkflowFailurePolicies[key] = api.StoredWorkflowFailurePolicy{Version: p.Version + 1, Enabled: *r.Enabled, FailureThreshold: r.FailureThreshold, MinCompletedRuns: r.MinCompletedRuns, WindowSeconds: r.WindowSeconds}
	if _, ok := m.storedWorkflowFailureGuards[key]; !ok {
		m.storedWorkflowFailureGuards[key] = storedWorkflowFailureGuard{MonitoringSince: time.Now().UTC()}
	}
	return m.failurePolicyResponseLocked(appID, name, time.Now().UTC()), nil
}
func (m *MemStore) ResumeStoredWorkflowFailurePause(ctx context.Context, appID, name, actor string, r api.ResumeStoredWorkflowFailurePauseRequest) (api.StoredWorkflowFailurePolicyResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowFailurePolicyResponse{}, err
	}
	if err := m.failurePolicyTargetLocked(appID, name); err != nil {
		return api.StoredWorkflowFailurePolicyResponse{}, err
	}
	if actor == "" || m.apps[appID].AccountID != actor {
		return api.StoredWorkflowFailurePolicyResponse{}, ErrNotFound
	}
	key := appID + "/" + name
	g := m.storedWorkflowFailureGuards[key]
	if g.PausedAt == nil || g.Generation != r.ExpectedGeneration {
		return api.StoredWorkflowFailurePolicyResponse{}, ErrStoredWorkflowFailurePolicyConflict
	}
	previous := m.failurePolicyResponseLocked(appID, name, time.Now().UTC())
	g.Generation++
	g.PausedAt = nil
	g.MonitoringSince = time.Now().UTC()
	m.storedWorkflowFailureGuards[key] = g
	m.storedWorkflowFailureHistory[key] = append(m.storedWorkflowFailureHistory[key], api.StoredWorkflowFailureTransition{Generation: g.Generation, State: "resumed", Reason: "operator_resume", RecordedAt: g.MonitoringSince, Failures: previous.ObservedFailures, CompletedRuns: previous.ObservedCompletedRuns, PolicyVersion: previous.Policy.Version, ActorAccountID: actor})
	m.rearmFailureSchedulesLocked(appID, name)
	return m.failurePolicyResponseLocked(appID, name, time.Now().UTC()), nil
}
func (m *MemStore) rearmFailureSchedulesLocked(appID, name string) {
	delete(m.workflowSchedules, appID+"/"+name)
	for key, c := range m.workflowTenantSchedules {
		if c.AppID == appID && c.WorkflowName == name {
			c.DeploymentID = ""
			c.Status = WorkflowScheduleArmed
			c.LastRunID = ""
			c.ScheduledFor = nil
			m.workflowTenantSchedules[key] = c
		}
	}
}
func (m *MemStore) ListStoredWorkflowFailurePauses(ctx context.Context, appID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []string{}
	for key, g := range m.storedWorkflowFailureGuards {
		if g.PausedAt != nil && strings.HasPrefix(key, appID+"/") {
			out = append(out, strings.TrimPrefix(key, appID+"/"))
		}
	}
	sort.Strings(out)
	return out, nil
}
func (m *MemStore) EvaluateStoredWorkflowFailurePolicies(ctx context.Context, owner, after string, limit int, now time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return after, err
	}
	if limit < 1 || limit > api.StoredWorkflowFailurePolicyBatch {
		return after, ErrStoredWorkflowInvalid
	}
	keys := []string{}
	for key, p := range m.storedWorkflowFailurePolicies {
		appID, _, _ := strings.Cut(key, "/")
		a := m.apps[appID]
		ac := m.accounts[a.AccountID]
		if p.Enabled && key > after && a.Status != AppDeleted && ac.Active() && ac.Plan.WorkflowsAllowed() && (owner == "" || a.NodeID == owner) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		g, exists := m.storedWorkflowFailureGuards[key]
		if !exists {
			if m.storedWorkflowFailureGuards == nil {
				m.storedWorkflowFailureGuards = map[string]storedWorkflowFailureGuard{}
			}
			g.MonitoringSince = now.UTC()
			m.storedWorkflowFailureGuards[key] = g
		}
		if g.PausedAt != nil {
			continue
		}
		appID, name, _ := strings.Cut(key, "/")
		if m.failurePolicyTargetLocked(appID, name) != nil {
			continue
		}
		out := m.failurePolicyResponseLocked(appID, name, now)
		if out.ObservedFailures < int64(out.Policy.FailureThreshold) || out.ObservedCompletedRuns < int64(out.Policy.MinCompletedRuns) {
			continue
		}
		m.latchStoredWorkflowFailureLocked(appID, name, out, now)
	}
	if len(keys) == limit {
		return keys[len(keys)-1], nil
	}
	return "", nil
}
func (m *MemStore) enqueueStoredWorkflowFailurePauseLocked(appID, name string, generation int64, out api.StoredWorkflowFailurePolicyResponse, at time.Time) {
	app := m.apps[appID]
	recipients := []string{}
	for id, w := range m.appWebhooks {
		if w.Scope == AppWebhookScopeApp && w.Enabled && w.AppID == appID && w.AccountID == app.AccountID && appWebhookMatches(w.EventFilter, AppWebhookEventStoredWorkflowPaused) {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return
	}
	sort.Strings(recipients)
	payload, _ := json.Marshal(map[string]any{"app_id": appID, "stored_workflow_name": name, "generation": generation, "reason": "failure_threshold", "failures": out.ObservedFailures, "completed_runs": out.ObservedCompletedRuns, "policy_version": out.Policy.Version, "paused_at": at})
	id := uuid.NewString()
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: app.AccountID, AppID: appID, Event: AppWebhookEventStoredWorkflowPaused, SourceID: id, Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: at}
}
func (m *MemStore) mergeRuntimeStoredWorkflowDefinitionsLocked(appID string, manifest json.RawMessage) (json.RawMessage, error) {
	raw, err := mergeStoredWorkflowDefinitions(manifest, m.storedWorkflowRecordsLocked(appID))
	if err != nil {
		return nil, err
	}
	var definitions []api.WorkflowSpec
	if err = json.Unmarshal(raw, &definitions); err != nil {
		return nil, err
	}
	for i := range definitions {
		d := &definitions[i]
		if m.storedWorkflowFailureGuards[appID+"/"+d.Name].PausedAt != nil && d.Trigger != nil && (d.Trigger.Type == "schedule" || d.Trigger.Type == "event") {
			enabled := false
			d.Trigger.Enabled = &enabled
		}
	}
	return json.Marshal(definitions)
}

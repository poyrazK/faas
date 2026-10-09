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

var ErrAutomationFailurePolicyConflict = errors.New("automation failure policy or pause generation changed; reload before changing it")

type AutomationFailurePolicyStore interface {
	GetAutomationFailurePolicy(context.Context, string, string) (api.AutomationFailurePolicyResponse, error)
	SetAutomationFailurePolicy(context.Context, string, string, api.SetAutomationFailurePolicyRequest) (api.AutomationFailurePolicyResponse, error)
	ResumeAutomationFailurePause(context.Context, string, string, string, api.ResumeAutomationFailurePauseRequest) (api.AutomationFailurePolicyResponse, error)
	ListAutomationFailurePauses(context.Context, string) ([]string, error)
	EvaluateAutomationFailurePolicies(context.Context, string, string, int, time.Time) (string, error)
}
type automationFailureGuard struct {
	Generation      int64
	MonitoringSince time.Time
	PausedAt        *time.Time
}

func defaultAutomationFailurePolicy() api.AutomationFailurePolicy {
	return api.AutomationFailurePolicy{FailureThreshold: api.AutomationFailurePolicyDefaultThreshold, MinCompletedRuns: api.AutomationFailurePolicyDefaultMinRuns, WindowSeconds: api.AutomationFailurePolicyDefaultWindowSeconds}
}
func validAutomationFailurePolicy(name string, r api.SetAutomationFailurePolicyRequest) bool {
	return name != "" && strings.TrimSpace(name) == name && !strings.ContainsAny(name, "/\\\x00") && len(name) <= api.AutomationNameMaxBytes && r.Enabled != nil && r.ExpectedVersion >= 0 && r.FailureThreshold > 0 && r.FailureThreshold <= api.AutomationFailurePolicyMaxCount && r.MinCompletedRuns > 0 && r.MinCompletedRuns <= api.AutomationFailurePolicyMaxCount && r.WindowSeconds >= api.AutomationFailurePolicyMinWindowSeconds && r.WindowSeconds <= api.AutomationFailurePolicyMaxWindowSeconds
}
func (m *MemStore) failurePolicyTargetLocked(appID, name string) error {
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return ErrNotFound
	}
	dep := m.automationDeploymentLocked(appID)
	raw, err := mergeAutomationDefinitions(dep.Workflows, m.automationRecordsLocked(appID))
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
func (m *MemStore) failurePolicyResponseLocked(appID, name string, now time.Time) api.AutomationFailurePolicyResponse {
	key := appID + "/" + name
	p, exists := m.automationFailurePolicies[key]
	if !exists {
		p = defaultAutomationFailurePolicy()
	}
	g := m.automationFailureGuards[key]
	out := api.AutomationFailurePolicyResponse{Policy: p, Generation: g.Generation, History: []api.AutomationFailureTransition{}}
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
	history := m.automationFailureHistory[key]
	for i := len(history) - 1; i >= 0 && len(out.History) < api.AutomationFailurePolicyHistoryLimit; i-- {
		out.History = append(out.History, history[i])
	}
	return out
}
func (m *MemStore) GetAutomationFailurePolicy(ctx context.Context, appID, name string) (api.AutomationFailurePolicyResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationFailurePolicyResponse{}, err
	}
	if err := m.failurePolicyTargetLocked(appID, name); err != nil {
		return api.AutomationFailurePolicyResponse{}, err
	}
	return m.failurePolicyResponseLocked(appID, name, time.Now().UTC()), nil
}
func (m *MemStore) SetAutomationFailurePolicy(ctx context.Context, appID, name string, r api.SetAutomationFailurePolicyRequest) (api.AutomationFailurePolicyResponse, error) {
	if !validAutomationFailurePolicy(name, r) {
		return api.AutomationFailurePolicyResponse{}, ErrAutomationInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationFailurePolicyResponse{}, err
	}
	if err := m.failurePolicyTargetLocked(appID, name); err != nil {
		return api.AutomationFailurePolicyResponse{}, err
	}
	account := m.accounts[m.apps[appID].AccountID]
	if !account.Active() || *r.Enabled && !account.Plan.WorkflowsAllowed() {
		return api.AutomationFailurePolicyResponse{}, ErrWorkflowEventTargetUnavailable
	}
	key := appID + "/" + name
	p := m.automationFailurePolicies[key]
	if p.Version != r.ExpectedVersion {
		return api.AutomationFailurePolicyResponse{}, ErrAutomationFailurePolicyConflict
	}
	if m.automationFailurePolicies == nil {
		m.automationFailurePolicies = map[string]api.AutomationFailurePolicy{}
	}
	if m.automationFailureGuards == nil {
		m.automationFailureGuards = map[string]automationFailureGuard{}
	}
	m.automationFailurePolicies[key] = api.AutomationFailurePolicy{Version: p.Version + 1, Enabled: *r.Enabled, FailureThreshold: r.FailureThreshold, MinCompletedRuns: r.MinCompletedRuns, WindowSeconds: r.WindowSeconds}
	if _, ok := m.automationFailureGuards[key]; !ok {
		m.automationFailureGuards[key] = automationFailureGuard{MonitoringSince: time.Now().UTC()}
	}
	return m.failurePolicyResponseLocked(appID, name, time.Now().UTC()), nil
}
func (m *MemStore) ResumeAutomationFailurePause(ctx context.Context, appID, name, actor string, r api.ResumeAutomationFailurePauseRequest) (api.AutomationFailurePolicyResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationFailurePolicyResponse{}, err
	}
	if err := m.failurePolicyTargetLocked(appID, name); err != nil {
		return api.AutomationFailurePolicyResponse{}, err
	}
	if actor == "" || m.apps[appID].AccountID != actor {
		return api.AutomationFailurePolicyResponse{}, ErrNotFound
	}
	key := appID + "/" + name
	g := m.automationFailureGuards[key]
	if !(g.PausedAt != nil && g.Generation == r.ExpectedGeneration) {
		return api.AutomationFailurePolicyResponse{}, ErrAutomationFailurePolicyConflict
	}
	previous := m.failurePolicyResponseLocked(appID, name, time.Now().UTC())
	g.Generation++
	g.PausedAt = nil
	g.MonitoringSince = time.Now().UTC()
	m.automationFailureGuards[key] = g
	m.automationFailureHistory[key] = append(m.automationFailureHistory[key], api.AutomationFailureTransition{Generation: g.Generation, State: "resumed", Reason: "operator_resume", RecordedAt: g.MonitoringSince, Failures: previous.ObservedFailures, CompletedRuns: previous.ObservedCompletedRuns, PolicyVersion: previous.Policy.Version, ActorAccountID: actor})
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
func (m *MemStore) ListAutomationFailurePauses(ctx context.Context, appID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []string{}
	for key, g := range m.automationFailureGuards {
		if g.PausedAt != nil && strings.HasPrefix(key, appID+"/") {
			out = append(out, strings.TrimPrefix(key, appID+"/"))
		}
	}
	sort.Strings(out)
	return out, nil
}
func (m *MemStore) EvaluateAutomationFailurePolicies(ctx context.Context, owner, after string, limit int, now time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return after, err
	}
	if limit < 1 || limit > api.AutomationFailurePolicyBatch {
		return after, ErrAutomationInvalid
	}
	keys := []string{}
	for key, p := range m.automationFailurePolicies {
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
		g, exists := m.automationFailureGuards[key]
		if !exists {
			if m.automationFailureGuards == nil {
				m.automationFailureGuards = map[string]automationFailureGuard{}
			}
			g.MonitoringSince = now.UTC()
			m.automationFailureGuards[key] = g
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
		at := now.UTC()
		g.Generation++
		g.PausedAt = &at
		m.automationFailureGuards[key] = g
		if m.automationFailureHistory == nil {
			m.automationFailureHistory = map[string][]api.AutomationFailureTransition{}
		}
		m.automationFailureHistory[key] = append(m.automationFailureHistory[key], api.AutomationFailureTransition{Generation: g.Generation, State: "paused", Reason: "failure_threshold", RecordedAt: at, Failures: out.ObservedFailures, CompletedRuns: out.ObservedCompletedRuns, PolicyVersion: out.Policy.Version})
		m.enqueueAutomationFailurePauseLocked(appID, name, g.Generation, out, at)
	}
	if len(keys) == limit {
		return keys[len(keys)-1], nil
	}
	return "", nil
}
func (m *MemStore) enqueueAutomationFailurePauseLocked(appID, name string, generation int64, out api.AutomationFailurePolicyResponse, at time.Time) {
	app := m.apps[appID]
	recipients := []string{}
	for id, w := range m.appWebhooks {
		if w.Scope == AppWebhookScopeApp && w.Enabled && w.AppID == appID && w.AccountID == app.AccountID && appWebhookMatches(w.EventFilter, AppWebhookEventAutomationPaused) {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return
	}
	sort.Strings(recipients)
	payload, _ := json.Marshal(map[string]any{"app_id": appID, "automation_name": name, "generation": generation, "reason": "failure_threshold", "failures": out.ObservedFailures, "completed_runs": out.ObservedCompletedRuns, "policy_version": out.Policy.Version, "paused_at": at})
	id := uuid.NewString()
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: app.AccountID, AppID: appID, Event: AppWebhookEventAutomationPaused, SourceID: id, Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: at}
}
func (m *MemStore) mergeRuntimeAutomationDefinitionsLocked(appID string, manifest json.RawMessage) (json.RawMessage, error) {
	raw, err := mergeAutomationDefinitions(manifest, m.automationRecordsLocked(appID))
	if err != nil {
		return nil, err
	}
	var definitions []api.WorkflowSpec
	if err = json.Unmarshal(raw, &definitions); err != nil {
		return nil, err
	}
	for i := range definitions {
		d := &definitions[i]
		if m.automationFailureGuards[appID+"/"+d.Name].PausedAt != nil && d.Trigger != nil && (d.Trigger.Type == "schedule" || d.Trigger.Type == "event") {
			enabled := false
			d.Trigger.Enabled = &enabled
		}
	}
	return json.Marshal(definitions)
}

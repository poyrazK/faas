package state

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) PutRunBudget(_ context.Context, account, kind, ref string, req api.SetRunBudgetRequest) (api.RunBudgetPolicy, error) {
	if !validRunPolicy(kind, ref, req) {
		return api.RunBudgetPolicy{}, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[account]
	if !ok {
		return api.RunBudgetPolicy{}, ErrNotFound
	}
	key := runControlKey(account, kind, ref)
	old, exists := m.runBudgets[key]
	if old.Revision != req.ExpectedRevision {
		return api.RunBudgetPolicy{}, ErrRunControlConflict
	}
	if kind == "task" {
		found := false
		for _, v := range m.executionTaskVersions {
			if v.AccountID == account && v.Name == ref {
				found = true
				break
			}
		}
		if !found {
			return api.RunBudgetPolicy{}, ErrNotFound
		}
	} else if !exists {
		count := 0
		for k, p := range m.runBudgets {
			if p.Kind == "customer" && k == runControlKey(account, p.Kind, p.Ref) {
				count++
			}
		}
		if count >= api.RunCustomersByPlan[a.Plan.PlanIndex()] {
			return api.RunBudgetPolicy{}, &ExecutionTaskQuotaError{"run_customers", int64(api.RunCustomersByPlan[a.Plan.PlanIndex()]), int64(count + 1)}
		}
	}
	if m.runBudgets == nil {
		m.runBudgets = map[string]api.RunBudgetPolicy{}
	}
	p := cloneRunPolicy(api.RunBudgetPolicy{Kind: kind, Ref: ref, Revision: old.Revision + 1, Enabled: req.Enabled, Limits: req.Limits})
	m.runBudgets[key] = p
	return m.runPolicyLocked(account, p, time.Now().UTC()), nil
}
func (m *MemStore) runPolicyLocked(account string, p api.RunBudgetPolicy, now time.Time) api.RunBudgetPolicy {
	p = cloneRunPolicy(p)
	p.Usage = m.runUsageLocked(account, p.Kind, p.Ref, now)
	for _, a := range m.runBudgetAlerts {
		if a.Kind == p.Kind && a.Ref == p.Ref && a.Day == p.Usage.Day && a.EventID == runBudgetAlertID(account, a) {
			p.Alerts = append(p.Alerts, a)
		}
	}
	sort.Slice(p.Alerts, func(i, j int) bool { return p.Alerts[i].EventID < p.Alerts[j].EventID })
	return p
}
func (m *MemStore) GetRunBudget(_ context.Context, account, kind, ref string) (api.RunBudgetPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.runBudgets[runControlKey(account, kind, ref)]
	if !ok {
		return p, ErrNotFound
	}
	return m.runPolicyLocked(account, p, time.Now().UTC()), nil
}
func (m *MemStore) runSubjectIDLocked(account, kind, id string) (string, error) {
	if kind == "agent_endpoint" {
		ep, ok := m.managedRealtimeEndpoints[id]
		if !ok || ep.AccountID != account {
			return "", ErrNotFound
		}
		return id, nil
	}
	key, ok := m.keys[id]
	if !ok || key.AccountID != account || key.RunsPrincipalID == "" {
		return "", ErrNotFound
	}
	return key.RunsPrincipalID, nil
}
func (m *MemStore) PutRunBinding(_ context.Context, account, kind, id string, req api.SetRunAttributionBindingRequest) (api.RunAttributionBinding, error) {
	if !validRunSubject(kind, id) || !api.ValidRunControlRef(req.CustomerRef) || (req.AgentRef != "" && !api.ValidRunControlRef(req.AgentRef)) || req.ExpectedRevision < 0 {
		return api.RunAttributionBinding{}, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[account]
	if !ok {
		return api.RunAttributionBinding{}, ErrNotFound
	}
	internalID, err := m.runSubjectIDLocked(account, kind, id)
	if err != nil {
		return api.RunAttributionBinding{}, err
	}
	if _, ok := m.runBudgets[runControlKey(account, "customer", req.CustomerRef)]; !ok {
		return api.RunAttributionBinding{}, ErrNotFound
	}
	key := runControlKey(account, kind, internalID)
	old, exists := m.runBindings[key]
	if old.Revision != req.ExpectedRevision {
		return api.RunAttributionBinding{}, ErrRunControlConflict
	}
	if !exists {
		count := 0
		for k := range m.runBindings {
			if strings.HasPrefix(k, account+"\x00") {
				count++
			}
		}
		if count >= api.RunBindingsByPlan[a.Plan.PlanIndex()] {
			return api.RunAttributionBinding{}, &ExecutionTaskQuotaError{"run_bindings", int64(api.RunBindingsByPlan[a.Plan.PlanIndex()]), int64(count + 1)}
		}
	}
	if req.AgentRef == "" {
		req.AgentRef = old.AgentRef
		if req.AgentRef == "" {
			req.AgentRef = kind + ":" + id
		}
	}
	b := api.RunAttributionBinding{Enabled: true, SubjectKind: kind, SubjectID: id, RunAttribution: api.RunAttribution{CustomerRef: req.CustomerRef, AgentRef: req.AgentRef}, Revision: old.Revision + 1}
	if m.runBindings == nil {
		m.runBindings = map[string]api.RunAttributionBinding{}
	}
	m.runBindings[key] = b
	return b, nil
}
func (m *MemStore) GetRunBinding(_ context.Context, account, kind, id string) (api.RunAttributionBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	internalID, err := m.runSubjectIDLocked(account, kind, id)
	if err != nil {
		return api.RunAttributionBinding{}, err
	}
	b, ok := m.runBindings[runControlKey(account, kind, internalID)]
	if !ok {
		return b, ErrNotFound
	}
	b.SubjectID = id
	return b, nil
}
func (m *MemStore) DeleteRunBinding(_ context.Context, account, kind, id string, revision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	internalID, err := m.runSubjectIDLocked(account, kind, id)
	if err != nil {
		return err
	}
	key := runControlKey(account, kind, internalID)
	b, ok := m.runBindings[key]
	if !ok {
		return ErrNotFound
	}
	if revision < 1 || b.Revision != revision {
		return ErrRunControlConflict
	}
	b.Enabled = false
	b.Revision++
	m.runBindings[key] = b
	return nil
}
func (m *MemStore) runUsageLocked(account, kind, ref string, at time.Time) api.RunBudgetUsage {
	day := runControlDay(at)
	u := api.RunBudgetUsage{Day: day.Format(time.DateOnly)}
	for id, r := range m.runReservations {
		if r.AccountID != account || kind == "customer" && r.Attribution.CustomerRef != ref || kind == "task" && r.TaskName != ref {
			continue
		}
		ledger, settled := m.executionUsageLedger[id]
		if !settled {
			u.Active++
		}
		if r.AdmittedAt.Before(day) || !r.AdmittedAt.Before(day.AddDate(0, 0, 1)) {
			continue
		}
		u.Invocations++
		if settled {
			u.ComputeUsedMBMS += settledRunCompute(r.ReservedMBMS, int64(m.executions[id].Limits.TimeoutMS), ledger.WallTimeMS)
		} else {
			u.ComputeReservedMBMS += r.ReservedMBMS
		}
	}
	return u
}
func (m *MemStore) prepareRunControlsLocked(p CreateExecutionParams, task ExecutionTaskVersion) (runControlReservation, []api.RunBudgetPolicy, error) {
	bindings := []api.RunAttributionBinding{}
	if p.RunsPrincipalID != nil {
		if b, ok := m.runBindings[runControlKey(p.AccountID, "api_key", *p.RunsPrincipalID)]; ok && b.Enabled {
			bindings = append(bindings, b)
		}
	}
	if endpoint := runCallbackEndpoint(p.Task); endpoint != "" {
		if b, ok := m.runBindings[runControlKey(p.AccountID, "agent_endpoint", endpoint)]; ok && b.Enabled {
			bindings = append(bindings, b)
		}
	}
	a, err := runAttribution(p, bindings)
	if err != nil {
		return runControlReservation{}, nil, err
	}
	r := runControlReservation{AccountID: p.AccountID, Attribution: a, TaskName: task.Name, ReservedMBMS: runReservationMBMS(p), AdmittedAt: time.Now().UTC()}
	policies := []api.RunBudgetPolicy{}
	for _, target := range []struct{ kind, ref string }{{"customer", a.CustomerRef}, {"task", task.Name}} {
		if target.ref == "" {
			continue
		}
		policy, ok := m.runBudgets[runControlKey(p.AccountID, target.kind, target.ref)]
		if !ok {
			if target.kind == "customer" {
				return r, nil, ErrNotFound
			}
			continue
		}
		if err := checkRunBudget(policy, m.runUsageLocked(p.AccountID, target.kind, target.ref, r.AdmittedAt), r.ReservedMBMS); err != nil {
			return r, nil, err
		}
		policies = append(policies, policy)
	}
	return r, policies, nil
}
func (m *MemStore) addRunControlsLocked(r *Execution, reservation runControlReservation, policies []api.RunBudgetPolicy) {
	if m.runReservations == nil {
		m.runReservations = map[string]runControlReservation{}
	}
	m.runReservations[r.ID] = reservation
	if reservation.Attribution.CustomerRef != "" || reservation.Attribution.AgentRef != "" {
		a := reservation.Attribution
		r.Attribution = &a
	}
	now := time.Now().UTC()
	for _, p := range policies {
		for _, a := range runBudgetAlerts(r.AccountID, p, m.runUsageLocked(r.AccountID, p.Kind, p.Ref, now), now) {
			if _, exists := m.runBudgetAlerts[a.EventID]; exists {
				continue
			}
			if m.runBudgetAlerts == nil {
				m.runBudgetAlerts = map[string]api.RunBudgetAlert{}
			}
			m.runBudgetAlerts[a.EventID] = a
			m.enqueueRunBudgetAlertLocked(r.AccountID, a, now)
		}
	}
}
func (m *MemStore) enqueueRunBudgetAlertLocked(account string, a api.RunBudgetAlert, now time.Time) {
	body, _ := json.Marshal(a)
	for _, hook := range m.appWebhooks {
		if hook.AccountID != account || hook.Scope != AppWebhookScopeAccount || !hook.Enabled || !slices.Contains(hook.EventFilter, api.RunBudgetThresholdWebhookEvent) {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{ID: id, WebhookID: hook.ID, AccountID: account, Event: AppWebhookEvent(api.RunBudgetThresholdWebhookEvent), Payload: body, Status: AppWebhookDeliveryPending, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		if m.appWebhookDeliverySources == nil {
			m.appWebhookDeliverySources = map[string]string{}
		}
		m.appWebhookDeliverySources[id] = a.EventID
	}
}
func (m *MemStore) ListRunHistory(_ context.Context, q RunHistoryQuery) ([]api.RunHistoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []Execution{}
	for _, r := range m.executions {
		if !matchesRunHistory(r, q) {
			continue
		}
		rows = append(rows, cloneExecution(r))
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID > rows[j].ID
	})
	limit := q.Limit
	if limit < 1 || limit > api.RunHistoryPageMax+1 {
		limit = api.RunHistoryPageMax + 1
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]api.RunHistoryEntry, 0, len(rows))
	now := time.Now().UTC()
	for _, r := range rows {
		out = append(out, runHistoryEntry(r, now))
	}
	return out, nil
}
func matchesRunHistory(r Execution, q RunHistoryQuery) bool {
	f := q.Filter
	if r.AccountID != q.AccountID || !q.Wide && (r.RunsPrincipalID == nil || *r.RunsPrincipalID != q.PrincipalID) {
		return false
	}
	if f.TaskName != "" && (r.Task == nil || r.Task.Name != f.TaskName) || f.TaskVersion != 0 && (r.Task == nil || r.Task.Version != f.TaskVersion) {
		return false
	}
	if f.CustomerRef != "" && (r.Attribution == nil || r.Attribution.CustomerRef != f.CustomerRef) || f.AgentRef != "" && (r.Attribution == nil || r.Attribution.AgentRef != f.AgentRef) {
		return false
	}
	if f.Status != "" && r.Status != f.Status || !f.Since.IsZero() && r.CreatedAt.Before(f.Since) || !f.Until.IsZero() && !r.CreatedAt.Before(f.Until) {
		return false
	}
	return q.Before.IsZero() || r.CreatedAt.Before(q.Before) || r.CreatedAt.Equal(q.Before) && r.ID < q.BeforeID
}

var _ RunControlStore = (*MemStore)(nil)

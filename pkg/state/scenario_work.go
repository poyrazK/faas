// adr: 20261017201011
package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type scenarioManagedJob struct {
	Job     rehearsalwork.Job
	Payload json.RawMessage
}

func finishScenarioManagedWork(e *rehearsalwork.Evidence, failed bool) {
	e.Status = "complete"
	if failed {
		e.Status, e.Reason = "incomplete", "managed work was unsupported, admitted after closing, lost or changed deployment"
		return
	}
	for _, j := range e.Jobs {
		if j.State == "pending" || j.State == "dispatching" {
			e.Status, e.Reason = "pending", "managed jobs or retries remain unfinished"
			return
		}
	}
	if e.Plan.PostgresFault != nil && e.PostgresFault != nil && e.PostgresFault.State != "recovered" && e.PostgresFault.State != "failed" {
		e.Status, e.Reason = "pending", "PostgreSQL fault activation or recovery is still pending"
		return
	}
	if err := e.Validate(); err != nil {
		e.Status, e.Reason = "incomplete", "managed work is incomplete: "+err.Error()
	}
}

func newScenarioManagedWork(run, raw string) *rehearsalwork.Evidence {
	p := rehearsalwork.Decode(raw)
	names := make([]string, 0, len(p.Participants))
	for _, v := range p.Participants {
		names = append(names, v.Workload)
	}
	_, digest, _ := rehearsalwork.Normalize(p, names)
	return &rehearsalwork.Evidence{Version: 1, RunID: run, PlanSHA256: digest, Plan: p, Closing: true, Bindings: []rehearsalwork.Binding{}, Jobs: []rehearsalwork.Job{}}
}

func addScenarioManagedWorkSnapshot(ctx context.Context, tx pgx.Tx, snapshot *api.ScenarioActionSnapshot, account string, policies []sqlc.ScenarioActionPoliciesForRunRow) error {
	if len(policies) == 0 || len(policies[0].ManagedWorkPlan) == 0 {
		return nil
	}
	e := newScenarioManagedWork(snapshot.RunID, string(policies[0].ManagedWorkPlan))
	snapshot.ManagedWork = e
	q := sqlc.New()
	args := sqlc.ScenarioManagedWorkBindingsParams{AccountID: account, RunID: snapshot.RunID}
	bindings, err := q.ScenarioManagedWorkBindings(ctx, tx, args)
	if err != nil {
		return err
	}
	valid, err := q.ScenarioManagedWorkInventoryValid(ctx, tx, sqlc.ScenarioManagedWorkInventoryValidParams{AccountID: account, RunID: snapshot.RunID})
	if err != nil {
		return err
	}
	failed := !valid.Valid || !valid.Bool
	for _, r := range bindings {
		p, _ := e.Plan.Participant(r.WorkloadName)
		b := rehearsalwork.Binding{Workload: r.WorkloadName, AppID: r.AppID, DeploymentID: r.DeploymentID}
		if p.Postgres != "" {
			b.PostgresResourceID = r.PostgresResourceID
		}
		if p.Objects != "" {
			b.ObjectsResourceID = r.ObjectsResourceID
		}
		e.Bindings = append(e.Bindings, b)
		failed = failed || r.ManagedWorkFailed || r.Deployments != 1
	}
	jobs, err := q.ScenarioManagedWorkJobs(ctx, tx, sqlc.ScenarioManagedWorkJobsParams{AccountID: account, RunID: snapshot.RunID})
	if err != nil {
		return err
	}
	for _, r := range jobs {
		var j rehearsalwork.Job
		if json.Unmarshal(r.Document, &j) != nil {
			return ErrConflict
		}
		j.PayloadSHA256 = rehearsalwork.PayloadHash(r.Payload)
		e.Jobs = append(e.Jobs, j)
	}
	faults, err := q.ScenarioWorkerFaultsForRun(ctx, tx, sqlc.ScenarioWorkerFaultsForRunParams{AccountID: account, RunID: snapshot.RunID})
	if err != nil {
		return err
	}
	if len(faults) > 1 {
		failed = true
	}
	for _, r := range faults {
		f := workerFaultRow(sqlc.ScenarioWorkerFaultRow(r))
		e.WorkerFault = &f
		failed = failed || f.State == "failed"
	}
	e.BindRecovery()
	if err := addScenarioPostgresFaultSnapshot(ctx, tx, e, account); err != nil {
		return err
	}
	finishScenarioManagedWork(e, failed)
	return nil
}

func (m *MemStore) failScenarioManagedWorkLocked(app string) {
	p, ok := m.scenarioActionPolicies[app]
	if !ok || p.Member.ManagedWorkJSON == "" || !p.SealedAt.IsZero() {
		return
	}
	for id, v := range m.scenarioActionPolicies {
		if v.Member.AccountID == p.Member.AccountID && v.Member.RunID == p.Member.RunID {
			v.ManagedWorkFailed = true
			m.scenarioActionPolicies[id] = v
		}
	}
}
func (m *MemStore) managedDeploymentLocked(app string) (string, int) {
	dep, count := "", 0
	for _, d := range m.deployments {
		if d.AppID == app {
			dep = d.ID
			count++
		}
	}
	return dep, count
}

func (m *MemStore) admitScenarioManagedWorkLocked(inv *Invocation) error {
	if m.suspendWorkPendingLocked(inv.AppID) || m.suspendNewJobBlockedLocked(inv.AppID) {
		return ErrConflict
	}
	p, ok := m.scenarioActionPolicies[inv.AppID]
	if !ok || p.Member.ManagedWorkJSON == "" {
		return nil
	}
	if !p.SealedAt.IsZero() {
		return ErrConflict
	}
	participant, _ := rehearsalwork.Decode(p.Member.ManagedWorkJSON).Participant(p.Member.Workload)
	plan := rehearsalwork.Decode(p.Member.ManagedWorkJSON)
	if plan.PostgresFault != nil {
		if p.Member.Workload != plan.PostgresFault.Workload {
			return ErrConflict
		}
		for _, j := range m.invocations {
			w := m.scenarioActionPolicies[j.AppID].Member
			if w.AccountID == p.Member.AccountID && w.RunID == p.Member.RunID {
				return ErrConflict
			}
		}
	}
	_, count := m.managedDeploymentLocked(inv.AppID)
	jobs := 0
	for _, j := range m.scenarioManagedWork {
		if v := m.scenarioActionPolicies[j.Job.AppID]; v.Member.AccountID == p.Member.AccountID && v.Member.RunID == p.Member.RunID {
			jobs++
		}
	}
	if p.ManagedWorkClosing || p.RecorderClosing || !participant.Async || inv.Source != InvocationAsyncInvoke || inv.State != InvocationPending || inv.Attempts != 0 || inv.ReplayGeneration != 0 || inv.AccountID != p.Member.AccountID || count != 1 || inv.WorkPolicyName != "" || len(inv.Payload) > rehearsalwork.MaxPayloadBytes || len(inv.Path) > 2048 || jobs >= rehearsalwork.MaxJobs || !eligibleScenarioTestApp(m.apps[inv.AppID], p.Member.AccountID) {
		m.failScenarioManagedWorkLocked(inv.AppID)
		inv.State = InvocationCancelled
		inv.LastError = "rehearsal: unsupported or closed managed-work admission"
		now := time.Now()
		inv.CompletedAt = &now
	}
	return nil
}

func (m *MemStore) captureScenarioManagedWorkLocked(old, inv Invocation) {
	p, ok := m.scenarioActionPolicies[inv.AppID]
	if !ok || p.Member.ManagedWorkJSON == "" {
		return
	}
	if m.scenarioManagedWork == nil {
		m.scenarioManagedWork = map[string]scenarioManagedJob{}
	}
	dep, count := m.managedDeploymentLocked(inv.AppID)
	if old.ID == "" {
		jobs := 0
		for _, j := range m.scenarioManagedWork {
			v := m.scenarioActionPolicies[j.Job.AppID]
			if v.Member.AccountID == p.Member.AccountID && v.Member.RunID == p.Member.RunID {
				jobs++
			}
		}
		if jobs >= rehearsalwork.MaxJobs {
			m.failScenarioManagedWorkLocked(inv.AppID)
			return
		}
		payload := append(json.RawMessage(nil), inv.Payload...)
		if len(payload) > rehearsalwork.MaxPayloadBytes || len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		m.scenarioManagedWork[inv.ID] = scenarioManagedJob{Job: rehearsalwork.Job{ID: inv.ID, Workload: p.Member.Workload, AppID: inv.AppID, DeploymentID: dep, Method: inv.Method, Path: inv.Path, State: string(inv.State), Attempts: []rehearsalwork.Attempt{}}, Payload: payload}
		return
	}
	r, exists := m.scenarioManagedWork[inv.ID]
	if !exists {
		m.failScenarioManagedWorkLocked(inv.AppID)
		return
	}
	if count != 1 || dep != r.Job.DeploymentID || inv.AppID != old.AppID || inv.AccountID != old.AccountID || inv.Source != old.Source || inv.Method != old.Method || inv.Path != old.Path || string(inv.Payload) != string(old.Payload) || inv.ReplayGeneration != old.ReplayGeneration {
		m.failScenarioManagedWorkLocked(inv.AppID)
	}
	if old.State == InvocationDispatching && (inv.State != InvocationDispatching || inv.Attempts != old.Attempts) {
		if len(r.Job.Attempts) != old.Attempts || old.Attempts == 0 {
			m.failScenarioManagedWorkLocked(inv.AppID)
		} else {
			r.Job.Attempts[old.Attempts-1].Outcome = invocationAttemptOutcome(old, inv)
		}
	}
	if inv.State == InvocationDispatching && inv.InstanceID != "" && (old.State != InvocationDispatching || inv.Attempts != old.Attempts || len(r.Job.Attempts) == inv.Attempts-1) {
		i, found := m.instances[inv.InstanceID]
		if !found || i.AppID != inv.AppID || i.DeploymentID != dep || inv.Attempts != len(r.Job.Attempts)+1 || len(r.Job.Attempts) >= rehearsalwork.MaxAttempts {
			m.failScenarioManagedWorkLocked(inv.AppID)
		} else {
			r.Job.Attempts = append(r.Job.Attempts, rehearsalwork.Attempt{Number: inv.Attempts, InstanceID: inv.InstanceID, DeploymentID: i.DeploymentID, Outcome: "running"})
		}
	}
	if inv.State == InvocationDispatching && len(r.Job.Attempts) == inv.Attempts && inv.Attempts > 0 && r.Job.Attempts[inv.Attempts-1].InstanceID != inv.InstanceID {
		m.failScenarioManagedWorkLocked(inv.AppID)
	}
	r.Job.State = string(inv.State)
	m.scenarioManagedWork[inv.ID] = r
}

func (m *MemStore) addScenarioManagedWorkSnapshotLocked(snapshot *api.ScenarioActionSnapshot, apps []string) {
	if len(apps) == 0 || m.scenarioActionPolicies[apps[0]].Member.ManagedWorkJSON == "" {
		return
	}
	e := newScenarioManagedWork(snapshot.RunID, m.scenarioActionPolicies[apps[0]].Member.ManagedWorkJSON)
	snapshot.ManagedWork = e
	failed := false
	for _, app := range apps {
		v := m.scenarioActionPolicies[app]
		v.ManagedWorkClosing = true
		m.scenarioActionPolicies[app] = v
		dep, count := m.managedDeploymentLocked(app)
		p, _ := e.Plan.Participant(v.Member.Workload)
		b := rehearsalwork.Binding{Workload: v.Member.Workload, AppID: app, DeploymentID: dep}
		key := v.Member.AccountID + ":" + v.Member.RunID
		if r, ok := m.scenarioPostgres[key]; ok && p.Postgres != "" && r.Status == "ready" && r.ExpiresAt.After(time.Now()) {
			b.PostgresResourceID = r.ID
		}
		if r, ok := m.scenarioObjects[key]; ok && p.Objects != "" && r.Status == "ready" && r.ExpiresAt.After(time.Now()) {
			b.ObjectsResourceID = r.ID
		}
		e.Bindings = append(e.Bindings, b)
		failed = failed || v.ManagedWorkFailed || count != 1
	}
	for _, r := range m.scenarioManagedWork {
		v := m.scenarioActionPolicies[r.Job.AppID]
		if v.Member.RunID != snapshot.RunID || v.Member.AccountID != m.scenarioActionPolicies[apps[0]].Member.AccountID {
			continue
		}
		j := r.Job
		j.Attempts = append([]rehearsalwork.Attempt{}, j.Attempts...)
		j.PayloadSHA256 = rehearsalwork.PayloadHash(r.Payload)
		if _, found := m.invocations[j.ID]; !found {
			failed = true
		}
		e.Jobs = append(e.Jobs, j)
	}
	for _, f := range m.scenarioWorkerFaults {
		if f.RunID == snapshot.RunID && f.AccountID == m.scenarioActionPolicies[apps[0]].Member.AccountID {
			copyFault := f
			e.WorkerFault = &copyFault
			failed = failed || f.State == "failed"
		}
	}
	e.BindRecovery()
	for _, r := range m.scenarioPostgresFaults {
		if r.RunID == snapshot.RunID && r.AccountID == m.scenarioActionPolicies[apps[0]].Member.AccountID {
			f := r.Receipt
			e.PostgresFault = &f
		}
	}
	sort.Slice(e.Bindings, func(i, j int) bool { return e.Bindings[i].Workload < e.Bindings[j].Workload })
	sort.Slice(e.Jobs, func(i, j int) bool { return e.Jobs[i].ID < e.Jobs[j].ID })
	finishScenarioManagedWork(e, failed)
}

func scenarioManagedWorkText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	p := rehearsalwork.Decode(string(body))
	names := make([]string, 0, len(p.Participants))
	for _, v := range p.Participants {
		names = append(names, v.Workload)
	}
	b, _, err := rehearsalwork.Normalize(p, names)
	if err != nil {
		return "invalid"
	}
	return string(b)
}

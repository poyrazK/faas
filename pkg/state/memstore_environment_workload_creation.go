package state

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsWorkloadCreationStore = (*MemStore)(nil)

func (m *MemStore) PrepareEnvironmentGitOpsWorkloads(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentGitOpsStep, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	revision := memory.revisions[memory.source.ApprovedRevisionID]
	desired, err := desiredEnvironmentRevision(revision)
	if err != nil {
		return nil, err
	}
	snapshot := m.gitOpsSnapshotLocked(memory)
	observed, err := compileGitOpsObservation(snapshot, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(memory.source, revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	if plan.Hash != reviewed.Hash {
		return nil, fmt.Errorf("%w: workload creation plan changed after review (current=%s %s; reviewed=%s %s)",
			ErrConflict, plan.Hash, gitOpsPlanChangeSummary(plan), reviewed.Hash, gitOpsPlanChangeSummary(reviewed))
	}
	apps, err := missingEnvironmentWorkloads(memory.source, desired, snapshot, plan)
	if err != nil {
		return nil, err
	}
	if len(apps) == 0 {
		return []EnvironmentGitOpsStep{}, nil
	}
	// Use detached reservation maps so a later quota/capacity/collision failure
	// cannot leave earlier members or service addresses behind.
	oldApps, oldIndex, oldCursors, oldJobs := m.apps, m.serviceAddressIndex, m.serviceAddressCursors, m.jobs
	m.apps = maps.Clone(m.apps)
	m.serviceAddressIndex = maps.Clone(m.serviceAddressIndex)
	m.serviceAddressCursors = maps.Clone(m.serviceAddressCursors)
	m.jobs = maps.Clone(m.jobs)
	committed := false
	defer func() {
		if !committed {
			m.apps, m.serviceAddressIndex, m.serviceAddressCursors, m.jobs = oldApps, oldIndex, oldCursors, oldJobs
		}
	}()
	limits, _ := api.LimitsFor(snapshot.Plan)
	for _, resource := range environmentWorkloadCreationNames(apps) {
		app, err := m.createAppIfUnderQuotaLocked(apps[resource], limits)
		if err != nil {
			return nil, fmt.Errorf("create workload %s: %w", resource, err)
		}
		apps[resource], observed.State.ResourceIDs[resource] = app, app.ID
	}
	if memory.resources == nil {
		memory.resources = map[string]string{}
	}
	if memory.owners == nil {
		memory.owners = map[string]environmentsync.Ownership{}
	}
	for resource, app := range apps {
		memory.resources[resource] = app.ID
	}
	createdPlan := plan
	createdPlan.Changes = environmentWorkloadCreationChanges(plan, apps)
	workloadRows := changedWorkloadIntents(snapshot, createdPlan, observed.State.ResourceIDs, false)
	for _, resource := range environmentWorkloadCreationNames(apps) {
		appModel := apps[resource]
		row, exists := workloadRows[appModel.ID]
		if !exists {
			continue
		}
		row.AccountID = memory.source.AccountID
		if row.Schedule != nil {
			app := gitOpsIntentApp{ID: appModel.ID, Slug: appModel.Slug, Type: appModel.Type,
				WorkloadClass: appModel.WorkloadClass, Manifest: appModel.Manifest, StartCommand: appModel.StartCommand}
			name := strings.TrimPrefix(resource, "workload/")
			row, err = m.syncEnvironmentGitOpsJobLocked(row, app, desired.Definition.Workloads[name], snapshot.Plan)
			if err != nil {
				return nil, fmt.Errorf("prepare scheduled workload %s: %w", resource, err)
			}
		}
		m.putWorkloadIntentLocked(row)
	}
	steps := make([]EnvironmentGitOpsStep, 0, len(createdPlan.Changes))
	for _, change := range createdPlan.Changes {
		field := environmentsync.Field{Resource: change.Resource, Path: change.Path, Value: change.After}
		memory.owners[field.Key()] = environmentsync.Ownership{Field: field, Manager: memory.source.ID}
		steps = append(steps, EnvironmentGitOpsStep{Resource: change.Resource, Path: change.Path, Action: "create", Status: "applied"})
	}
	committed = true
	touchGitOpsMemoryIntent(memory)
	run := memory.runs[lease.RunID]
	run.Status = "applying"
	run.Plan, _ = json.Marshal(plan)
	run.Steps, _ = json.Marshal(steps)
	memory.runs[lease.RunID] = run
	return steps, nil
}

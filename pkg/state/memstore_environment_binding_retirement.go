// adr:1548 — complete graph removal consumes reviewed retirement authority.
package state

import (
	"maps"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type environmentBindingRetirementPolicy struct {
	Revision int64
	Mode     string
}

func retirementBindingPolicy(p api.BindingReleasePolicy) environmentBindingRetirementPolicy {
	return environmentBindingRetirementPolicy{Revision: p.Revision, Mode: p.Mode}
}

// This private grant cannot authorize a recipient, traffic increase or recovery.
// Its policy snapshots were taken when the reviewed retirement was reserved.
type environmentBindingRetirementPublication struct {
	lease         EnvironmentGitOpsLease
	intentVersion int64
	planHash      string
	priorRelease  string
	policies      map[string]environmentBindingRetirementPolicy // retirement ID -> policy
}

func (m *MemStore) environmentBindingRetirementGrantLocked(memory *environmentGitOpsMemory, lease EnvironmentGitOpsLease, planHash string, members []ProjectReleaseMember) (*environmentBindingPublication, error) {
	if m.rejectUncheckedBindingReleaseGraphLocked(memory.source.ProjectID, memory.source.EnvironmentSlug) == nil {
		return nil, nil
	}
	if len(members) != 0 {
		return nil, ErrBindingReleaseRequired
	}
	lease.Source = memory.source
	lease.Revision = cloneEnvironmentRevision(memory.revisions[lease.Revision.ID])
	retirement := environmentBindingRetirementPublication{lease: lease, intentVersion: memory.source.IntentVersion, planHash: planHash,
		priorRelease: m.activeProjectReleaseSets[releaseKey(memory.source.ProjectID, memory.source.EnvironmentSlug)], policies: map[string]environmentBindingRetirementPolicy{}}
	for _, r := range memory.retirements {
		if r.Phase == "planned" && r.Generation == memory.source.Generation && r.RevisionID == lease.Revision.ID {
			policy, ok := memory.retirementPolicies[r.ID]
			if !ok {
				return nil, ErrBindingReleaseRequired
			}
			retirement.policies[r.ID] = policy
		}
	}
	if err := m.checkEnvironmentBindingRetirementLocked(retirement, false); err != nil {
		return nil, err
	}
	return &environmentBindingPublication{members: map[string]string{}, validUntil: memory.lease.LeaseUntil, retirement: &retirement}, nil
}

func (m *MemStore) checkEnvironmentBindingRetirementLocked(grant environmentBindingRetirementPublication, started bool) error {
	memory, err := m.gitOpsLeaseLocked(grant.lease, time.Now())
	if err != nil {
		return err
	}
	source := memory.source
	desired, err := desiredEnvironmentRevision(memory.revisions[grant.lease.Revision.ID])
	if err != nil {
		return err
	}
	if source.Spec.Mode != "enforce" || !source.Spec.Prune || source.Detached || source.IntentVersion != grant.intentVersion || len(grant.planHash) != 64 ||
		desired.Definition.Workloads == nil || len(desired.Definition.Workloads) != 0 || desired.Definition.WorkloadPruningPolicy != "retain" ||
		m.activeProjectReleaseSets[releaseKey(source.ProjectID, source.EnvironmentSlug)] != grant.priorRelease || len(grant.policies) == 0 {
		return ErrBindingReleaseRequired
	}
	phase := "planned"
	if started {
		phase = "retiring"
	}
	removed, policies := map[string]bool{}, map[string]environmentBindingRetirementPolicy{}
	for _, r := range memory.retirements {
		if r.Phase == "retired" {
			removed[r.AppID] = true
			continue
		}
		if r.Phase != phase || r.Generation != source.Generation || r.RevisionID != grant.lease.Revision.ID {
			continue
		}
		if memory.resources[r.Resource] != r.AppID || !workloadRetirementInventoryMatches(r, m.retirementInventoryLocked(memory, r.Resource)) {
			return ErrBindingReleaseRequired
		}
		policy, ok := memory.retirementPolicies[r.ID]
		if !ok || policy != retirementBindingPolicy(m.bindingReleasePolicyLocked(r.AppID, source.EnvironmentSlug)) {
			return ErrBindingReleaseRequired
		}
		removed[r.AppID], policies[r.ID] = true, policy
	}
	if !maps.Equal(grant.policies, policies) {
		return ErrBindingReleaseRequired
	}
	for _, app := range m.apps {
		if app.ProjectID == source.ProjectID && app.AccountID == source.AccountID && app.Status != AppDeleted && app.PreviewOfSlug == "" && !removed[app.ID] {
			return ErrBindingReleaseRequired // a surviving caller/recipient needs its own qualified graph
		}
	}
	return nil
}

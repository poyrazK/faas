package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func quarantineSnapshot(r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision) []api.TaskToolReleaseApp {
	if in.Decision == "recovery_quarantine_qualify" && in.Quarantine != nil && in.Quarantine.Qualification != nil {
		return in.Quarantine.Qualification.NativeApps
	}
	if r.Execution != nil && r.Execution.Recovery != nil && r.Execution.Recovery.Quarantine != nil {
		return r.Execution.Recovery.Quarantine.Qualification.NativeApps
	}
	return nil
}
func quarantineActor(r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision) (string, bool) {
	if strings.HasPrefix(in.Decision, "recovery_quarantine_") {
		return in.AuthorizerKeyID, true
	}
	return r.RequesterKeyID, false
}
func quarantineNeedsSnapshot(in api.TaskToolReleaseIntentDecision) bool {
	return in.Decision == "recovery_quarantine_qualify" || in.Decision == "recovery_quarantine_approve" || in.Quarantine != nil && in.Quarantine.Action == "claim"
}
func (m *MemStore) quarantineOwnerFreshLocked(account, key string) bool {
	k, ok := m.keys[key]
	return ok && m.taskToolReleaseKeyFreshLocked(account, key) && slices.Contains(k.Scopes, api.ScopeAdmin) && !slices.Contains(k.Scopes, api.ScopeAgent) && k.Label != api.MCPLoginKeyLabel
}
func (m *MemStore) changeTaskQuarantineLocked(ctx context.Context, account string, r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision) (api.TaskToolReleaseIntentRecord, error) {
	if !tasktoolrelease.ValidRecoveryResult(r) || in.Quarantine == nil {
		return r, ErrConflict
	}
	actor, owner := quarantineActor(r, in)
	fresh := m.taskToolReleaseKeyFreshLocked(account, actor)
	if owner {
		fresh = fresh && m.quarantineOwnerFreshLocked(account, actor)
	}
	if !fresh {
		return r, ErrConflict
	}
	if quarantineNeedsSnapshot(in) {
		apps := quarantineSnapshot(r, in)
		fresh = len(apps) > 0 && m.taskToolReleaseFreshLocked(account, actor, api.TaskToolReleaseIntent{Apps: apps}, false)
		if !owner && in.Quarantine != nil && in.Quarantine.Action == "claim" {
			q := r.Execution.Recovery.Quarantine
			fresh = fresh && q != nil && m.quarantineOwnerFreshLocked(account, q.AuthorizerKeyID)
			if fresh {
				fresh = m.taskQuarantineRuntimeLocked(ctx, r, in.Quarantine, apps)
			}
		}
	}
	out, err := tasktoolrelease.Quarantine(r, in, fresh, time.Now())
	if err != nil {
		return r, ErrConflict
	}
	return out, nil
}
func (m *MemStore) taskQuarantineRuntimeLocked(ctx context.Context, r api.TaskToolReleaseIntentRecord, in *api.TaskToolQuarantineRequest, apps []api.TaskToolReleaseApp) bool {
	metricApp := ""
	for _, pin := range apps {
		rows := []api.InstanceResponse{}
		for _, row := range m.instances {
			if row.AppID == pin.Review.AppID {
				rows = append(rows, api.InstanceResponse{ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID, State: string(row.State)})
			}
		}
		if !taskRecoveryInstancesOK(r, pin, rows, in.ExpectedInstanceIDs[pin.Review.AppID]) {
			return false
		}
		if pin.App == r.Intent.ObserverMetricApp {
			metricApp = pin.Review.AppID
		}
		if pin.Role == "web" {
			if in.ExpectedMCPAccessPolicy == nil {
				return false
			}
			policy := WithMCPAccessPolicyExpectation(ctx, r.AccountID, pin.Review.AppID, *in.ExpectedMCPAccessPolicy)
			if m.checkMCPAccessPolicyLocked(policy, pin.Review.AppID) != nil {
				return false
			}
		}
	}
	metric, ok := m.customMetrics[metricApp]["mcp_tasks_observer_heartbeat"]
	return ok && taskRecoveryHeartbeatOK(metric.Value, metric.ObservedAt, time.Now())
}

package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

var ErrTaskToolRecoveryChanged = errors.New("task recovery approval or fresh evidence changed")

type taskRecoveryTrafficKey struct{}
type taskRecoveryTrafficGuard struct {
	account, key, intent string
	request              api.TaskToolRecoveryRestoreRequest
}

// WithTaskToolRecoveryTraffic binds authenticated caller identities to the
// ordinary native traffic transaction. It does not bypass any existing gate.
func WithTaskToolRecoveryTraffic(ctx context.Context, account, key, intent string, in api.TaskToolRecoveryRestoreRequest) context.Context {
	return context.WithValue(ctx, taskRecoveryTrafficKey{}, taskRecoveryTrafficGuard{account, key, intent, in})
}
func taskRecoveryTrafficFrom(ctx context.Context) *taskRecoveryTrafficGuard {
	g, ok := ctx.Value(taskRecoveryTrafficKey{}).(taskRecoveryTrafficGuard)
	if !ok {
		return nil
	}
	return &g
}
func taskRecoveryTarget(r api.TaskToolReleaseIntentRecord, g *taskRecoveryTrafficGuard, id string, percent int, expected []string) bool {
	return r.AccountID == g.account && r.RequesterKeyID == g.key && tasktoolrelease.RestoreIdentity(r, g.request) && percent == 100 && len(expected) == 1 &&
		sameDeploymentID(r.Execution.Recovery.Qualification.TargetDeploymentID, id) && sameDeploymentID(r.Execution.Recovery.Qualification.ExpectedServingDeploymentID, expected[0])
}
func taskRecoveryInstancesOK(r api.TaskToolReleaseIntentRecord, pin api.TaskToolReleaseApp, rows []api.InstanceResponse, wanted []string) bool {
	if len(rows) > api.TaskToolReleaseReceiptWorkerIDsMax {
		return false
	}
	expected := pin.Review.LatestDeploymentID
	if pin.Role == "web" {
		target, current := r.Execution.Receipt.ServingDeploymentID, r.Execution.Receipt.WebDeploymentID
		ids := []string{}
		for _, row := range rows {
			if row.AppID != pin.Review.AppID {
				return false
			}
			status := strings.ToLower(row.State)
			if status == "parked" || status == "stopped" || status == "failed" || status == "exited" {
				continue
			}
			if row.DeploymentID == current {
				continue
			}
			if row.DeploymentID != target || status != "running" {
				return false
			}
			ids = append(ids, row.ID)
		}
		want := slices.Clone(wanted)
		slices.Sort(want)
		slices.Sort(ids)
		return len(ids) > 0 && slices.Equal(want, ids)
	}
	if expected == "" {
		return false
	}
	ids := []string{}
	for _, row := range rows {
		if row.AppID != pin.Review.AppID {
			return false
		}
		switch strings.ToLower(row.State) {
		case "parked", "stopped", "failed", "exited":
			continue
		case "running":
			if row.DeploymentID != expected {
				return false
			}
			ids = append(ids, row.ID)
		default:
			return false
		}
	}
	if len(ids) == 0 {
		return false
	}
	want := slices.Clone(wanted)
	slices.Sort(want)
	slices.Sort(ids)
	return slices.Equal(want, ids)
}
func taskRecoveryHeartbeatOK(value float64, observed, now time.Time) bool {
	return value == 1 && !observed.IsZero() && !observed.After(now) && now.Sub(observed) <= time.Duration(api.CustomMetricFreshnessSeconds)*time.Second
}

func (m *MemStore) prepareTaskRecoveryTrafficLocked(ctx context.Context, id string, percent int, expected []string) (*api.TaskToolReleaseIntentRecord, error) {
	g := taskRecoveryTrafficFrom(ctx)
	if g == nil {
		return nil, nil
	}
	r, ok := m.taskToolReleaseIntents[g.account+":"+g.intent]
	if !ok || !taskRecoveryTarget(r, g, id, percent, expected) {
		return nil, fmt.Errorf("%w: operation target changed", ErrTaskToolRecoveryChanged)
	}
	out, err := tasktoolrelease.Restore(r, g.request, time.Now())
	if err != nil {
		return nil, fmt.Errorf("%w: approval or fresh local evidence changed", ErrTaskToolRecoveryChanged)
	}
	h := r.Execution.Recovery
	owner, ok := m.keys[h.AuthorizerKeyID]
	if !ok || !m.taskToolReleaseKeyFreshLocked(g.account, h.AuthorizerKeyID) || !slices.Contains(owner.Scopes, api.ScopeAdmin) || slices.Contains(owner.Scopes, api.ScopeAgent) || owner.Label == api.MCPLoginKeyLabel ||
		!m.taskToolReleaseFreshLocked(g.account, g.key, api.TaskToolReleaseIntent{Apps: h.Qualification.NativeApps}, false) {
		return nil, fmt.Errorf("%w: owner or app snapshot changed", ErrTaskToolRecoveryChanged)
	}
	for _, pin := range h.Qualification.NativeApps {
		rows := []api.InstanceResponse{}
		for _, row := range m.instances {
			if row.AppID == pin.Review.AppID {
				rows = append(rows, api.InstanceResponse{ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID, State: string(row.State)})
			}
		}
		if !taskRecoveryInstancesOK(r, pin, rows, g.request.ExpectedInstanceIDs[pin.Review.AppID]) {
			return nil, fmt.Errorf("%w: %s runtime changed", ErrTaskToolRecoveryChanged, pin.Role)
		}
	}
	destination := ""
	for _, pin := range h.Qualification.NativeApps {
		if pin.App == r.Intent.ObserverMetricApp {
			destination = pin.Review.AppID
		}
	}
	metric, ok := m.customMetrics[destination]["mcp_tasks_observer_heartbeat"]
	if !ok || !taskRecoveryHeartbeatOK(metric.Value, metric.ObservedAt, time.Now()) {
		return nil, fmt.Errorf("%w: observer heartbeat changed", ErrTaskToolRecoveryChanged)
	}
	return &out, nil
}

// TaskToolRecoveryTrafficStore advertises atomic recovery support to the native
// handler; ordinary Store wrappers cannot silently fall back to an unfenced write.
type TaskToolRecoveryTrafficStore interface{ TaskToolRecoveryTrafficSupported() bool }

func (*MemStore) TaskToolRecoveryTrafficSupported() bool { return true }
func (*PgStore) TaskToolRecoveryTrafficSupported() bool  { return true }

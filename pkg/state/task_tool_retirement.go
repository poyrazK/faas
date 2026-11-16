package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

type taskRetirementKey struct{}
type taskRetirementGuard struct {
	account, key, intent string
	request              api.TaskToolRetirementRequest
}

func WithTaskToolRetirementPark(ctx context.Context, account, key, intent string, in api.TaskToolRetirementRequest) context.Context {
	return context.WithValue(ctx, taskRetirementKey{}, taskRetirementGuard{account, key, intent, in})
}
func retirementGuardFrom(ctx context.Context) *taskRetirementGuard {
	g, ok := ctx.Value(taskRetirementKey{}).(taskRetirementGuard)
	if !ok {
		return nil
	}
	return &g
}

type TaskToolRetirementParkStore interface{ TaskToolRetirementParkSupported() bool }

func (*MemStore) TaskToolRetirementParkSupported() bool { return true }
func (*PgStore) TaskToolRetirementParkSupported() bool  { return true }

func retirementApps(r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision) []api.TaskToolReleaseApp {
	if in.Decision == "recovery_retire_qualify" && in.Retirement != nil && in.Retirement.Qualification != nil {
		return in.Retirement.Qualification.NativeApps
	}
	if r.Execution != nil && r.Execution.Recovery != nil && r.Execution.Recovery.Retirement != nil {
		return r.Execution.Recovery.Retirement.Qualification.NativeApps
	}
	return nil
}
func retirementWorker(r api.TaskToolReleaseIntentRecord) string {
	for _, pin := range r.Intent.Apps {
		if pin.Role == "worker" {
			return pin.Review.AppID
		}
	}
	return ""
}
func retirementOwner(in api.TaskToolReleaseIntentDecision) bool {
	return in.Decision == "recovery_retire_qualify" || in.Decision == "recovery_retire_approve" || in.Decision == "recovery_retire_revoke"
}
func retirementEvidence(in *api.TaskToolRetirementRequest) *api.TaskToolQuarantineRequest {
	return &api.TaskToolQuarantineRequest{ExpectedInstanceIDs: in.ExpectedInstanceIDs, ExpectedMCPAccessPolicy: in.ExpectedMCPAccessPolicy}
}
func retirementTarget(r api.TaskToolReleaseIntentRecord, g *taskRetirementGuard, app, dep string) bool {
	return g != nil && r.AccountID == g.account && r.RequesterKeyID == g.key && tasktoolrelease.RetirementIdentity(r, g.request.QualificationSHA256) && retirementWorker(r) == app && sameDeploymentID(r.Execution.Receipt.WorkerDeploymentID, dep)
}
func retirementWorkerShape(manifest AppManifest, class string, shutdown int64) bool {
	return (manifest.ExecutionMode == api.ExecutionModeWorker || class == "worker") && shutdown > 0 && int64(manifest.StopGracePeriodS)*1000 > shutdown
}

func (m *MemStore) changeTaskRetirementLocked(account string, r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision) (api.TaskToolReleaseIntentRecord, error) {
	if in.Retirement == nil || !tasktoolrelease.ValidRetirement(r) {
		return r, ErrConflict
	}
	actor := r.RequesterKeyID
	owner := retirementOwner(in)
	if owner {
		actor = in.AuthorizerKeyID
	}
	fresh := m.taskToolReleaseKeyFreshLocked(account, actor)
	if owner {
		fresh = fresh && m.quarantineOwnerFreshLocked(account, actor)
	}
	if !fresh {
		return r, ErrConflict
	}
	if in.Decision == "recovery_retire_qualify" || in.Decision == "recovery_retire_approve" {
		apps := retirementApps(r, in)
		fresh = len(apps) > 0 && m.taskToolReleaseFreshLocked(account, actor, api.TaskToolReleaseIntent{Apps: apps}, false)
	}
	if !owner {
		if in.Retirement.Action != "observe" {
			return r, ErrConflict
		}
		fresh = m.taskRetirementDrainedLocked(r)
	}
	out, err := tasktoolrelease.Retirement(r, in, fresh, time.Now(), "")
	if err != nil {
		return r, ErrConflict
	}
	return out, nil
}
func (m *MemStore) taskRetirementDrainedLocked(r api.TaskToolReleaseIntentRecord) bool {
	if r.Execution == nil || r.Execution.Recovery == nil || r.Execution.Recovery.Retirement == nil {
		return false
	}
	q := r.Execution.Recovery.Retirement
	id := retirementWorker(r)
	app, ok := m.apps[id]
	if !ok || app.Status != AppEvictedCold || m.appParkTransitionByApp[id] != q.ParkTransitionID {
		return false
	}
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == id && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found || !sameDeploymentID(latest.ID, r.Execution.Receipt.WorkerDeploymentID) {
		return false
	}
	for _, row := range m.instances {
		if row.AppID == id && IsLive(row.State) {
			return false
		}
	}
	return true
}
func (m *MemStore) prepareTaskRetirementParkLocked(ctx context.Context, app, dep string) (*api.TaskToolReleaseIntentRecord, error) {
	g := retirementGuardFrom(ctx)
	if g == nil {
		return nil, nil
	}
	r, ok := m.taskToolReleaseIntents[g.account+":"+g.intent]
	if !ok || !retirementTarget(r, g, app, dep) || r.Execution.Recovery.Retirement == nil {
		return nil, ErrConflict
	}
	q := r.Execution.Recovery.Retirement
	apps := q.Qualification.NativeApps
	actual := m.apps[app]
	if !retirementWorkerShape(actual.Manifest, string(actual.WorkloadClass), g.request.ShutdownTimeoutMS) || !m.quarantineOwnerFreshLocked(g.account, q.AuthorizerKeyID) || !m.taskToolReleaseFreshLocked(g.account, g.key, api.TaskToolReleaseIntent{Apps: apps}, false) || !m.taskQuarantineRuntimeLocked(ctx, r, retirementEvidence(&g.request), apps) {
		return nil, ErrConflict
	}
	out, err := tasktoolrelease.Retirement(r, api.TaskToolReleaseIntentDecision{ReleaseFingerprint: r.Intent.ReleaseFingerprint, Retirement: &g.request}, true, time.Now(), uuid.NewString())
	if err != nil {
		return nil, ErrConflict
	}
	return &out, nil
}

// adr: 601 — exact historical rollback separates readiness from checked routing.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrCheckedRollbackChanged = errors.New("state: checked rollback deployment pair changed")
var ErrCheckedRollbackRequired = errors.New("state: exact checked rollback operation required")

type CheckedRollbackStore interface {
	CreateCheckedRollback(context.Context, string, string, string, string, string) (api.RollbackOperation, error)
	GetCheckedRollback(context.Context, string, string, string) (api.RollbackOperation, error)
	CheckedRollbackForTarget(context.Context, string) (api.RollbackOperation, error)
	ListPendingCheckedRollbacks(context.Context) ([]api.RollbackOperation, error)
	CommitCheckedRollback(context.Context, api.RollbackOperation) (api.RollbackOperation, error)
	UpdateCheckedRollback(context.Context, api.RollbackOperation, string, string, []api.BindingCheckFinding) error
}
type checkedRollbackKey struct{}

func withCheckedRollback(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, checkedRollbackKey{}, id)
}
func rollbackRequestID(ctx context.Context) string {
	id, _ := ctx.Value(checkedRollbackKey{}).(string)
	return id
}
func rollbackTerminal(r api.RollbackOperation) bool {
	return r.Status == "complete" || r.Status == "failed"
}
func newCheckedRollback(appID, scope, target, current, reason string, service bool) api.RollbackOperation {
	now := time.Now().UTC()
	return api.RollbackOperation{ID: uuid.NewString(), AppID: appID, Scope: normalizedDeploymentScope(scope), TargetDeploymentID: target, CurrentDeploymentID: current, Status: "preparing", Reason: reason, Service: service, CreatedAt: now, UpdatedAt: now}
}
func rollbackHandoff(r api.RollbackOperation) ServiceRolloutHandoff {
	if !r.Service {
		return ServiceRolloutHandoff{}
	}
	return ServiceRolloutHandoff{Action: ServiceRolloutActionPromote, Phase: ServiceRolloutPhasePending, PredecessorDeploymentID: r.CurrentDeploymentID, Reason: r.Reason,
		BindingsCheck: &api.ServiceRolloutBindingGate{RequestID: r.ID, Action: ServiceRolloutActionPromote, DeploymentID: r.TargetDeploymentID, Status: "pending"}}
}
func blockedRollback(r api.RollbackOperation, status, code string, blockers []api.BindingCheckFinding) api.RollbackOperation {
	gate := blockedServiceBindingGate(&api.ServiceRolloutBindingGate{}, code, blockers)
	r.Status, r.Code, r.Blockers, r.UpdatedAt = status, gate.Code, gate.Blockers, time.Now().UTC()
	if status == "complete" {
		r.CompletedAt = &r.UpdatedAt
		r.Code = ""
		r.Blockers = nil
	}
	return r
}
func decodeCheckedRollback(raw []byte, err error) (api.RollbackOperation, error) {
	var r api.RollbackOperation
	if err != nil {
		return r, mapErr(err)
	}
	err = json.Unmarshal(raw, &r)
	return r, err
}
func rollbackPairValid(r api.RollbackOperation, target, current Deployment, rows []Deployment) bool {
	if target.ID != r.TargetDeploymentID || target.AppID != r.AppID || normalizedDeploymentScope(target.Scope) != r.Scope || current.ID != r.CurrentDeploymentID || current.AppID != r.AppID || normalizedDeploymentScope(current.Scope) != r.Scope || current.Status != DeployLive || current.TrafficPercent != 100 || current.CanaryTotalSteps > 0 && current.CanaryStep < current.CanaryTotalSteps || IsServiceRollout(current) {
		return false
	}
	for _, d := range rows {
		if d.ID != target.ID && d.ID != current.ID && d.Status == DeployLive && (d.TrafficPercent > 0 || IsServiceRollout(d) || d.CanaryTotalSteps > 0 && (d.RolloutState == "pending" || d.RolloutState == "rolling_out")) {
			return false
		}
	}
	return true
}

func validateCheckedRollbackArguments(acct, app, target, current, reason string) error {
	for _, id := range []string{acct, app, target, current} {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalidArgument
		}
	}
	if sameDeploymentID(target, current) || len(reason) > api.BindingReleasePolicyReasonMaxBytes || !utf8.ValidString(reason) || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validRollbackStatusUpdate(r api.RollbackOperation, status string) bool {
	return status == r.Status || status == "failed" || status == "blocked" && (r.Status == "ready" || r.Status == "blocked") || status == "complete" && r.Status == "routing"
}

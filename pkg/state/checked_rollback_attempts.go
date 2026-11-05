package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRollbackAttemptAccepted = errors.New("rollback attempt already accepted; inspect its retained operation")
var ErrRollbackAttemptConflict = errors.New("rollback attempt belongs to a different request")

type checkedRollbackAttemptKey struct{}

// ContextWithCheckedRollbackAttempt attaches APID's validated attempt identity.
// CreateCheckedRollback commits the identity as its original operation UUID.
func ContextWithCheckedRollbackAttempt(ctx context.Context, attempt string) context.Context {
	return context.WithValue(ctx, checkedRollbackAttemptKey{}, attempt)
}
func checkedRollbackAttemptID(ctx context.Context) (string, error) {
	id, _ := ctx.Value(checkedRollbackAttemptKey{}).(string)
	if id == "" {
		return "", nil
	}
	if !api.ValidDeploymentAttemptID(id) {
		return "", ErrInvalidArgument
	}
	return uuid.MustParse(id).String(), nil
}
func checkedRollbackAttemptError(r api.RollbackOperation, app, target, current, reason string) error {
	original := api.RollbackRequest{TargetDeploymentID: &r.TargetDeploymentID, ExpectedCurrentDeploymentID: &r.CurrentDeploymentID, Reason: r.Reason}
	proposed := api.RollbackRequest{TargetDeploymentID: &target, ExpectedCurrentDeploymentID: &current, Reason: reason}
	if api.RollbackRequestHash(r.AppID, original) != api.RollbackRequestHash(app, proposed) || r.Scope != api.DefaultEnvScope || r.Service {
		return ErrRollbackAttemptConflict
	}
	return ErrRollbackAttemptAccepted
}

package state

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
)

// RouteRemovalFence pins the server evidence used for a contract exception.
// Only an internal preflight may attach it; HTTP inputs never become fences.
type RouteRemovalFence struct {
	BaselineSnapshotSHA256  string `json:"baseline_snapshot_sha256,omitempty"`
	CandidateSnapshotSHA256 string `json:"candidate_snapshot_sha256,omitempty"`
	DeploymentID            string `json:"deployment_id"`
	BaselineDeploymentID    string `json:"baseline_deployment_id"`
	ApprovalID              string `json:"approval_id"`
	PolicyRevision          int64  `json:"policy_revision"`
	BaselineSHA256          string `json:"baseline_sha256"`
	CandidateSHA256         string `json:"candidate_sha256"`
}
type routeRemovalFencesKey struct{}

func WithRouteRemovalFence(ctx context.Context, fence RouteRemovalFence) context.Context {
	fences, _ := ctx.Value(routeRemovalFencesKey{}).([]RouteRemovalFence)
	copyFences := append([]RouteRemovalFence(nil), fences...)
	return context.WithValue(ctx, routeRemovalFencesKey{}, append(copyFences, fence))
}
func pgAuthorizeRouteRemoval(ctx context.Context, tx pgx.Tx) error {
	fences, _ := ctx.Value(routeRemovalFencesKey{}).([]RouteRemovalFence)
	if len(fences) == 0 {
		return nil
	}
	body, err := json.Marshal(fences)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT set_config('faas.route_removal_fences',$1,true)`, string(body))
	return err
}

func validateRouteRemovalSnapshot(ctx context.Context, snapshot OpenAPISnapshot) error {
	fences, _ := ctx.Value(routeRemovalFencesKey{}).([]RouteRemovalFence)
	for _, f := range fences {
		if f.DeploymentID == snapshot.DeploymentID && f.CandidateSnapshotSHA256 != "" && f.CandidateSnapshotSHA256 != snapshot.SHA256 {
			return &RouteRemovalBlockedError{Reason: "candidate_contract_changed_after_preflight"}
		}
	}
	return nil
}

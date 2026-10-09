package openapidiff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// CheckLiveContract compares against serving traffic, rather than the newest
// snapshot (which may belong to a dark candidate). A configured removal policy
// retains its baseline through partial canaries.
func CheckLiveContract(ctx context.Context, store state.Store, appID, id, scope string, captured bool) (PromotionCheck, error) {
	var check PromotionCheck
	var err error
	if captured {
		check, err = CheckDeploymentPromotion(ctx, store, appID, id, scope)
	} else {
		check, err = CheckPromotion(ctx, store, appID, id, scope)
	}
	if err != nil && !errors.Is(err, ErrSnapshotBaselineMissing) {
		return check, err
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil {
		return check, err
	}
	baselineID := ""
	if policies, ok := store.(state.RouteRemovalStore); ok {
		policy, err := policies.GetRouteRemovalPolicy(ctx, app.AccountID, appID)
		if err != nil {
			return check, err
		}
		baselineID = policy.BaselineDeploymentID
	}
	if baselineID == "" {
		rows, err := store.LiveDeployments(ctx, appID)
		if err != nil {
			return check, err
		}
		weight := 0
		for _, d := range rows {
			if (d.Scope == scope || productionContractScope(d.Scope) && productionContractScope(scope)) && d.TrafficPercent > weight {
				baselineID = d.ID
				weight = d.TrafficPercent
			}
		}
	}
	if baselineID == "" {
		return PromotionCheck{Proposed: check.Proposed, ProposedSource: check.ProposedSource, Diff: SnapshotDiff{ProposedSHA256: check.Proposed.SHA256}}, ErrSnapshotBaselineMissing
	}
	snapshots, ok := store.(state.OpenAPISnapshotStore)
	if !ok {
		return check, ErrSnapshotStoreUnavailable
	}
	baseline, err := snapshots.OpenAPISnapshotByDeployment(ctx, baselineID)
	if err != nil {
		return check, fmt.Errorf("read serving contract snapshot: %w", err)
	}
	if baseline.AppID != appID || check.Proposed.AppID != appID || !productionContractScope(baseline.Scope) || !productionContractScope(check.Proposed.Scope) {
		return check, fmt.Errorf("contract snapshot identity mismatch")
	}
	check.Baseline = baseline
	check.HasBaseline = true
	check.Diff, err = CompareSnapshots(baseline.Snapshot, check.Proposed.Snapshot)
	return check, err
}

// ApplyRemovalException removes only whole-operation deletion findings. Every
// schema break and unknown remains intact, even for an approved operation.
// Dark candidates need no approval, since they receive no weighted traffic.
func ApplyRemovalException(ctx context.Context, store state.Store, check PromotionCheck, dark bool) (PromotionCheck, *state.RouteRemovalFence, error) {
	before, err := UnmarshalSnapshot(check.Baseline.Snapshot)
	if err != nil {
		return check, nil, err
	}
	after, err := UnmarshalSnapshot(check.Proposed.Snapshot)
	if err != nil {
		return check, nil, err
	}
	removed := map[string]bool{}
	for path, item := range before.Paths {
		for method := range item.Methods {
			if after.Paths[path] == nil || after.Paths[path].Methods[method] == nil {
				removed[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	if len(removed) == 0 {
		return check, nil, nil
	}
	var fence *state.RouteRemovalFence
	if !dark {
		policies, ok := store.(state.RouteRemovalStore)
		if !ok {
			return check, nil, nil
		}
		app, err := store.AppByID(ctx, check.Proposed.AppID)
		if err != nil {
			return check, nil, err
		}
		approval, err := policies.CheckRouteRemoval(ctx, app.AccountID, app.ID, check.Proposed.DeploymentID)
		if err != nil {
			return check, nil, err
		}
		if approval.Status != "passed" || approval.ApprovalID == "" || approval.Policy.Mode != "enforce" || approval.Policy.BaselineDeploymentID != check.Baseline.DeploymentID {
			return check, nil, nil
		}
		for _, endpoint := range []struct{ id, hash, want string }{{check.Baseline.DeploymentID, approval.BaselineContractSHA256, check.Diff.BaselineSHA256}, {check.Proposed.DeploymentID, approval.CandidateContractSHA256, check.Diff.ProposedSHA256}} {
			doc, meta, err := store.GetDeploymentOpenAPIDoc(ctx, endpoint.id, app.AccountID)
			if err != nil {
				return check, nil, err
			}
			if meta.Truncated || fmt.Sprintf("%x", meta.DocSHA256) != endpoint.hash {
				return check, nil, nil
			}
			snapshot, _, err := SnapshotFromDocument(endpoint.id, app.ID, check.Proposed.Scope, doc, nil)
			if err != nil {
				return check, nil, err
			}
			if snapshot.SHA256 != endpoint.want {
				return check, nil, nil
			}
		}
		covered := map[string]bool{}
		for _, m := range approval.Removed {
			covered[strings.ToUpper(m.Method)+" "+m.Path] = true
		}
		for operation := range removed {
			if !covered[operation] {
				return check, nil, nil
			}
		}
		fence = &state.RouteRemovalFence{BaselineSnapshotSHA256: check.Diff.BaselineSHA256, CandidateSnapshotSHA256: check.Diff.ProposedSHA256, DeploymentID: check.Proposed.DeploymentID, BaselineDeploymentID: check.Baseline.DeploymentID, ApprovalID: approval.ApprovalID, PolicyRevision: approval.Policy.Revision, BaselineSHA256: approval.BaselineContractSHA256, CandidateSHA256: approval.CandidateContractSHA256}
	}
	breaks := make([]SchemaBreak, 0, len(check.Diff.Breaks))
	for _, b := range check.Diff.Breaks {
		if b.Kind == SchemaKindFieldRemoved && b.Status == "" && b.PathInSchema == "" && b.After == nil && removed[strings.ToUpper(b.Method)+" "+b.Path] && (b.Before == b.Path || b.Before == strings.ToLower(b.Method)) {
			continue
		}
		breaks = append(breaks, b)
	}
	check.Diff.Breaks = breaks
	return check, fence, nil
}

func productionContractScope(scope string) bool {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "default", "prod", "production":
		return true
	}
	return false
}

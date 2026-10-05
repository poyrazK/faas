package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func captureCloneScopedPoliciesDB(ctx context.Context, db sqlc.DBTX, op ProjectEnvironmentCloneOperation, appID string, settings ProjectEnvironmentWorkloadSettings) (projectCloneScopedPolicies, error) {
	raw, err := new(sqlc.Queries).ReadProjectEnvironmentCloneScopedEdgePolicy(ctx, db, sqlc.ReadProjectEnvironmentCloneScopedEdgePolicyParams{
		AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), AppID: mustPgUUID(appID), Environment: op.SourceEnvironment,
	})
	policies := projectCloneScopedPolicies{}
	if err != nil {
		return policies, mapErr(err)
	}
	if json.Unmarshal(raw, &policies) != nil {
		return policies, ErrConflict
	}
	policies.OnlyAllowDeclaredRoutes, policies.DeclaredRoutes = settings.OnlyAllowDeclaredRoutes, settings.DeclaredRoutes
	return normalizeCloneScopedPolicies(policies)
}

func copyCapturedProjectEnvironmentScopedPolicies(ctx context.Context, db sqlc.DBTX, clone ProjectEnvironmentClone, result *ProjectEnvironmentCloneResult) error {
	q := new(sqlc.Queries)
	for appID, policies := range clone.capturedPolicies {
		if !policies.OnlyAllowDeclaredRoutes || len(policies.DeclaredRoutes) > 0 {
			routes, err := json.Marshal(policies.DeclaredRoutes)
			if err != nil {
				return err
			}
			if err := q.InsertProjectEnvironmentCloneCapturedRoutePolicy(ctx, db, sqlc.InsertProjectEnvironmentCloneCapturedRoutePolicyParams{
				AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), AppID: mustPgUUID(appID), Environment: clone.TargetSlug,
				OnlyAllowDeclaredRoutes: policies.OnlyAllowDeclaredRoutes, DeclaredRoutes: routes,
			}); err != nil {
				return mapErr(err)
			}
			result.RoutesCopied++
		}
		if policies.EdgePresent {
			rules, err := json.Marshal(policies.EdgeRules)
			if err != nil {
				return err
			}
			if err := q.InsertProjectEnvironmentCloneCapturedEdgePolicy(ctx, db, sqlc.InsertProjectEnvironmentCloneCapturedEdgePolicyParams{
				AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), AppID: mustPgUUID(appID), Environment: clone.TargetSlug, Rules: rules,
			}); err != nil {
				return mapErr(err)
			}
			result.PoliciesCopied++
		}
	}
	return nil
}

func verifyCloneScopedPolicyPublicationDB(ctx context.Context, db sqlc.DBTX, op ProjectEnvironmentCloneOperation) error {
	records, err := cloneWorkloadRecordsDB(ctx, db, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	for _, record := range records {
		raw, err := new(sqlc.Queries).ReadProjectEnvironmentCloneScopedPolicyProof(ctx, db, sqlc.ReadProjectEnvironmentCloneScopedPolicyProofParams{
			AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), AppID: mustPgUUID(record.AppID), Environment: op.TargetEnvironment,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("clone workload %q scoped policy proof is unavailable: %w", record.WorkloadSlug, ErrConflict)
		}
		if err != nil {
			return mapErr(err)
		}
		var actual projectCloneScopedPolicies
		if json.Unmarshal(raw, &actual) != nil {
			return ErrConflict
		}
		if err := validateCloneScopedPolicyPublication(record, actual, true); err != nil {
			return err
		}
	}
	return nil
}

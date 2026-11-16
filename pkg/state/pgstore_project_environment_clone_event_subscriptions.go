package state

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func captureCloneEventSubscriptionsDB(ctx context.Context, db sqlc.DBTX, appID, scope string) (projectCloneEventSubscriptions, error) {
	subscriptionScope := NormalizeCronDeploymentScope(scope)
	subscriptions, _, err := readCloneEventSubscriptionsDB(ctx, db, appID, subscriptionScope)
	if err != nil {
		return projectCloneEventSubscriptions{}, err
	}
	return normalizeCloneEventSubscriptions(projectCloneEventSubscriptions{AppID: appID, SourceScope: subscriptionScope, Subscriptions: subscriptions})
}

// readCloneEventSubscriptionsDB reads one app's subscriptions in a scope
// with each one's stored enabled flag.
func readCloneEventSubscriptionsDB(ctx context.Context, db sqlc.DBTX, appID, scope string) ([]projectCloneEventSubscription, []bool, error) {
	rows, err := sqlc.New().ReadProjectEnvironmentCloneEventSubscriptions(ctx, db, sqlc.ReadProjectEnvironmentCloneEventSubscriptionsParams{
		AppID: mustPgUUID(appID), Scope: scope})
	if err != nil {
		return nil, nil, mapErr(err)
	}
	out := make([]projectCloneEventSubscription, 0, len(rows))
	stored := make([]bool, 0, len(rows))
	for _, row := range rows {
		out = append(out, projectCloneEventSubscription{Source: row.Source, Type: row.Type,
			Filter: append(json.RawMessage(nil), row.Filter...), Enabled: row.Enabled,
			RoutingRetryPolicy: decodeEventRoutingRetryPolicy(row.RoutingRetryPolicy),
			SchemaVersions:     slices.Clone(row.SchemaVersions), AcknowledgmentLeaseSeconds: int(row.AcknowledgmentLeaseSeconds)})
		stored = append(stored, row.StoredEnabled)
	}
	return out, stored, nil
}

// requireCloneEventSubscriptionsCapturedDB refuses a source whose environment
// holds subscriptions on project apps outside the captured workloads: the
// clone would drop them.
func requireCloneEventSubscriptionsCapturedDB(ctx context.Context, db sqlc.DBTX, projectID, source string, appIDs []string) error {
	ids := make([]pgtype.UUID, 0, len(appIDs))
	for _, id := range appIDs {
		ids = append(ids, mustPgUUID(id))
	}
	n, err := sqlc.New().CountProjectEnvironmentCloneUncapturedEventSubscriptions(ctx, db, sqlc.CountProjectEnvironmentCloneUncapturedEventSubscriptionsParams{
		ProjectID: mustPgUUID(projectID), Scope: NormalizeCronDeploymentScope(source), AppIds: ids})
	if err != nil {
		return mapErr(err)
	}
	if n > 0 {
		return ErrProjectEnvironmentCloneEventSubscriptionCaptureUnavailable
	}
	return nil
}

func copyCapturedEventSubscriptionsTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, result *ProjectEnvironmentCloneResult) error {
	q := sqlc.New()
	for _, appID := range slices.Sorted(maps.Keys(clone.capturedEventSubscriptions)) {
		for _, s := range clone.capturedEventSubscriptions[appID].Subscriptions {
			var policy []byte
			if s.RoutingRetryPolicy != nil {
				policy = retryPolicyCloneJSON(s.RoutingRetryPolicy)
			}
			versions := s.SchemaVersions
			if versions == nil {
				versions = []string{}
			}
			if err := q.InsertProjectEnvironmentCloneEventSubscription(ctx, tx, sqlc.InsertProjectEnvironmentCloneEventSubscriptionParams{
				AppID: mustPgUUID(appID), Source: s.Source, Type: s.Type, Filter: s.Filter, ClonePaused: s.Enabled,
				RoutingRetryPolicy: policy, SchemaVersions: versions,
				AcknowledgmentLeaseSeconds: int32(s.AcknowledgmentLeaseSeconds), //nolint:gosec // bounded by the subscription CHECKs
				Scope:                      clone.TargetSlug,
			}); err != nil {
				return mapErr(err)
			}
			result.EventSubscriptionsCopied++
		}
	}
	return nil
}

func verifyCloneEventSubscriptionPublicationDB(ctx context.Context, db sqlc.DBTX, op ProjectEnvironmentCloneOperation) error {
	records, err := cloneWorkloadRecordsDB(ctx, db, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	for _, record := range records {
		actual, stored, err := readCloneEventSubscriptionsDB(ctx, db, record.AppID, NormalizeCronDeploymentScope(op.TargetEnvironment))
		if err != nil {
			return err
		}
		if err := validateCloneEventSubscriptionPublication(record, actual, stored); err != nil {
			return err
		}
	}
	return nil
}

func (s *PgStore) ResumeEnvironmentClonePausedEventSubscriptions(ctx context.Context, accountID, projectID, environment string, limits api.Limits) ([]EventSubscription, error) {
	if IsProductionScope(environment) {
		return nil, ErrInvalidArgument
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, err
	}
	project, err := parsePgUUID(projectID)
	if err != nil {
		return nil, err
	}
	id, err := sqlc.New().ReadProjectEnvironmentCloneFlagScope(ctx, s.pool, sqlc.ReadProjectEnvironmentCloneFlagScopeParams{
		AccountID: account, ProjectID: project, Environment: environment})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, mapErr(err)
	}
	// Counting after the writes, inside the transaction, makes concurrent
	// enables of one billing subject safe as the manifest path's count is
	// (ADR-20261031130000): the later of two sees both.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	rows, err := q.ResumeEnvironmentClonePausedEventSubscriptions(ctx, tx, id)
	if err != nil {
		return nil, mapErr(err)
	}
	counted := map[string]bool{}
	for _, row := range rows {
		appID := uuidString(row.AppID)
		if counted[appID] {
			continue
		}
		counted[appID] = true
		counts, err := q.CountEnabledEventSubscriptionsForQuota(ctx, tx, row.AppID)
		if err != nil {
			return nil, mapErr(err)
		}
		if err := checkEventSubscriptionQuota(int(counts.AppCount), int(counts.AccountCount), limits); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	resumed := make([]EventSubscription, 0, len(rows))
	for _, row := range rows {
		resumed = append(resumed, EventSubscription{ID: uuidString(row.ID), AppID: uuidString(row.AppID), AccountID: accountID,
			DeploymentScope: environment, Enabled: true})
	}
	slices.SortFunc(resumed, func(a, b EventSubscription) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	return resumed, nil
}

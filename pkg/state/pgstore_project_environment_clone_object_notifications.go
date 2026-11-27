package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// copyCloneBucketNotificationsTx writes a captured configuration to a newly
// reserved target bucket, paused. A target that already holds one keeps it:
// publication proves it equals the capture.
func copyCloneBucketNotificationsTx(ctx context.Context, tx pgx.Tx, source ProjectEnvironmentCloneObjectBucket, targetID string) error {
	if source.Notifications == nil {
		return nil
	}
	p := clonedObjectNotifications(targetID, source.Notifications)
	rules, err := json.Marshal(p.Rules)
	if err != nil {
		return err
	}
	managed, err := json.Marshal(managedObjectNotificationRules(p))
	if err != nil {
		return err
	}
	return mapErr(sqlc.New().InsertProjectEnvironmentCloneBucketNotifications(ctx, tx, sqlc.InsertProjectEnvironmentCloneBucketNotificationsParams{
		BucketID: mustPgUUID(targetID), Revision: p.Revision, Rules: rules, ManagedRules: managed}))
}

func verifyCloneBucketNotificationPublicationDB(ctx context.Context, db sqlc.DBTX, op ProjectEnvironmentCloneOperation) error {
	records, err := cloneWorkloadRecordsDB(ctx, db, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	q := sqlc.New()
	for _, record := range records {
		if record.snapshot.Bindings == nil {
			continue
		}
		for _, source := range record.snapshot.Bindings.Buckets {
			targets, err := q.ReadProjectEnvironmentCloneTargetBucketNotifications(ctx, db, sqlc.ReadProjectEnvironmentCloneTargetBucketNotificationsParams{
				OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), SourceBucketID: mustPgUUID(source.ID)})
			if err != nil {
				return mapErr(err)
			}
			for _, target := range targets {
				id := uuidString(target.BucketID)
				stored := api.ObjectBucketNotifications{BucketID: id, Rules: []api.ObjectNotificationRule{}}
				if target.Present {
					p, err := decodeObjectNotificationPolicy(id, target.Revision, target.Rules)
					if err != nil {
						return err
					}
					if stored, err = decodeManagedObjectNotifications(p, target.ManagedRules); err != nil {
						return err
					}
				}
				if err := validateCloneNotificationCopy(source.Notifications, stored, target.Present, target.ClonePaused); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *PgStore) ResumeEnvironmentClonePausedBucketNotifications(ctx context.Context, accountID, projectID, environment string) ([]string, error) {
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
	if _, err := sqlc.New().ReadProjectEnvironmentCloneFlagScope(ctx, s.pool, sqlc.ReadProjectEnvironmentCloneFlagScopeParams{
		AccountID: account, ProjectID: project, Environment: environment}); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, mapErr(err)
	}
	ids, err := sqlc.New().ResumeEnvironmentClonePausedBucketNotifications(ctx, s.pool, sqlc.ResumeEnvironmentClonePausedBucketNotificationsParams{
		AccountID: account, ProjectID: project, Environment: environment})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, uuidString(id))
	}
	slices.Sort(out)
	return out, nil
}

package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func taskToolReleaseFresh(ctx context.Context, tx pgx.Tx, account, key string, intent api.TaskToolReleaseIntent, configurationOnly bool) (bool, error) {
	_, err := toolpublicationdb.New().LockTaskToolReleaseKey(ctx, tx, toolpublicationdb.LockTaskToolReleaseKeyParams{AccountID: mustPgUUID(account), ID: mustPgUUID(key)})
	if errors.Is(mapErr(err), ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	apps := slices.Clone(intent.Apps)
	slices.SortFunc(apps, func(a, b api.TaskToolReleaseApp) int { return strings.Compare(a.Review.AppID, b.Review.AppID) })
	for _, app := range apps {
		_, err := toolpublicationdb.New().LockTaskToolReleaseApp(ctx, tx, toolpublicationdb.LockTaskToolReleaseAppParams{
			AccountID: mustPgUUID(account), ID: mustPgUUID(app.Review.AppID), Slug: app.App})
		if errors.Is(mapErr(err), ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		current, err := readReleaseReview(ctx, tx, app.Review.AppID, api.DefaultEnvScope)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if configurationOnly {
			current.LatestDeploymentID = app.Review.LatestDeploymentID
			current.ServingDeploymentIDs = app.Review.ServingDeploymentIDs
		}
		if matches := checkReleaseReview(app.Review, current) == nil; !matches {
			return false, nil
		}
	}
	return true, nil
}

func (s *PgStore) PrepareTaskToolReleaseIntent(ctx context.Context, account, key, id string, intent api.TaskToolReleaseIntent) (api.TaskToolReleaseIntentRecord, error) {
	record, err := tasktoolrelease.New(account, key, id, intent, time.Now())
	if err != nil {
		return record, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return record, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	if _, err := q.LockToolPublicationAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return record, mapErr(err)
	}
	body, err := q.GetTaskToolReleaseIntent(ctx, tx, toolpublicationdb.GetTaskToolReleaseIntentParams{AccountID: mustPgUUID(account), ID: id})
	if err == nil {
		// Do not decode through the provisional intent's caller-owned maps or slices.
		var retained api.TaskToolReleaseIntentRecord
		if err := json.Unmarshal(body, &retained); err != nil {
			return retained, err
		}
		if retained.RequesterKeyID != key || !reflect.DeepEqual(retained.Intent, intent) {
			return retained, ErrConflict
		}
		return tasktoolrelease.View(retained, time.Now()), nil
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return record, mapErr(err)
	}
	return insertTaskToolRelease(ctx, tx, account, record)
}

func insertTaskToolRelease(ctx context.Context, tx pgx.Tx, account string, record api.TaskToolReleaseIntentRecord) (api.TaskToolReleaseIntentRecord, error) {
	fresh, err := taskToolReleaseFresh(ctx, tx, account, record.RequesterKeyID, record.Intent, false)
	if err != nil {
		return record, err
	}
	if !fresh {
		return record, ErrConflict
	}
	q := toolpublicationdb.New()
	count, err := q.CountTaskToolReleaseIntents(ctx, tx, mustPgUUID(account))
	if err != nil {
		return record, err
	}
	if count >= api.TaskToolReleaseIntentsPerAccount {
		return record, ErrTaskToolReleaseLimit
	}
	body, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	_, err = q.InsertTaskToolReleaseIntent(ctx, tx, toolpublicationdb.InsertTaskToolReleaseIntentParams{AccountID: mustPgUUID(account), ID: record.ID, Payload: body})
	if err != nil {
		return record, mapErr(err)
	}
	return record, tx.Commit(ctx)
}

func (s *PgStore) GetTaskToolReleaseIntent(ctx context.Context, account, id string) (api.TaskToolReleaseIntentRecord, error) {
	body, err := toolpublicationdb.New().GetTaskToolReleaseIntent(ctx, s.pool, toolpublicationdb.GetTaskToolReleaseIntentParams{AccountID: mustPgUUID(account), ID: id})
	var record api.TaskToolReleaseIntentRecord
	if err != nil {
		return record, mapErr(err)
	}
	err = json.Unmarshal(body, &record)
	return tasktoolrelease.View(record, time.Now()), err
}

func (s *PgStore) ChangeTaskToolReleaseIntent(ctx context.Context, account, id, action string, in api.TaskToolReleaseIntentDecision) (api.TaskToolReleaseIntentRecord, error) {
	var record api.TaskToolReleaseIntentRecord
	tx, body, err := s.lockTaskToolRelease(ctx, account, id)
	if err != nil {
		return record, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	if err := json.Unmarshal(body, &record); err != nil {
		return record, err
	}
	configurationOnly := tasktoolrelease.IsRecoveryDecision(in.Decision) || action == "execution" && in.Execution != nil && in.Execution.Action != "claim"
	freshKey := record.RequesterKeyID
	if tasktoolrelease.IsRecoveryDecision(in.Decision) {
		freshKey = in.AuthorizerKeyID
	}
	fresh, err := taskToolReleaseFresh(ctx, tx, account, freshKey, record.Intent, configurationOnly)
	if err != nil {
		return record, err
	}
	if tasktoolrelease.IsRecoveryDecision(in.Decision) && in.Recovery != nil {
		snapshot := in.Recovery.Qualification
		if snapshot == nil && record.Execution != nil && record.Execution.Recovery != nil {
			snapshot = record.Execution.Recovery.Qualification
		}
		if snapshot != nil {
			snapshotFresh, snapshotErr := taskToolReleaseFresh(ctx, tx, account, in.AuthorizerKeyID, api.TaskToolReleaseIntent{Apps: snapshot.NativeApps}, false)
			if snapshotErr != nil {
				return record, snapshotErr
			}
			fresh = fresh && snapshotFresh
		}
	}
	if action == "execution" || in.Decision == "authorize" || tasktoolrelease.IsRecoveryDecision(in.Decision) {
		owner := in.AuthorizerKeyID
		if action == "execution" && record.Execution != nil {
			owner = record.Execution.AuthorizerKeyID
		}
		if owner == "" {
			return record, ErrConflict
		}
		_, ownerErr := q.LockTaskToolExecutionOwnerKey(ctx, tx, toolpublicationdb.LockTaskToolExecutionOwnerKeyParams{AccountID: mustPgUUID(account), ID: mustPgUUID(owner)})
		if ownerErr != nil && !errors.Is(mapErr(ownerErr), ErrNotFound) {
			return record, ownerErr
		}
		if tasktoolrelease.IsRecoveryDecision(in.Decision) && ownerErr != nil {
			return record, ErrConflict
		}
		fresh = fresh && ownerErr == nil
	}
	record, err = changeTaskToolRelease(record, action, in, fresh)
	if err != nil {
		return record, err
	}
	body, err = json.Marshal(record)
	if err != nil {
		return record, err
	}
	if err := q.UpdateTaskToolReleaseIntent(ctx, tx, toolpublicationdb.UpdateTaskToolReleaseIntentParams{AccountID: mustPgUUID(account), ID: id, Status: record.Status, Payload: body}); err != nil {
		return record, mapErr(err)
	}
	return record, tx.Commit(ctx)
}

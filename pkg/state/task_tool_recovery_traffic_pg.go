package state

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func (s *PgStore) lockTaskRecoveryTraffic(ctx context.Context, tx pgx.Tx, id string, percent int, expected []string) (*api.TaskToolReleaseIntentRecord, error) {
	g := taskRecoveryTrafficFrom(ctx)
	if g == nil {
		return nil, nil
	}
	body, err := toolpublicationdb.New().LockTaskToolReleaseIntentNowait(ctx, tx, toolpublicationdb.LockTaskToolReleaseIntentNowaitParams{AccountID: mustPgUUID(g.account), ID: g.intent})
	if err != nil {
		return nil, ErrTaskToolRecoveryChanged
	}
	var r api.TaskToolReleaseIntentRecord
	if json.Unmarshal(body, &r) != nil || !taskRecoveryTarget(r, g, id, percent, expected) {
		return nil, ErrTaskToolRecoveryChanged
	}
	if _, err := tasktoolrelease.Restore(r, g.request, time.Now()); err != nil {
		return nil, ErrTaskToolRecoveryChanged
	}
	return &r, nil
}

func (s *PgStore) prepareTaskRecoveryTraffic(ctx context.Context, tx pgx.Tx, r *api.TaskToolReleaseIntentRecord) (*api.TaskToolReleaseIntentRecord, error) {
	if r == nil {
		return nil, nil
	}
	g := taskRecoveryTrafficFrom(ctx)
	h := r.Execution.Recovery
	q := toolpublicationdb.New()
	if _, err := q.LockTaskToolExecutionOwnerKey(ctx, tx, toolpublicationdb.LockTaskToolExecutionOwnerKeyParams{AccountID: mustPgUUID(g.account), ID: mustPgUUID(h.AuthorizerKeyID)}); err != nil {
		return nil, ErrTaskToolRecoveryChanged
	}
	apps := slices.Clone(h.Qualification.NativeApps)
	slices.SortFunc(apps, func(a, b api.TaskToolReleaseApp) int { return strings.Compare(a.Review.AppID, b.Review.AppID) })
	for _, pin := range apps {
		if _, err := q.LockTaskRecoveryApp(ctx, tx, toolpublicationdb.LockTaskRecoveryAppParams{AccountID: mustPgUUID(g.account), ID: mustPgUUID(pin.Review.AppID)}); err != nil {
			return nil, ErrTaskToolRecoveryChanged
		}
	}
	fresh, err := taskToolReleaseFresh(ctx, tx, g.account, g.key, api.TaskToolReleaseIntent{Apps: h.Qualification.NativeApps}, false)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return nil, ErrTaskToolRecoveryChanged
	}
	metricApp := ""
	for _, pin := range h.Qualification.NativeApps {
		rows, err := q.LockTaskRecoveryInstances(ctx, tx, mustPgUUID(pin.Review.AppID))
		if err != nil {
			return nil, err
		}
		instances := make([]api.InstanceResponse, 0, len(rows))
		for _, row := range rows {
			instances = append(instances, api.InstanceResponse{ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID, State: row.State})
		}
		if !taskRecoveryInstancesOK(*r, pin, instances, g.request.ExpectedInstanceIDs[pin.Review.AppID]) {
			return nil, ErrTaskToolRecoveryChanged
		}
		if pin.App == r.Intent.ObserverMetricApp {
			metricApp = pin.Review.AppID
		}
	}
	if metricApp == "" {
		return nil, ErrTaskToolRecoveryChanged
	}
	metric, err := q.LockTaskRecoveryHeartbeat(ctx, tx, mustPgUUID(metricApp))
	if err != nil || !metric.ObservedAt.Valid || !taskRecoveryHeartbeatOK(metric.Value, metric.ObservedAt.Time, time.Now()) {
		return nil, ErrTaskToolRecoveryChanged
	}
	out, err := tasktoolrelease.Restore(*r, g.request, time.Now())
	if err != nil {
		return nil, ErrTaskToolRecoveryChanged
	}
	return &out, nil
}

func completeTaskRecoveryTraffic(ctx context.Context, tx pgx.Tx, r *api.TaskToolReleaseIntentRecord) error {
	if r == nil {
		return nil
	}
	g := taskRecoveryTrafficFrom(ctx)
	metricApp := ""
	for _, pin := range r.Execution.Recovery.Qualification.NativeApps {
		if pin.App == r.Intent.ObserverMetricApp {
			metricApp = pin.Review.AppID
		}
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	count, err := toolpublicationdb.New().CompleteTaskRecoveryTraffic(ctx, tx, toolpublicationdb.CompleteTaskRecoveryTrafficParams{AccountID: mustPgUUID(g.account), ID: g.intent, Payload: body,
		OwnerKeyID: mustPgUUID(r.Execution.Recovery.AuthorizerKeyID), RequesterKeyID: mustPgUUID(g.key), MetricAppID: mustPgUUID(metricApp), FreshnessSeconds: api.CustomMetricFreshnessSeconds})
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrTaskToolRecoveryChanged
	}
	return nil
}

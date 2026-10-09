// adr: 712
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runDurableEntityAlarms(ctx context.Context) {
	if s.durableEntities == nil || !s.durableEntityAlarmsEnabled {
		return
	}
	cursor := alarmSweepCursor{}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		next, err := s.sweepDurableEntityAlarms(ctx, cursor)
		cursor = next
		if err != nil && ctx.Err() == nil {
			// Provider errors can contain credentials or customer state.
			s.log.Warn("durable entity alarm discovery failed")
		}
		timer.Reset(api.DurableEntityAlarmPollInterval)
	}
}

type alarmSweepCursor struct{ index, entities string }

func scanDurableEntityAlarmPage(ctx context.Context, cursor string, scan func(context.Context, string) (durableentity.AlarmPage, error)) (durableentity.AlarmPage, error) {
	scanCtx, cancel := context.WithTimeout(ctx, api.DurableEntityAlarmScanTimeout)
	defer cancel()
	return scan(scanCtx, cursor)
}

func (s *server) sweepDurableEntityAlarms(ctx context.Context, cursor alarmSweepCursor) (alarmSweepCursor, error) {
	indexed, indexErr := scanDurableEntityAlarmPage(ctx, cursor.index, s.durableEntities.ScanIndexedDueAlarms)
	reconciled, reconcileErr := scanDurableEntityAlarmPage(ctx, cursor.entities, s.durableEntities.ScanDueAlarms)
	// A failed/expired native cursor restarts only its own disposable scan. The
	// other scan can keep delivering while the provider repairs its listing.
	next := alarmSweepCursor{index: indexed.NextCursor, entities: reconciled.NextCursor}
	page := durableentity.AlarmPage{Failed: indexed.Failed + reconciled.Failed, Alarms: append(indexed.Alarms, reconciled.Alarms...)}
	if page.Failed > 0 {
		s.log.Warn("durable entity alarm state unavailable", "entities", page.Failed)
	}
	seen := map[durableentity.ID]uint64{}
	for _, alarm := range page.Alarms {
		if ctx.Err() != nil {
			return next, ctx.Err()
		}
		if seen[alarm.Entity] == alarm.Version || !s.durableEntityApps[alarm.Entity.AppID] {
			continue
		}
		seen[alarm.Entity] = alarm.Version
		if err := s.deliverDurableEntityAlarm(ctx, alarm); err != nil && ctx.Err() == nil {
			if !errors.Is(err, durableentity.ErrAlarmObsolete) && !errors.Is(err, durableentity.ErrAlarmBackoff) && !errors.Is(err, durableentity.ErrAlarmExhausted) && !errors.Is(err, durableentity.ErrBusy) && !errors.Is(err, durableentity.ErrConflict) {
				s.log.Warn("durable entity alarm delivery deferred")
			}
		}
	}
	return next, errors.Join(indexErr, reconcileErr)
}

func (s *server) deliverDurableEntityAlarm(ctx context.Context, alarm durableentity.Alarm) (err error) {
	if s.durableEntities == nil || !s.durableEntityAlarmsEnabled || !s.durableEntityApps[alarm.Entity.AppID] {
		return durableentity.ErrInvalid
	}
	s.durableEntityMetrics.observeAlarmDelay(alarm.At)
	var result durableentity.Result
	defer func() { s.durableEntityMetrics.observeResult("alarm", result, err) }()
	callCtx, cancel := context.WithTimeout(ctx, api.DurableEntityInvokeTimeout)
	defer cancel()
	acct, app, request, err := s.durableEntityAlarmSelection(callCtx, alarm)
	if err != nil {
		return err
	}
	id, scope, problem := s.durableEntityIdentity((&http.Request{}).WithContext(callCtx), acct, app, request)
	if problem != nil {
		return problem
	}
	if id != alarm.Entity {
		return durableentity.ErrAlarmObsolete
	}
	result, err = s.durableEntities.InvokeAlarm(callCtx, alarm, s.durableEntityOwner, func(ctx context.Context, view durableentity.View) (durableentity.Transition, error) {
		return s.dispatchDurableEntityHandler(ctx, acct, app, scope, alarm.Entity, request, view, "alarm")
	})
	return err
}

func (s *server) durableEntityAlarmSelection(ctx context.Context, alarm durableentity.Alarm) (state.Account, state.App, api.DurableEntityInvokeRequest, error) {
	acct, err := s.store.AccountByID(ctx, alarm.Entity.AccountID)
	if err != nil {
		return acct, state.App{}, api.DurableEntityInvokeRequest{}, err
	}
	app, err := s.store.AppByID(ctx, alarm.Entity.AppID)
	if err != nil {
		return acct, app, api.DurableEntityInvokeRequest{}, err
	}
	if app.AccountID != acct.ID || app.DeletedAt != nil || app.Status == state.AppDeleted || !app.AcceptsRequestInvocations() ||
		acct.Status != state.AccountActive && acct.Status != state.AccountPastDue || acct.DeletionRequestedAt != nil || !api.MustLimitsFor(acct.Plan).AsyncInvokeAllowed {
		return acct, app, api.DurableEntityInvokeRequest{}, durableentity.ErrInvalid
	}
	work := durableentity.AlarmRequest(alarm)
	request := api.DurableEntityInvokeRequest{Namespace: alarm.Entity.Namespace, Key: alarm.Entity.Key, RequestID: work.ID, Payload: work.Payload, PlatformTenantID: alarm.Entity.TenantID}
	if app.ProjectID == "" || app.PreviewOfSlug != "" {
		if alarm.Entity.EnvironmentID != app.ID {
			return acct, app, request, durableentity.ErrInvalid
		}
		return acct, app, request, nil
	}
	environments, err := s.store.ListProjectEnvironments(ctx, acct.ID, app.ProjectID)
	if err != nil {
		return acct, app, request, err
	}
	for _, env := range environments {
		if env.ID == alarm.Entity.EnvironmentID {
			request.Environment = env.Slug
			return acct, app, request, nil
		}
	}
	return acct, app, request, durableentity.ErrAlarmObsolete
}

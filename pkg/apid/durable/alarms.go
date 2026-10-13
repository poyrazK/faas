// adr: 712
package durable

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) RunAlarms(ctx context.Context) {
	if s.Engine == nil || !s.AlarmsEnabled {
		return
	}
	cursor := AlarmCursor{}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		next, err := s.SweepAlarms(ctx, cursor)
		cursor = next
		if err != nil && ctx.Err() == nil {
			// Provider errors can contain credentials or customer state.
			s.Host.Logger().Warn("durable entity alarm discovery failed")
		}
		timer.Reset(api.DurableEntityAlarmPollInterval)
	}
}

type AlarmCursor struct{ index, entities string }

func scanDurableEntityAlarmPage(ctx context.Context, cursor string, scan func(context.Context, string) (durableentity.AlarmPage, error)) (durableentity.AlarmPage, error) {
	scanCtx, cancel := context.WithTimeout(ctx, api.DurableEntityAlarmScanTimeout)
	defer cancel()
	return scan(scanCtx, cursor)
}

func (s *Service) SweepAlarms(ctx context.Context, cursor AlarmCursor) (AlarmCursor, error) {
	indexed, indexErr := scanDurableEntityAlarmPage(ctx, cursor.index, s.Engine.ScanIndexedDueAlarms)
	reconciled, reconcileErr := scanDurableEntityAlarmPage(ctx, cursor.entities, s.Engine.ScanDueAlarms)
	// A failed/expired native cursor restarts only its own disposable scan. The
	// other scan can keep delivering while the provider repairs its listing.
	next := AlarmCursor{index: indexed.NextCursor, entities: reconciled.NextCursor}
	page := durableentity.AlarmPage{Failed: indexed.Failed + reconciled.Failed, Alarms: append(indexed.Alarms, reconciled.Alarms...)}
	if page.Failed > 0 {
		s.Host.Logger().Warn("durable entity alarm state unavailable", "entities", page.Failed)
	}
	seen := map[durableentity.ID]uint64{}
	for _, alarm := range page.Alarms {
		if ctx.Err() != nil {
			return next, ctx.Err()
		}
		if seen[alarm.Entity] == alarm.Version || !s.Apps[alarm.Entity.AppID] {
			continue
		}
		seen[alarm.Entity] = alarm.Version
		if err := s.DeliverAlarm(ctx, alarm); err != nil && ctx.Err() == nil {
			if !errors.Is(err, durableentity.ErrAlarmObsolete) && !errors.Is(err, durableentity.ErrAlarmBackoff) && !errors.Is(err, durableentity.ErrAlarmExhausted) && !errors.Is(err, durableentity.ErrBusy) && !errors.Is(err, durableentity.ErrConflict) {
				s.Host.Logger().Warn("durable entity alarm delivery deferred")
			}
		}
	}
	return next, errors.Join(indexErr, reconcileErr)
}

func (s *Service) DeliverAlarm(ctx context.Context, alarm durableentity.Alarm) (err error) {
	if s.Engine == nil || !s.AlarmsEnabled || !s.Apps[alarm.Entity.AppID] {
		return durableentity.ErrInvalid
	}
	s.Metrics.observeAlarmDelay(alarm.At)
	var result durableentity.Result
	started := time.Now()
	defer s.Metrics.observeDuration("alarm", started)
	defer func() { s.Metrics.observeResult("alarm", result, err) }()
	callCtx, cancel := context.WithTimeout(ctx, api.DurableEntityInvokeTimeout)
	defer cancel()
	acct, app, request, err := s.durableEntityAlarmSelection(callCtx, alarm)
	if err != nil {
		return err
	}
	id, scope, problem := s.Identity((&http.Request{}).WithContext(callCtx), acct, app, request)
	if problem != nil {
		return problem
	}
	if id != alarm.Entity {
		return durableentity.ErrAlarmObsolete
	}
	if s.TenantQuotas != nil {
		if err := s.TenantQuotas.AdmitInvocation(callCtx, id); err != nil {
			return err
		}
	}
	result, err = s.Engine.InvokeAlarm(callCtx, alarm, s.Owner, func(ctx context.Context, view durableentity.View) (durableentity.Transition, error) {
		return s.dispatchDurableEntityHandler(ctx, acct, app, scope, alarm.Entity, request, view, "alarm")
	})
	return err
}

func (s *Service) durableEntityAlarmSelection(ctx context.Context, alarm durableentity.Alarm) (state.Account, state.App, api.DurableEntityInvokeRequest, error) {
	acct, app, request, err := s.durableEntityBackgroundSelection(ctx, alarm.Entity)
	work := durableentity.AlarmRequest(alarm)
	request.RequestID, request.Payload = work.ID, work.Payload
	return acct, app, request, err
}

func (s *Service) durableEntityBackgroundSelection(ctx context.Context, entity durableentity.ID) (state.Account, state.App, api.DurableEntityInvokeRequest, error) {
	acct, err := s.Host.Store().AccountByID(ctx, entity.AccountID)
	if err != nil {
		return acct, state.App{}, api.DurableEntityInvokeRequest{}, err
	}
	app, err := s.Host.Store().AppByID(ctx, entity.AppID)
	if err != nil {
		return acct, app, api.DurableEntityInvokeRequest{}, err
	}
	if app.AccountID != acct.ID || app.DeletedAt != nil || app.Status == state.AppDeleted || !app.AcceptsRequestInvocations() ||
		acct.Status != state.AccountActive && acct.Status != state.AccountPastDue || acct.DeletionRequestedAt != nil || !api.MustLimitsFor(acct.Plan).AsyncInvokeAllowed {
		return acct, app, api.DurableEntityInvokeRequest{}, durableentity.ErrInvalid
	}
	request := api.DurableEntityInvokeRequest{Namespace: entity.Namespace, Key: entity.Key, PlatformTenantID: entity.TenantID}
	if app.ProjectID == "" || app.PreviewOfSlug != "" {
		if entity.EnvironmentID != app.ID {
			return acct, app, request, durableentity.ErrInvalid
		}
		return acct, app, request, nil
	}
	environments, err := s.Host.Store().ListProjectEnvironments(ctx, acct.ID, app.ProjectID)
	if err != nil {
		return acct, app, request, err
	}
	for _, env := range environments {
		if env.ID == entity.EnvironmentID {
			request.Environment = env.Slug
			return acct, app, request, nil
		}
	}
	return acct, app, request, durableentity.ErrAlarmObsolete
}

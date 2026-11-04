package managedrealtime

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"sync"
	"time"
)

func realtimeScheduledFailureCode(err error) string {
	var scheduledCondition *state.ManagedRealtimeScheduleConditionFailure
	var version *state.ManagedRealtimeEntityVersionConflict
	var condition *state.ManagedRealtimeConditionConflict
	var reducer *state.ManagedRealtimeReducerError
	var schema *state.ManagedRealtimeEventSchemaError
	switch {
	case errors.As(err, &scheduledCondition):
		return "realtime_schedule_condition_failed"
	case errors.As(err, &version):
		return api.CodeRealtimeEntityVersionConflict
	case errors.As(err, &condition):
		return api.CodeRealtimeConditionConflict
	case errors.As(err, &reducer), errors.As(err, &schema), errors.Is(err, state.ErrManagedRealtimeHistoryInvalid):
		return api.CodeRealtimeInvalid
	case errors.Is(err, state.ErrManagedRealtimeHistoryLimit):
		return api.CodeCapacity
	}
	return "realtime_schedule_unavailable"
}
func (s *Service) RunManagedRealtimeSchedules(ctx context.Context) {
	store, ok := s.Host.Store().(state.ManagedRealtimeScheduleStore)
	if !ok || !s.HistoryPreviewEnabled {
		return
	}
	log := s.Host.Logger()
	if log == nil {
		log = slog.Default()
	}
	Run := func() {
		// A pass lists up to scheduleListBatch due schedules; while a pass
		// finds a full list, the next one starts at once, up to
		// scheduleRoundsPerTick (ADR-1323, amendment of 2026-11-03).
		for range scheduleRoundsPerTick {
			if ctx.Err() != nil || s.runScheduleRound(ctx, store, log) < scheduleListBatch {
				return
			}
		}
	}
	Run()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			Run()
		}
	}
}

// scheduleWritePass is what one schedule pass learned: each endpoint's account
// and how many of an account's schedules it postponed.
type scheduleWritePass struct {
	accounts map[string]string
	deferred map[string]int
}

// admitScheduledWrite takes one write for a due schedule from its account's
// realtime write rate. Over the rate, the schedule is postponed, without
// counting an attempt, until the account's bucket has room for it and for the
// account's schedules postponed before it in this pass, so a busy account's
// schedules leave the due list to other accounts'. A failed lookup admits the
// schedule: the publish reports its own errors.
func (s *Service) admitScheduledWrite(ctx context.Context, row state.ManagedRealtimeSchedule, pass *scheduleWritePass) bool {
	accountID, known := pass.accounts[row.EndpointID]
	if !known {
		endpoints, ok := realtimeEndpointStore(s)
		if !ok {
			return true
		}
		ep, err := endpoints.ManagedRealtimeEndpointByID(ctx, row.EndpointID)
		if err != nil {
			return true
		}
		accountID = ep.AccountID
		pass.accounts[row.EndpointID] = accountID
	}
	limits, wait, ok, err := s.takeAccountWrites(ctx, accountID, 1)
	if err != nil || ok {
		return true
	}
	if deferrer, available := s.Host.Store().(state.ManagedRealtimeScheduleDeferStore); available {
		wait += time.Duration(pass.deferred[accountID]) * time.Second / time.Duration(limits.RetainedWritesPerSecond)
		pass.deferred[accountID]++
		if err := deferrer.DeferManagedRealtimeSchedule(ctx, row, time.Now().Add(wait)); err != nil && s.Host.Logger() != nil {
			s.Host.Logger().Warn("realtime schedule deferral failed", "schedule_id", row.ID, "err", err)
		}
	}
	return false
}

const (
	scheduleListBatch     = 128
	scheduleRoundsPerTick = 20
	// schedulePublishWorkers publishes this many channels' due schedules at
	// once; one channel's schedules publish in due order.
	schedulePublishWorkers = 8
)

// runScheduleRound publishes one list of due schedules and reports how many
// were due.
func (s *Service) runScheduleRound(ctx context.Context, store state.ManagedRealtimeScheduleStore, log *slog.Logger) int {
	passCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	due, err := store.ListDueManagedRealtimeSchedules(passCtx, scheduleListBatch)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Warn("managed realtime schedule scan failed", "err", err)
		}
		return 0
	}
	pass := scheduleWritePass{accounts: map[string]string{}, deferred: map[string]int{}}
	type channelKey struct{ endpoint, channel string }
	var order []channelKey
	groups := map[channelKey][]state.ManagedRealtimeSchedule{}
	for _, schedule := range due {
		if passCtx.Err() != nil {
			return 0
		}
		if !s.admitScheduledWrite(passCtx, schedule, &pass) {
			continue
		}
		key := channelKey{schedule.EndpointID, schedule.Channel}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], schedule)
	}
	work := make(chan []state.ManagedRealtimeSchedule)
	var wg sync.WaitGroup
	for range min(schedulePublishWorkers, len(order)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range work {
				for _, schedule := range group {
					if passCtx.Err() != nil {
						break
					}
					s.publishDueSchedule(passCtx, store, log, schedule)
				}
			}
		}()
	}
	for _, key := range order {
		work <- groups[key]
	}
	close(work)
	wg.Wait()
	if passCtx.Err() != nil {
		return 0
	}
	return len(due)
}

// publishDueSchedule publishes one admitted due schedule and fans it out.
func (s *Service) publishDueSchedule(ctx context.Context, store state.ManagedRealtimeScheduleStore, log *slog.Logger, schedule state.ManagedRealtimeSchedule) {
	msg, err := store.PublishManagedRealtimeSchedule(ctx, schedule)
	if errors.Is(err, state.ErrManagedRealtimeScheduleSkipped) || errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		return
	}
	if err != nil {
		if code := realtimeScheduledFailureCode(err); ctx.Err() == nil {
			if failureErr := store.FailManagedRealtimeSchedule(ctx, schedule, code); failureErr != nil {
				log.Warn("realtime schedule failure recording failed", "schedule_id", schedule.ID, "err", failureErr)
			}
		}
		log.Warn("managed realtime scheduled publish failed", "endpoint_id", schedule.EndpointID, "channel", schedule.Channel, "schedule_id", schedule.ID, "err", err)
		return
	}
	// Publication and terminal status committed together; fanout failures do not
	// republish. Replay recovers events missed during outages or a worker crash.
	if s.Owner == nil {
		return
	}
	payload := realtime.Message{Data: msg.Data, Binary: msg.Binary, Metadata: msg.Metadata, ExpiresAt: msg.ExpiresAt}
	if publisher, ok := s.Owner.(realtimeRetainedPublishStatus); ok {
		result, publishErr := publisher.PublishRetainedWithStatus(ctx, msg.EndpointID, msg.Channel, payload, msg.Sequence)
		if publishErr != nil || result.Partial || result.NodesUnavailable > 0 || result.QueueFull > 0 || result.Failed > 0 {
			log.Warn("scheduled realtime publish committed with incomplete fanout", "schedule_id", schedule.ID, "sequence", msg.Sequence, "err", publishErr)
		}
	} else if _, err := s.Owner.Publish(ctx, msg.EndpointID, msg.Channel, payload); err != nil {
		log.Warn("scheduled realtime publish live fanout failed", "schedule_id", schedule.ID, "sequence", msg.Sequence, "err", err)
	}
}

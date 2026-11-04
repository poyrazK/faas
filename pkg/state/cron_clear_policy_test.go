package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// A cron's schedule policy and failure rules can be removed; an update that
// omits them keeps them, and one that both sets and clears is refused. Once
// set, nothing could remove either (crons audit 2026-11-03).
func TestUpdateCronClearsPolicyAndFailureRules(t *testing.T) {
	cronAnchorBackends(t, func(t *testing.T, ctx context.Context, s state.Store, appID, _ string) {
		policy := &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "skip", MissedRuns: "skip"}
		rules := &workpolicy.FailureRules{Version: workpolicy.Version, Rules: []workpolicy.FailureRule{}, UnmatchedFailure: "retry", UncertainOutcome: "hold"}
		c, err := s.CreateCron(ctx, appID, "0 3 * * *", "/nightly", true)
		if err != nil {
			t.Fatal(err)
		}
		update := func(opts state.CronOptions) (state.Cron, error) {
			return s.UpdateCronWithOptions(ctx, c.ID, nil, nil, nil, nil, nil, nil, opts)
		}
		set, err := update(state.CronOptions{SchedulePolicy: policy, FailureRules: rules})
		if err != nil || set.SchedulePolicy == nil || set.FailureRules == nil {
			t.Fatalf("set = %+v, %v", set, err)
		}
		kept, err := update(state.CronOptions{})
		if err != nil || kept.SchedulePolicy == nil || kept.FailureRules == nil {
			t.Fatalf("an update without them dropped them: %+v, %v", kept, err)
		}
		if _, err := update(state.CronOptions{SchedulePolicy: policy, ClearSchedulePolicy: true}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("set and clear together = %v, want ErrInvalidArgument", err)
		}
		cleared, err := update(state.CronOptions{ClearSchedulePolicy: true})
		if err != nil || cleared.SchedulePolicy != nil || cleared.FailureRules == nil {
			t.Fatalf("clear policy = %+v, %v", cleared, err)
		}
		cleared, err = update(state.CronOptions{ClearFailureRules: true})
		if err != nil || cleared.SchedulePolicy != nil || cleared.FailureRules != nil {
			t.Fatalf("clear rules = %+v, %v", cleared, err)
		}
		read, err := s.CronByID(ctx, c.ID)
		if err != nil || read.SchedulePolicy != nil || read.FailureRules != nil {
			t.Fatalf("stored cron = %+v, %v", read, err)
		}
	})
}

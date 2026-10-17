package issuetriage

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/url"
	"strconv"
)

func ParseReminderPolicy(form url.Values) (state.IssueApplicationReminderPolicy, error) {
	in := state.IssueApplicationReminderPolicy{Enabled: form.Get("enabled") == "1"}
	var err error
	in.Revision, err = strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil {
		return in, state.ErrInvalidArgument
	}
	in.IntervalMinutes, err = strconv.Atoi(form.Get("interval_minutes"))
	if err != nil {
		return in, state.ErrInvalidArgument
	}
	in.RepeatLimit, err = strconv.Atoi(form.Get("repeat_limit"))
	if err != nil {
		return in, state.ErrInvalidArgument
	}
	return in, nil
}
func EnqueueReminderBatch(ctx context.Context, store state.IssueApplicationReminderStore) error {
	for range api.IssueApplicationReminderBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		processed, err := store.EnqueueIssueApplicationReminder(ctx)
		if err != nil {
			return err
		}
		if !processed {
			return nil
		}
	}
	return ctx.Err()
}

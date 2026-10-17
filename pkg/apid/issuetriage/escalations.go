package issuetriage

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/url"
	"strconv"
)

func ParseEscalationPolicy(form url.Values) (state.IssueApplicationEscalationPolicy, error) {
	in := state.IssueApplicationEscalationPolicy{Enabled: form.Get("enabled") == "1", TeamID: form.Get("team_id")}
	var err error
	in.Revision, err = strconv.ParseInt(form.Get("expected_revision"), 10, 64)
	if err != nil {
		return in, state.ErrInvalidArgument
	}
	in.DelayMinutes, err = strconv.Atoi(form.Get("delay_minutes"))
	if err != nil {
		return in, state.ErrInvalidArgument
	}
	return in, nil
}
func EnqueueEscalationBatch(ctx context.Context, store state.IssueApplicationEscalationStore) error {
	for range api.IssueApplicationEscalationBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := store.EnqueueIssueApplicationEscalation(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return ctx.Err()
}

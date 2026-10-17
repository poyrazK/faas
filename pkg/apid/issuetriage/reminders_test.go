package issuetriage_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"github.com/onebox-faas/faas/pkg/state"
	"net/url"
	"testing"
)

type reminderBatchStore struct {
	state.IssueApplicationReminderStore
	calls, stop int
	err         error
}

func (s *reminderBatchStore) EnqueueIssueApplicationReminder(context.Context) (bool, error) {
	s.calls++
	return s.calls < s.stop, s.err
}
func TestNativeReminderBatchBoundsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stop, want   int
		fail, cancel bool
	}{{"empty", 1, 1, false, false}, {"bounded", api.IssueApplicationReminderBatch + 1, api.IssueApplicationReminderBatch, false, false}, {"failure", 5, 1, true, false}, {"canceled", 5, 0, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &reminderBatchStore{stop: tc.stop}
			if tc.fail {
				s.err = errors.New("database failure")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			err := issuetriage.EnqueueReminderBatch(ctx, s)
			if s.calls != tc.want || (err != nil) != (tc.fail || tc.cancel) {
				t.Fatal(s.calls, err)
			}
		})
	}
}
func TestNativeReminderPolicyForm(t *testing.T) {
	f := url.Values{"enabled": {"1"}, "expected_revision": {"2"}, "interval_minutes": {"60"}, "repeat_limit": {"3"}}
	in, err := issuetriage.ParseReminderPolicy(f)
	if err != nil || !in.Enabled || in.Revision != 2 || in.IntervalMinutes != 60 || in.RepeatLimit != 3 {
		t.Fatal(in, err)
	}
	for _, field := range []string{"expected_revision", "interval_minutes", "repeat_limit"} {
		copy := url.Values{}
		for k, v := range f {
			copy[k] = append([]string{}, v...)
		}
		copy.Set(field, "invalid")
		if _, err := issuetriage.ParseReminderPolicy(copy); err == nil {
			t.Fatal(field)
		}
	}
}

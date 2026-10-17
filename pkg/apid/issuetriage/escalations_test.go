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

type escalationBatchStore struct {
	state.IssueApplicationEscalationStore
	calls, stop int
	err         error
}

func (s *escalationBatchStore) EnqueueIssueApplicationEscalation(context.Context) (bool, error) {
	s.calls++
	return s.calls < s.stop, s.err
}

// ADR-1494: escalation work stops at empty/failure/cancellation and a bounded batch.
func TestNativeEscalationBatchAndForm(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stop, want   int
		fail, cancel bool
	}{{"empty", 1, 1, false, false}, {"bounded", api.IssueApplicationEscalationBatch + 1, api.IssueApplicationEscalationBatch, false, false}, {"failure", 5, 1, true, false}, {"canceled", 5, 0, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &escalationBatchStore{stop: tc.stop}
			if tc.fail {
				s.err = errors.New("database failure")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			err := issuetriage.EnqueueEscalationBatch(ctx, s)
			if s.calls != tc.want || (err != nil) != (tc.fail || tc.cancel) {
				t.Fatal(s.calls, err)
			}
		})
	}
	form := url.Values{"enabled": {"1"}, "expected_revision": {"2"}, "delay_minutes": {"15"}, "team_id": {"backup"}}
	in, err := issuetriage.ParseEscalationPolicy(form)
	if err != nil || !in.Enabled || in.Revision != 2 || in.DelayMinutes != 15 || in.TeamID != "backup" {
		t.Fatal(in, err)
	}
	for _, field := range []string{"expected_revision", "delay_minutes"} {
		f := url.Values{}
		for k, v := range form {
			f[k] = v
		}
		f.Set(field, "invalid")
		if _, err := issuetriage.ParseEscalationPolicy(f); err == nil {
			t.Fatal(field)
		}
	}
}

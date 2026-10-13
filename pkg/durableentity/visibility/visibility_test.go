package visibility

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestRecoveryInterpretation(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	for _, tc := range []struct {
		name                        string
		alarm                       api.DurableEntityAlarmInspection
		outbox                      api.DurableEntityOutboxInspection
		wantAlarm, wantOutbox, code string
	}{
		{name: "empty", wantAlarm: "none", wantOutbox: "empty"},
		{name: "scheduled", alarm: api.DurableEntityAlarmInspection{AlarmAt: &future}, wantAlarm: "scheduled", wantOutbox: "empty"},
		{name: "due", alarm: api.DurableEntityAlarmInspection{AlarmAt: &past}, wantAlarm: "due", wantOutbox: "empty", code: "alarm_due"},
		{name: "backoff", alarm: api.DurableEntityAlarmInspection{AlarmAt: &past, NextAttemptAt: &future}, outbox: api.DurableEntityOutboxInspection{Pending: 1, NextAttemptAt: &future}, wantAlarm: "backoff", wantOutbox: "backoff", code: "delivery_unknown"},
		{name: "exhausted", alarm: api.DurableEntityAlarmInspection{AlarmAt: &past, Exhausted: true}, outbox: api.DurableEntityOutboxInspection{Pending: 1, Exhausted: true}, wantAlarm: "exhausted", wantOutbox: "exhausted", code: "outbox_exhausted"},
		{name: "transport_success_pending_ack", outbox: api.DurableEntityOutboxInspection{Pending: 1, HeadDelivery: &api.DurableEntityHeadDelivery{Status: "succeeded"}}, wantAlarm: "none", wantOutbox: "pending", code: "delivery_succeeded_pending"},
		{name: "transport_dead", outbox: api.DurableEntityOutboxInspection{Pending: 1, HeadDelivery: &api.DurableEntityHeadDelivery{Status: "dead"}}, wantAlarm: "none", wantOutbox: "pending", code: "delivery_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Summarize(api.DurableEntityInspectResponse{Version: 1, StateCommitted: true, Alarm: tc.alarm, Outbox: tc.outbox}, now)
			if r.AlarmStatus != tc.wantAlarm || r.OutboxStatus != tc.wantOutbox {
				t.Fatalf("wrong recovery classification: %+v", r)
			}
			if len(r.Limitations) != 3 {
				t.Fatal("observation limitations missing")
			}
			if tc.code != "" {
				found := false
				for _, f := range r.Findings {
					found = found || f.Code == tc.code
				}
				if !found {
					t.Fatalf("missing %s: %+v", tc.code, r)
				}
			}
		})
	}
}
func TestClockBoundaryDoesNotClaimStallOrDelivery(t *testing.T) {
	now := time.Now()
	r := Summarize(api.DurableEntityInspectResponse{Alarm: api.DurableEntityAlarmInspection{AlarmAt: &now}, Outbox: api.DurableEntityOutboxInspection{Pending: 1}}, now)
	if r.AlarmStatus != "due" || r.OutboxStatus != "pending" {
		t.Fatal(r)
	}
	if r.Findings[0].Code != "state_uncommitted" {
		t.Fatal(r.Findings)
	}
}

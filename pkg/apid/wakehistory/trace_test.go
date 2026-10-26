package wakehistory

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type wakeByIDSource struct {
	rows    []state.WakeHistoryRecord
	err     error
	gotApps []string
	gotIDs  []string
}

func (s *wakeByIDSource) ListWakeHistoryByWakeIDs(_ context.Context, appIDs, wakeIDs []string) ([]state.WakeHistoryRecord, error) {
	s.gotApps, s.gotIDs = appIDs, wakeIDs
	return s.rows, s.err
}

// thawSource also reads frozen-VM thaws.
type thawSource struct {
	*wakeByIDSource
	thaws              []state.WakeThawRecord
	err                error
	gotSince, gotUntil time.Time
}

func (s *thawSource) ListWakeThaws(_ context.Context, _, _ []string, since, until time.Time) ([]state.WakeThawRecord, error) {
	s.gotSince, s.gotUntil = since, until
	return s.thaws, s.err
}

func TestTraceWakes(t *testing.T) {
	at := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	app, otherApp := uuid.NewString(), uuid.NewString()
	restored, unfinished, noMeta, mismatched, fellBack := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	meta := func(app, wake, method string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"app_id": app, "wake_id": wake, "method": method, "trigger": "service.mesh"})
		return raw
	}
	source := &wakeByIDSource{rows: []state.WakeHistoryRecord{
		{AppID: app, WakeID: restored, StartedAt: at, BootAt: at, CompletedAt: at.Add(610 * time.Millisecond), BootData: meta(app, restored, "restore"), HasMeta: true},
		{AppID: app, WakeID: unfinished, StartedAt: at, BootAt: at, BootData: meta(app, unfinished, "cold_boot"), HasMeta: true},
		{AppID: app, WakeID: noMeta, StartedAt: at},
		{AppID: app, WakeID: mismatched, StartedAt: at, BootAt: at, BootData: meta(otherApp, mismatched, "restore"), HasMeta: true},
		{AppID: app, WakeID: fellBack, StartedAt: at, BootAt: at, CompletedAt: at.Add(3612 * time.Millisecond), BootData: meta(app, fellBack, "restore"), HasMeta: true,
			CompletedData: json.RawMessage(`{"method":"cold_boot","restore_fallback_reason":"after_restore_failed"}`)},
	}}
	cold := func(wake string) TraceRequest {
		return TraceRequest{AppID: app, WakeID: wake, ColdBoot: true, ReceivedAt: at, LatencyMS: 4000}
	}
	requests := []TraceRequest{
		cold(restored), cold(unfinished), cold(noMeta), cold(mismatched), cold(fellBack),
		{AppID: app, WakeID: restored, ReceivedAt: at, LatencyMS: 40}, // a warm request on the restored instance
		cold("not-a-wake"),
		{AppID: "not-an-app", WakeID: uuid.NewString(), ColdBoot: true},
		cold(uuid.NewString()), // a wake whose history is gone
	}
	got, err := TraceWakes(t.Context(), source, requests)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(source.gotApps, []string{app}) || len(source.gotIDs) != 6 {
		t.Fatalf("read apps %v and wakes %v, want malformed ids and warm requests dropped", source.gotApps, source.gotIDs)
	}
	want := []*api.AccountTraceWake{
		{WakeID: restored, Method: "restore", ReadyInMS: 610},
		{WakeID: unfinished, Method: "cold_boot"},
		{WakeID: noMeta},
		{WakeID: mismatched},
		// Hunt 12 H12-49: the method vmmd ran, not the one schedd planned.
		{WakeID: fellBack, Method: "cold_boot", ReadyInMS: 3612, PlannedMethod: "restore", RestoreFallbackReason: "after_restore_failed"},
		nil, // the warm request waited for no wake
		nil, nil, nil,
	}
	assertTraceWakes(t, got, want)

	many := make([]TraceRequest, api.AccountTraceWakesMax+5)
	for i := range many {
		many[i] = cold(uuid.NewString())
	}
	if _, err := TraceWakes(t.Context(), source, many); err != nil || len(source.gotIDs) != api.AccountTraceWakesMax {
		t.Fatalf("read %d wakes, want at most %d (%v)", len(source.gotIDs), api.AccountTraceWakesMax, err)
	}
	if got, err := TraceWakes(t.Context(), struct{}{}, []TraceRequest{cold(restored)}); err != nil || got != nil {
		t.Fatalf("a store without the capability = %+v, %v", got, err)
	}
	if got, err := TraceWakes(t.Context(), source, []TraceRequest{cold("not-a-wake")}); err != nil || got != nil {
		t.Fatalf("only malformed wake ids = %+v, %v; want no read", got, err)
	}
	boom := errors.New("down")
	if _, err := TraceWakes(t.Context(), &wakeByIDSource{err: boom}, []TraceRequest{cold(restored)}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the source failure", err)
	}
}

// Hunt 12 H12-49: a thaw resumes a frozen instance under its original wake ID,
// so a request that thawed one must not be credited with that wake's boot.
func TestTraceWakesNamesAThaw(t *testing.T) {
	booted := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	app, wake := uuid.NewString(), uuid.NewString()
	boot, _ := json.Marshal(map[string]any{"app_id": app, "wake_id": wake, "method": "restore"})
	history := &wakeByIDSource{rows: []state.WakeHistoryRecord{
		{AppID: app, WakeID: wake, StartedAt: booted, BootAt: booted, CompletedAt: booted.Add(309 * time.Millisecond), BootData: boot, HasMeta: true},
	}}
	later := booted.Add(10 * time.Minute) // the request's minute bucket
	source := &thawSource{wakeByIDSource: history, thaws: []state.WakeThawRecord{
		{WakeID: wake, At: later.Add(-5 * time.Minute), TotalMs: 95}, // an earlier thaw of the same instance
		{WakeID: wake, At: later.Add(17 * time.Second), TotalMs: 110},
	}}
	request := func(received time.Time) TraceRequest {
		return TraceRequest{AppID: app, WakeID: wake, ColdBoot: true, ReceivedAt: received, LatencyMS: 300}
	}
	tests := []struct {
		name    string
		source  any
		request TraceRequest
		want    *api.AccountTraceWake
	}{
		{"the request that woke the instance", source, request(booted), &api.AccountTraceWake{WakeID: wake, Method: "restore", ReadyInMS: 309}},
		{"a request that thawed it", source, request(later), &api.AccountTraceWake{WakeID: wake, Thawed: true, ThawMS: 110}},
		{"a later cold request without a thaw on record", history, request(later), &api.AccountTraceWake{WakeID: wake}},
		{"a warm request after the thaw", source, TraceRequest{AppID: app, WakeID: wake, ReceivedAt: later, LatencyMS: 20}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TraceWakes(t.Context(), tt.source, []TraceRequest{tt.request})
			if err != nil {
				t.Fatal(err)
			}
			assertTraceWakes(t, got, []*api.AccountTraceWake{tt.want})
		})
	}
	if _, err := TraceWakes(t.Context(), source, []TraceRequest{request(later)}); err != nil {
		t.Fatal(err)
	}
	if wantSince, wantUntil := later.Add(-300*time.Millisecond-traceClockSlack), later.Add(time.Minute+traceClockSlack); !source.gotSince.Equal(wantSince) || !source.gotUntil.Equal(wantUntil) {
		t.Fatalf("thaw window = [%s, %s], want [%s, %s]", source.gotSince, source.gotUntil, wantSince, wantUntil)
	}
	boom := errors.New("down")
	if _, err := TraceWakes(t.Context(), &thawSource{wakeByIDSource: history, err: boom}, []TraceRequest{request(later)}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the thaw read failure", err)
	}
}

func assertTraceWakes(t *testing.T, got, want []*api.AccountTraceWake) {
	t.Helper()
	if got == nil { // no request waited for a retained wake
		got = make([]*api.AccountTraceWake, len(want))
	}
	if len(got) != len(want) {
		t.Fatalf("wakes = %d, want %d", len(got), len(want))
	}
	for i := range want {
		switch {
		case want[i] == nil && got[i] != nil:
			t.Fatalf("request %d wake = %+v, want none", i, *got[i])
		case want[i] != nil && (got[i] == nil || *got[i] != *want[i]):
			t.Fatalf("request %d wake = %+v, want %+v", i, got[i], *want[i])
		}
	}
}

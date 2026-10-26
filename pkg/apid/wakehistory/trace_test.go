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
	got, err := TraceWakes(t.Context(), source, []string{app, "not-an-app"}, []string{restored, unfinished, noMeta, mismatched, fellBack, "not-a-wake"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(source.gotApps, []string{app}) || len(source.gotIDs) != 5 {
		t.Fatalf("read apps %v and wakes %v, want malformed ids dropped", source.gotApps, source.gotIDs)
	}
	want := map[string]api.AccountTraceWake{
		restored:   {WakeID: restored, Method: "restore", ReadyInMS: 610},
		unfinished: {WakeID: unfinished, Method: "cold_boot"},
		noMeta:     {WakeID: noMeta},
		mismatched: {WakeID: mismatched},
		// Hunt 12 H12-49: the method vmmd ran, not the one schedd planned.
		fellBack: {WakeID: fellBack, Method: "cold_boot", ReadyInMS: 3612, PlannedMethod: "restore", RestoreFallbackReason: "after_restore_failed"},
	}
	if len(got) != len(want) {
		t.Fatalf("wakes = %+v", got)
	}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("wake %s = %+v, want %+v", id, got[id], w)
		}
	}

	many := make([]string, api.AccountTraceWakesMax+5)
	for i := range many {
		many[i] = uuid.NewString()
	}
	if _, err := TraceWakes(t.Context(), source, []string{app}, many); err != nil || len(source.gotIDs) != api.AccountTraceWakesMax {
		t.Fatalf("read %d wakes, want at most %d (%v)", len(source.gotIDs), api.AccountTraceWakesMax, err)
	}
	if got, err := TraceWakes(t.Context(), struct{}{}, []string{app}, []string{restored}); err != nil || got != nil {
		t.Fatalf("a store without the capability = %+v, %v", got, err)
	}
	if got, err := TraceWakes(t.Context(), source, []string{app}, []string{"not-a-wake"}); err != nil || got != nil {
		t.Fatalf("only malformed wake ids = %+v, %v; want no read", got, err)
	}
	boom := errors.New("down")
	if _, err := TraceWakes(t.Context(), &wakeByIDSource{err: boom}, []string{app}, []string{restored}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the source failure", err)
	}
}

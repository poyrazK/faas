package operatorobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeTimelineReturnsOneWakeOldestFirst(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	app := uuid.New().String()
	base := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	add := func(kind, wake string, at time.Time, extra string) {
		t.Helper()
		data := fmt.Sprintf(`{"wake_id":%q%s}`, wake, extra)
		if err := store.AppendEventAt(ctx, "schedd", kind, &app, []byte(data), at); err != nil {
			t.Fatal(err)
		}
	}
	wake := "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	add("wake.boot_completed", wake, base.Add(380*time.Millisecond), `,"ready_in_ms":380`)
	add("wake.admitted", wake, base, "")
	add("wake.restore_breakdown", wake, base.Add(300*time.Millisecond), `,"restore_ms":290`)
	add("wake.admitted", "0193a1b2-0000-7e5f-8a9b-0c1d2e3f4a5b", base, "")

	got, err := WakeTimeline(ctx, store, wake)
	if err != nil {
		t.Fatal(err)
	}
	if got.WakeID != wake || got.Subject != app || got.Truncated || len(got.Events) != 3 {
		t.Fatalf("timeline = %+v", got)
	}
	var kinds []string
	for _, e := range got.Events {
		kinds = append(kinds, e.Kind)
	}
	if fmt.Sprint(kinds) != "[wake.admitted wake.restore_breakdown wake.boot_completed]" {
		t.Fatalf("order = %v", kinds)
	}
	var data map[string]any
	if err := json.Unmarshal(got.Events[1].Data, &data); err != nil || data["restore_ms"] != float64(290) {
		t.Fatalf("payload = %s %v", got.Events[1].Data, err)
	}
}

func TestWakeTimelineBoundsAndValidation(t *testing.T) {
	for _, id := range []string{"", "short", "has space in it", "0193'; DROP TABLE events; --", string(make([]byte, 65))} {
		if _, err := WakeTimeline(context.Background(), state.NewMemStore(), id); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("wake_id %q: err = %v, want ErrInvalidArgument", id, err)
		}
	}

	rows := make([]state.Event, api.ObsWakeTimelineEventsMax+1)
	for i := range rows {
		rows[i] = state.Event{At: time.Unix(int64(i), 0), Kind: "wake.phase"}
	}
	got, err := WakeTimeline(context.Background(), fakeWakeReader(rows), "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
	if err != nil || !got.Truncated || len(got.Events) != api.ObsWakeTimelineEventsMax {
		t.Fatalf("truncated = %v, %d events, %v", got.Truncated, len(got.Events), err)
	}
}

type fakeWakeReader []state.Event

func (f fakeWakeReader) ListEventsByWakeID(_ context.Context, _ string, _ time.Time, limit int) ([]state.Event, error) {
	if limit < len(f) {
		return f[:limit], nil
	}
	return f, nil
}

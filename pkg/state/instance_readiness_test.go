package state

import (
	"context"
	"testing"
	"time"
)

func TestMemStoreLatestInstanceReadiness(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	first := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for _, event := range []struct {
		at     time.Time
		status string
	}{
		{at: first, status: "ready"},
		{at: first.Add(time.Second), status: "unready"},
	} {
		body := []byte(`{"instance_id":"instance-1","status":"` + event.status + `"}`)
		if err := store.AppendEventAt(ctx, "vmmd", "wake.sidecar_health", nil, body, event.at); err != nil {
			t.Fatalf("AppendEventAt(%s): %v", event.status, err)
		}
	}

	got, err := store.LatestInstanceReadiness(ctx, []string{"instance-1", "missing"})
	if err != nil {
		t.Fatalf("LatestInstanceReadiness: %v", err)
	}
	readiness, ok := got["instance-1"]
	if !ok || readiness.Ready || !readiness.At.Equal(first.Add(time.Second)) || readiness.EventID != 2 {
		t.Fatalf("latest readiness = %+v, present=%t; want latest unready event", readiness, ok)
	}
	if _, ok := got["missing"]; ok {
		t.Fatal("returned readiness for unknown instance")
	}
}

package state

import (
	"context"
	"testing"
	"time"
)

func TestMemStoreListFreshCustomMetrics(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, p := range []struct {
		app, name string
		age       time.Duration
	}{
		{"app-b", "queue", time.Minute},
		{"app-a", "orders", 2 * time.Minute},
		{"app-a", "aaa", time.Minute},
		{"app-c", "stale", time.Hour},
	} {
		if err := m.PutCustomMetric(ctx, p.app, p.name, 1, now.Add(-p.age), 5); err != nil {
			t.Fatal(err)
		}
	}
	since := now.Add(-5 * time.Minute)
	got, err := m.ListFreshCustomMetrics(ctx, since, 10)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range got {
		keys = append(keys, s.AppID+"/"+s.Name)
	}
	if want := "app-a/aaa app-a/orders app-b/queue"; joinSpace(keys) != want {
		t.Fatalf("fresh samples = %q, want %q (stale omitted, ordered by app then name)", joinSpace(keys), want)
	}
	capped, err := m.ListFreshCustomMetrics(ctx, since, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 {
		t.Fatalf("cap = %d samples, want 2", len(capped))
	}
}

func joinSpace(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}

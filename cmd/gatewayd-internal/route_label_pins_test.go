package main

// adr: 20261028110044

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type fakeRouteLabelPinStore struct {
	labels []string
	err    error
	reads  int
}

func (s *fakeRouteLabelPinStore) RouteLabelPins(context.Context, string) ([]string, error) {
	s.reads++
	return s.labels, s.err
}

// Pins are read once per TTL; a failed read keeps the last known pins and
// retries after the short interval (amendment 4).
func TestRouteLabelPinsCacheAndFailure(t *testing.T) {
	store := &fakeRouteLabelPinStore{labels: []string{"POST /checkout"}}
	now := time.Unix(1_800_000_000, 0)
	pins := newRouteLabelPins(store, nil)
	pins.now = func() time.Time { return now }
	app := gateway.App{ID: "app"}
	if !pins.RouteLabelPinned(context.Background(), app, "POST /checkout") || pins.RouteLabelPinned(context.Background(), app, "GET /") {
		t.Fatal("pins do not match the store")
	}
	pins.RouteLabelPinned(context.Background(), app, "POST /checkout")
	if store.reads != 1 {
		t.Fatalf("reads within the TTL = %d, want 1", store.reads)
	}
	now = now.Add(2 * time.Minute)
	store.err = errors.New("database unavailable")
	if !pins.RouteLabelPinned(context.Background(), app, "POST /checkout") || store.reads != 2 {
		t.Fatalf("a failed refresh dropped the last pins (reads %d)", store.reads)
	}
	now = now.Add(5 * time.Second)
	pins.RouteLabelPinned(context.Background(), app, "POST /checkout")
	if store.reads != 2 {
		t.Fatalf("retried before the retry interval: reads %d", store.reads)
	}
	now = now.Add(10 * time.Second)
	store.err, store.labels = nil, nil
	if pins.RouteLabelPinned(context.Background(), app, "POST /checkout") || store.reads != 3 {
		t.Fatalf("an emptied selection still pinned (reads %d)", store.reads)
	}
	if (*routeLabelPins)(nil).RouteLabelPinned(context.Background(), app, "POST /checkout") {
		t.Fatal("a nil cache pinned a label")
	}
}

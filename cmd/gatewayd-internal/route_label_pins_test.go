package main

// adr: 20261028110044

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type fakeRouteLabelPinStore struct {
	mu     sync.Mutex
	labels []string
	err    error
	reads  int
}

func (s *fakeRouteLabelPinStore) RouteLabelPins(context.Context, string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return s.labels, s.err
}

func (s *fakeRouteLabelPinStore) set(labels []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labels, s.err = labels, err
}

func (s *fakeRouteLabelPinStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// A request never reads the store: the first lookup and each expiry start one
// background read, and lookups keep the last known pins meanwhile; a failed
// read keeps them and retries after the short interval (amendments 4 and 5).
func TestRouteLabelPinsRefreshInBackground(t *testing.T) {
	store := &fakeRouteLabelPinStore{labels: []string{"POST /checkout"}}
	now := time.Unix(1_800_000_000, 0)
	pins := newRouteLabelPins(store, nil)
	pins.now = func() time.Time { return now }
	app := gateway.App{ID: "app"}
	pinned := func(label string) bool { return pins.RouteLabelPinned(context.Background(), app, label) }

	if pinned("POST /checkout") {
		t.Fatal("pinned before the first read finished")
	}
	pins.reads.Wait()
	if !pinned("POST /checkout") || pinned("GET /") || store.count() != 1 {
		t.Fatalf("pins after the first read: reads %d", store.count())
	}

	now = now.Add(2 * time.Minute)
	store.set(nil, errors.New("database unavailable"))
	if !pinned("POST /checkout") {
		t.Fatal("an expired entry stopped serving its pins before the refresh")
	}
	pins.reads.Wait()
	if !pinned("POST /checkout") || store.count() != 2 {
		t.Fatalf("a failed refresh dropped the last pins (reads %d)", store.count())
	}
	now = now.Add(5 * time.Second)
	pinned("POST /checkout")
	pins.reads.Wait()
	if store.count() != 2 {
		t.Fatalf("retried before the retry interval: reads %d", store.count())
	}

	now = now.Add(10 * time.Second)
	store.set(nil, nil)
	pinned("POST /checkout")
	pins.reads.Wait()
	if pinned("POST /checkout") || store.count() != 3 {
		t.Fatalf("an emptied selection still pinned (reads %d)", store.count())
	}
	if (*routeLabelPins)(nil).RouteLabelPinned(context.Background(), app, "POST /checkout") {
		t.Fatal("a nil cache pinned a label")
	}
}

// Concurrent lookups of an expired app start a single read.
func TestRouteLabelPinsSingleRefresh(t *testing.T) {
	store := &fakeRouteLabelPinStore{labels: []string{"GET /items"}}
	pins := newRouteLabelPins(store, nil)
	app := gateway.App{ID: "app"}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { pins.RouteLabelPinned(context.Background(), app, "GET /items") })
	}
	wg.Wait()
	pins.reads.Wait()
	if store.count() != 1 {
		t.Fatalf("concurrent lookups read the store %d times, want once", store.count())
	}
}

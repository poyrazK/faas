package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type purgeReplayFixture struct {
	rows    []state.EnvironmentCachePurgeChange
	last    int64
	failAck bool
	acks    int
}

func (f *purgeReplayFixture) LatestEnvironmentCachePurgeID(context.Context) (int64, error) {
	if len(f.rows) == 0 {
		return 0, nil
	}
	return f.rows[len(f.rows)-1].ID, nil
}
func (f *purgeReplayFixture) ListEnvironmentCachePurgesAfter(_ context.Context, after int64) ([]state.EnvironmentCachePurgeChange, error) {
	out := []state.EnvironmentCachePurgeChange{}
	for _, p := range f.rows {
		if p.ID > after {
			out = append(out, p)
		}
		if len(out) == 100 {
			break
		}
	}
	return out, nil
}
func (f *purgeReplayFixture) RecordEnvironmentCachePurgeWatermark(_ context.Context, _, _ string, last int64) error {
	if f.failAck {
		return errors.New("database unavailable")
	}
	f.last = last
	f.acks++
	return nil
}
func TestEnvironmentPurgeReplayStartupRestartAndLostAck(t *testing.T) {
	f := &purgeReplayFixture{}
	for i := int64(1); i <= 101; i++ {
		f.rows = append(f.rows, state.EnvironmentCachePurgeChange{ID: i, AppID: "app", EnvironmentID: "env", Path: "/item"})
	}
	cache := gateway.NewResponseCache()
	var cursor int64
	if err := reconcileEnvironmentCachePurges(t.Context(), f, cache, "node", "session", &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 100 || f.acks != 0 {
		t.Fatal("partial startup replay acknowledged")
	}
	if _, ready := cache.EnvironmentPurgeToken("app", "env", "/item"); ready {
		t.Fatal("partial startup enabled cache")
	}
	f.failAck = true
	if err := reconcileEnvironmentCachePurges(t.Context(), f, cache, "node", "session", &cursor); err == nil {
		t.Fatal("failed ack hidden")
	}
	token, ready := cache.EnvironmentPurgeToken("app", "env", "/item")
	if !ready || token != "0/101" {
		t.Fatalf("namespace=%s ready=%v", token, ready)
	}
	f.failAck = false
	if err := reconcileEnvironmentCachePurges(t.Context(), f, cache, "node", "session", &cursor); err != nil {
		t.Fatal(err)
	}
	if f.last != 101 {
		t.Fatal("lost ack did not recover")
	}
	// New processes replay from zero, never trusting the old process's checkpoint.
	restarted := gateway.NewResponseCache()
	cursor = 0
	for i := 0; i < 2; i++ {
		if err := reconcileEnvironmentCachePurges(t.Context(), f, restarted, "node", "next-session", &cursor); err != nil {
			t.Fatal(err)
		}
	}
	restored, _ := restarted.EnvironmentPurgeToken("app", "env", "/item")
	if restored != token {
		t.Fatal("restart exposed older shared namespace")
	}
}
func TestEnvironmentPurgeReplayRejectsInvalidCommand(t *testing.T) {
	f := &purgeReplayFixture{rows: []state.EnvironmentCachePurgeChange{{ID: 1, AppID: "app", EnvironmentID: "env", Path: "/*"}}}
	cache := gateway.NewResponseCache()
	var cursor int64
	if err := reconcileEnvironmentCachePurges(t.Context(), f, cache, "node", "session", &cursor); err == nil || f.acks != 0 || cursor != 0 {
		t.Fatal("invalid command acknowledged")
	}
}

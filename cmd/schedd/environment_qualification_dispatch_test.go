package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationDispatchPageResult struct {
	page sched.EnvironmentQualificationGraphDispatchPage
	err  error
}

type qualificationDispatchEngineFake struct {
	nodes   []string
	workers []string
	cursors []string
	limits  []int
	results []qualificationDispatchPageResult
}

func (f *qualificationDispatchEngineFake) DispatchEnvironmentWorkloadQualificationGraphsWithHTTPHealth(_ context.Context,
	nodeID, workerID, after string, limit int) (sched.EnvironmentQualificationGraphDispatchPage, error) {
	f.nodes = append(f.nodes, nodeID)
	f.workers = append(f.workers, workerID)
	f.cursors = append(f.cursors, after)
	f.limits = append(f.limits, limit)
	result := f.results[0]
	f.results = f.results[1:]
	return result.page, result.err
}

func TestEnvironmentQualificationDispatchCursorAdvancesAcrossFailures(t *testing.T) {
	ctx := t.Context()
	engine := &qualificationDispatchEngineFake{results: []qualificationDispatchPageResult{
		{page: sched.EnvironmentQualificationGraphDispatchPage{Examined: 100, Claimed: 2, Executed: 1, NextCursor: "first-page-last-id"}, err: errors.New("one graph failed")},
		{page: sched.EnvironmentQualificationGraphDispatchPage{Examined: 100, Claimed: 3, Executed: 3, NextCursor: "second-page-last-id"}},
		{page: sched.EnvironmentQualificationGraphDispatchPage{}},
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cursor := &environmentQualificationDispatchCursor{}
	for range 3 {
		cursor.dispatchPage(ctx, engine, "9387ca52-9fe6-4920-866e-a53018ef8839", log)
	}
	want := []string{"", "first-page-last-id", "second-page-last-id"}
	for i := range want {
		if engine.cursors[i] != want[i] || engine.limits[i] != api.EnvironmentGitOpsQualificationDispatchBatchMax {
			t.Fatalf("dispatch call %d cursor/limit = %q/%d, want %q/%d", i, engine.cursors[i], engine.limits[i], want[i], api.EnvironmentGitOpsQualificationDispatchBatchMax)
		}
		if engine.workers[i] != "schedd-environment-qualification/9387ca52-9fe6-4920-866e-a53018ef8839" {
			t.Fatalf("dispatch worker identity = %q", engine.workers[i])
		}
	}
	if cursor.after != "" {
		t.Fatalf("completed dispatch scan retained cursor %q", cursor.after)
	}
}

func TestEnvironmentQualificationDispatchCursorResetsWhenStalled(t *testing.T) {
	engine := &qualificationDispatchEngineFake{results: []qualificationDispatchPageResult{{
		page: sched.EnvironmentQualificationGraphDispatchPage{NextCursor: "same-graph-id"},
	}}}
	cursor := &environmentQualificationDispatchCursor{after: "same-graph-id"}
	cursor.dispatchPage(t.Context(), engine, "9387ca52-9fe6-4920-866e-a53018ef8839", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if cursor.after != "" {
		t.Fatalf("stalled dispatch cursor = %q, want reset", cursor.after)
	}
}

func TestEnvironmentQualificationDispatchGateIsExplicit(t *testing.T) {
	for _, value := range []string{"", "0", "true", " yes", "1x"} {
		if environmentQualificationDispatchEnabled(value) {
			t.Errorf("dispatch gate unexpectedly accepted %q", value)
		}
	}
	if !environmentQualificationDispatchEnabled("1") || !environmentQualificationDispatchEnabled(" 1 ") {
		t.Fatal("dispatch gate did not accept the explicit enabled value")
	}
	flagsFor := func(value string) *platformflags.Set {
		return platformflags.NewSet(map[string]platformflags.Definition{
			platformflags.EnvironmentGitOpsExecution: environmentQualificationDispatchBootstrap(value)})
	}
	if environmentQualificationDispatchGate(flagsFor("1"), false) != nil {
		t.Fatal("dispatch started although admission ledger seeding failed")
	}
	if environmentQualificationDispatchGate(flagsFor(""), true)() || !environmentQualificationDispatchGate(flagsFor("1"), true)() {
		t.Fatal("dispatch gate did not follow the explicit boot fallback")
	}
	if flagsFor("1").Enabled(platformflags.EnvironmentGitOpsExecution, platformflags.Subject{AccountID: "9387ca52-9fe6-4920-866e-a53018ef8839"}) {
		t.Fatal("schedd's boot fallback admitted an account")
	}
	// A durable operator value decides from then on, in both directions.
	flags := flagsFor("1")
	open := environmentQualificationDispatchGate(flags, true)
	if err := flags.Apply(platformflags.ConfigKey(platformflags.EnvironmentGitOpsExecution), []byte(`{"enabled":false,"default":false}`), 1); err != nil || open() {
		t.Fatalf("operator kill switch did not stop dispatch: %v", err)
	}
	if err := flags.Apply(platformflags.ConfigKey(platformflags.EnvironmentGitOpsExecution), []byte(`{"enabled":true,"default":false}`), 2); err != nil || !open() {
		t.Fatalf("operator release did not start dispatch: %v", err)
	}
}

func TestEnvironmentQualificationDispatchSkipsPassWhileKillSwitchIsOff(t *testing.T) {
	// The fake has no page results, so any dispatch call would panic.
	engine := &qualificationDispatchEngineFake{}
	store := qualificationDispatchNodeStoreFake{node: state.ComputeNode{ID: "9387ca52-9fe6-4920-866e-a53018ef8839", Name: state.DefaultLocalNodeName}}
	checked := make(chan struct{})
	startEnvironmentQualificationDispatch(t.Context(), engine, store, "", func() bool {
		close(checked)
		return false
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	<-checked
	startEnvironmentQualificationDispatch(t.Context(), engine, store, "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

type qualificationDispatchNodeStoreFake struct {
	node state.ComputeNode
	err  error
}

func (f qualificationDispatchNodeStoreFake) ComputeNodeByName(_ context.Context, name string) (state.ComputeNode, error) {
	if name != state.DefaultLocalNodeName {
		return state.ComputeNode{}, state.ErrNotFound
	}
	return f.node, f.err
}

func TestEnvironmentQualificationDispatchNodeUsesConfiguredOwnerOrExactLocalNode(t *testing.T) {
	ctx := t.Context()
	store := qualificationDispatchNodeStoreFake{node: state.ComputeNode{ID: "9387ca52-9fe6-4920-866e-a53018ef8839", Name: state.DefaultLocalNodeName}}
	if got, err := environmentQualificationRecoveryNodeID(ctx, store, ""); err != nil || got != store.node.ID {
		t.Fatalf("default-local dispatch node = %q, %v", got, err)
	}
	if got, err := environmentQualificationRecoveryNodeID(ctx, store, "configured-owner"); err != nil || got != "configured-owner" {
		t.Fatalf("configured dispatch node = %q, %v", got, err)
	}
	if _, err := environmentQualificationRecoveryNodeID(ctx,
		qualificationDispatchNodeStoreFake{node: state.ComputeNode{ID: store.node.ID, Name: "unexpected"}}, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unexpected node identity was accepted: %v", err)
	}
}

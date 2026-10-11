package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type qualificationRecoveryPageResult struct {
	page sched.EnvironmentQualificationRecoveryPage
	err  error
}

type qualificationRecoveryEngineFake struct {
	cursors []string
	results []qualificationRecoveryPageResult
}

func (f *qualificationRecoveryEngineFake) RecoverEnvironmentQualificationExecutions(_ context.Context, nodeID, after string, limit int) (sched.EnvironmentQualificationRecoveryPage, error) {
	if nodeID == "" || limit != api.EnvironmentGitOpsQualificationRecoveryBatchMax {
		return sched.EnvironmentQualificationRecoveryPage{}, state.ErrInvalidArgument
	}
	f.cursors = append(f.cursors, after)
	result := f.results[0]
	f.results = f.results[1:]
	return result.page, result.err
}

type qualificationRecoveryNodeStoreFake struct {
	node state.ComputeNode
	err  error
}

func (f qualificationRecoveryNodeStoreFake) ComputeNodeByName(_ context.Context, name string) (state.ComputeNode, error) {
	if name != state.DefaultLocalNodeName {
		return state.ComputeNode{}, state.ErrNotFound
	}
	return f.node, f.err
}

func TestEnvironmentQualificationRecoveryUsesExactLocalNodeAndPagesPastFailures(t *testing.T) {
	ctx := t.Context()
	nodeID := "9387ca52-9fe6-4920-866e-a53018ef8839"
	nodeStore := qualificationRecoveryNodeStoreFake{node: state.ComputeNode{ID: nodeID, Name: state.DefaultLocalNodeName}}
	if got, err := environmentQualificationRecoveryNodeID(ctx, nodeStore, ""); err != nil || got != nodeID {
		t.Fatalf("legacy recovery node = %q, %v", got, err)
	}
	if got, err := environmentQualificationRecoveryNodeID(ctx, nodeStore, "configured-owner"); err != nil || got != "configured-owner" {
		t.Fatalf("configured recovery node = %q, %v", got, err)
	}
	if _, err := environmentQualificationRecoveryNodeID(ctx, qualificationRecoveryNodeStoreFake{node: state.ComputeNode{ID: nodeID, Name: "unexpected"}}, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unexpected default-local identity was accepted: %v", err)
	}

	rowFailure := errors.New("one native host is temporarily unavailable")
	engine := &qualificationRecoveryEngineFake{results: []qualificationRecoveryPageResult{
		{page: sched.EnvironmentQualificationRecoveryPage{Examined: 100, Retired: 99, NextCursor: "first-page-last-id"}, err: rowFailure},
		{page: sched.EnvironmentQualificationRecoveryPage{Examined: 100, Retired: 100, NextCursor: "second-page-last-id"}},
		{page: sched.EnvironmentQualificationRecoveryPage{}},
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cursor := &environmentQualificationRecoveryCursor{}
	cursor.recoverPage(ctx, engine, nodeID, log)
	cursor.recoverPage(ctx, engine, nodeID, log)
	cursor.recoverPage(ctx, engine, nodeID, log)
	want := []string{"", "first-page-last-id", "second-page-last-id"}
	if len(engine.cursors) != len(want) {
		t.Fatalf("recovery cursor calls = %v", engine.cursors)
	}
	for i := range want {
		if engine.cursors[i] != want[i] {
			t.Fatalf("recovery cursor[%d] = %q, want %q", i, engine.cursors[i], want[i])
		}
	}
	if cursor.after != "" {
		t.Fatalf("completed recovery scan retained cursor %q", cursor.after)
	}
}

func TestEnvironmentQualificationRecoveryResetsStalledCursor(t *testing.T) {
	engine := &qualificationRecoveryEngineFake{results: []qualificationRecoveryPageResult{{
		page: sched.EnvironmentQualificationRecoveryPage{NextCursor: "same-id"},
	}}}
	cursor := &environmentQualificationRecoveryCursor{after: "same-id"}
	cursor.recoverPage(t.Context(), engine, "9387ca52-9fe6-4920-866e-a53018ef8839", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if cursor.after != "" {
		t.Fatalf("stalled recovery cursor = %q, want reset", cursor.after)
	}
}

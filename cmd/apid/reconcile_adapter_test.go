package main

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMapReconcileErrorConflictIsClientVisible(t *testing.T) {
	mapped := mapReconcileError(errors.Join(errors.New("create workload"), state.ErrConflict))
	if mapped == nil {
		t.Fatal("mapReconcileError returned nil")
	}
	if mapped.Status != 409 || mapped.Code != "workload_slug_conflict" {
		t.Fatalf("conflict mapping = status %d code %q, want 409 workload_slug_conflict", mapped.Status, mapped.Code)
	}
}

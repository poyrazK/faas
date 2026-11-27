package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowOutboundUnavailable(t *testing.T) {
	minter := func(outbound.WorkflowIdentity, outbound.WorkflowOutboundRequest, []byte) (string, error) {
		return "t", nil
	}
	retired := time.Now()
	active := func(context.Context) (state.ClusterSigningKey, error) { return state.ClusterSigningKey{}, nil }
	failing := func(context.Context) (state.ClusterSigningKey, error) {
		return state.ClusterSigningKey{}, errors.New("permission denied")
	}
	retiredKey := func(context.Context) (state.ClusterSigningKey, error) {
		return state.ClusterSigningKey{RetiredAt: &retired}, nil
	}
	ctx := context.Background()
	if got := workflowOutboundUnavailable(ctx, false, nil, active); got == "" {
		t.Error("no minter must disable integration steps")
	}
	if got := workflowOutboundUnavailable(ctx, true, minter, failing); got != "" {
		t.Errorf("secretd minting must not read the cluster key, got %q", got)
	}
	if got := workflowOutboundUnavailable(ctx, false, minter, active); got != "" {
		t.Errorf("local minter with an active key must enable, got %q", got)
	}
	if got := workflowOutboundUnavailable(ctx, false, minter, failing); got == "" {
		t.Error("an unreadable key must disable integration steps")
	}
	if got := workflowOutboundUnavailable(ctx, false, minter, retiredKey); got == "" {
		t.Error("a retired key must disable integration steps")
	}
}

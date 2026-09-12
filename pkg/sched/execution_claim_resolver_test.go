package sched

// adr: 171 — claim resolution keeps tenant payload opaque while selecting a
// trusted plan, node, machine shape, and sanitized runtime snapshot.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExecutionClaimResolverBuildsPayloadFreeRestoreEnvelope(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "claim-resolver@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	request := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimeNode22,
		Source:  "export default async function main(input) { return input }",
		Limits: &api.ExecutionLimitRequest{
			TimeoutMS: 5000, MemoryMB: 128, CPUMillicores: 250, EphemeralDiskMB: 64, MaxOutputBytes: 1024,
		},
	}
	resolved, problem := request.Resolve(account.Plan)
	if problem != nil {
		t.Fatalf("Resolve: %v", problem)
	}
	admitted := time.Now().UTC()
	created, err := store.CreateExecution(ctx, state.CreateExecutionParams{
		AccountID: account.ID, Request: resolved, SourceBytes: len(request.Source), InputBytes: 0,
		AdmittedAt: admitted, DeadlineAt: admitted.Add(5 * time.Second),
		SealedPayload: []byte("caller-source-and-input-must-stay-opaque"), PayloadKID: "execution-kid",
	})
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	claim, err := store.ClaimExecution(ctx, "schedd-test", admitted, time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}

	artifacts := StaticExecutionRuntimeArtifacts{
		api.ExecutionRuntimeNode22: {
			Architecture:        archAMD64,
			KernelDigest:        strings.Repeat("a", 64),
			GuestExecutorDigest: strings.Repeat("b", 64),
			BaseImageDigest:     strings.Repeat("c", 64),
			KernelKey:           "kernel/1.10.0",
			BaseKey:             "base/runner-node22-amd64.ext4",
			LayerKey:            "execution/node22-amd64.ext4",
			FCVersion:           "1.10.0",
		},
	}
	identity := RuntimeSnapshotIdentity{
		Runtime:             api.ExecutionRuntimeNode22,
		Architecture:        archAMD64,
		KernelDigest:        strings.Repeat("a", 64),
		GuestExecutorDigest: strings.Repeat("b", 64),
		BaseImageDigest:     strings.Repeat("c", 64),
		MemoryMB:            128,
		EphemeralDiskMB:     64,
		FormatVersion:       CurrentRuntimeSnapshotFormatVersion,
	}
	index := NewMemoryRuntimeSnapshotIndex()
	if err := index.Publish(RuntimeSnapshot{
		Identity: identity, StorageKey: "execution-snapshots/node22-amd64/mem", SnapshotDigest: strings.Repeat("d", 64),
		MemBytes: 128 << 20, VMStateBytes: 4096, Sanitized: true, PayloadFree: true,
		State: RuntimeSnapshotReady, CreatedAt: admitted,
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	catalog := NewRuntimeSnapshotCatalog(index, runtimeSnapshotVerifierFunc(func(context.Context, RuntimeSnapshot) error { return nil }))
	resolver := NewExecutionClaimResolver(store, catalog, artifacts, "node-a")
	got, err := resolver.ResolveExecutionClaim(ctx, claim)
	if err != nil {
		t.Fatalf("ResolveExecutionClaim: %v", err)
	}
	if got.ID != created.ID || got.AccountID != account.ID || got.NodeID != "node-a" || got.Plan != api.PlanPro {
		t.Fatalf("identity = %+v", got)
	}
	if got.KernelKey != "kernel/1.10.0" || got.BaseKey != "base/runner-node22-amd64.ext4" || got.LayerKey != "execution/node22-amd64.ext4" {
		t.Fatalf("artifact keys = %+v", got)
	}
	if got.VcpuCount != 2 || got.MemSizeMiB != 128 || got.CPUMillicores != 250 {
		t.Fatalf("machine shape = %+v", got)
	}
	if !got.Snapshot.Networkless || got.Snapshot.StorageKey != "execution-snapshots/node22-amd64/mem" ||
		got.Snapshot.VMStateStorageKey != "execution-snapshots/node22-amd64/vmstate" || got.Snapshot.FCVersion != "1.10.0" {
		t.Fatalf("snapshot = %+v", got.Snapshot)
	}
	if string(claim.SealedPayload) != "caller-source-and-input-must-stay-opaque" {
		t.Fatal("claim payload was mutated while resolving machine metadata")
	}
}

func TestExecutionClaimResolverMissingSnapshotColdBootsSameIdentity(t *testing.T) {
	ctx := context.Background()
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 1, 5000)
	claim, err := store.ClaimExecution(ctx, "schedd-test", time.Now().UTC(), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	resolver := NewExecutionClaimResolver(store, NewRuntimeSnapshotCatalog(NewMemoryRuntimeSnapshotIndex(), nil), StaticExecutionRuntimeArtifacts{
		api.ExecutionRuntimeNode22: {
			Architecture: archAMD64, KernelDigest: strings.Repeat("a", 64), GuestExecutorDigest: strings.Repeat("b", 64), BaseImageDigest: strings.Repeat("c", 64),
			KernelKey: "kernel/1.10.0", BaseKey: "base/runner-node22-amd64.ext4", LayerKey: "execution/node22-amd64.ext4", FCVersion: "1.10.0",
		},
	}, "node-a")
	got, err := resolver.ResolveExecutionClaim(ctx, claim)
	if err != nil {
		t.Fatalf("ResolveExecutionClaim: %v", err)
	}
	if got.Snapshot != (SnapshotRef{}) {
		t.Fatalf("snapshot = %+v, want cold boot", got.Snapshot)
	}
	if got.ID != executions[0].ID || got.AccountID != account.ID {
		t.Fatalf("identity = %+v", got)
	}
}

func TestExecutionClaimResolverRequiresNodeAndTrustedArtifacts(t *testing.T) {
	store, _, _, _ := newExecutionCoordinatorFixture(t, 1, 5000)
	claim, err := store.ClaimExecution(context.Background(), "schedd-test", time.Now().UTC(), time.Second)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	if _, err := NewExecutionClaimResolver(store, nil, nil, "node-a").ResolveExecutionClaim(context.Background(), claim); !errors.Is(err, ErrExecutionClaimResolverUnwired) {
		t.Fatalf("missing dependencies error = %v, want ErrExecutionClaimResolverUnwired", err)
	}
	if _, err := NewExecutionClaimResolver(store, nil, StaticExecutionRuntimeArtifacts{}, "").ResolveExecutionClaim(context.Background(), claim); !errors.Is(err, ErrExecutionClaimResolverUnwired) {
		t.Fatalf("missing node error = %v, want ErrExecutionClaimResolverUnwired", err)
	}
}

package sched

// adr:383 — profiles never share a snapshot or silently select a standard image.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExecutionProfileSeparatesSnapshotIdentity(t *testing.T) {
	identity := validRuntimeSnapshotIdentity()
	identity.Runtime = api.ExecutionRuntimePython313
	standard, err := identity.Key()
	if err != nil {
		t.Fatal(err)
	}
	identity.Profile = api.ExecutionProfilePythonDataV1
	data, err := identity.Key()
	if err != nil {
		t.Fatal(err)
	}
	if standard == data {
		t.Fatal("dependency profile shares standard snapshot key")
	}
	artifacts := StaticExecutionRuntimeArtifacts{api.ExecutionRuntimePython313: {Architecture: identity.Architecture, KernelDigest: identity.KernelDigest, GuestExecutorDigest: identity.GuestExecutorDigest, BaseImageDigest: identity.BaseImageDigest}}
	if _, err := artifacts.ResolveExecutionArtifacts(context.Background(), api.ExecutionRuntimePython313, api.ExecutionSnapshotShape{Runtime: api.ExecutionRuntimePython313, Profile: api.ExecutionProfilePythonDataV1, MemoryMB: identity.MemoryMB, EphemeralDiskMB: identity.EphemeralDiskMB}); err == nil {
		t.Fatal("standard artifacts accepted for data profile")
	}
}

func TestExecutionProfilePinsImageAcrossRestoreRetry(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "data-profile@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	request, problem := (api.CreateExecutionRequest{Runtime: api.ExecutionRuntimePython313, Profile: api.ExecutionProfilePythonDataV1, Source: "def main(input, context): return input", Limits: &api.ExecutionLimitRequest{TimeoutMS: 30000}}).Resolve(api.PlanPro)
	if problem != nil {
		t.Fatal(problem)
	}
	base := time.Now().UTC()
	created, err := store.CreateExecution(ctx, state.CreateExecutionParams{AccountID: account.ID, Request: request, SourceBytes: len(request.Source), AdmittedAt: base, DeadlineAt: base.Add(30 * time.Second), SealedPayload: []byte("opaque"), PayloadKID: "kid"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimExecution(ctx, "first", base, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := StaticExecutionRuntimeArtifacts{api.ExecutionRuntimePython313: {
		Profile: request.Profile, Architecture: archAMD64, KernelDigest: strings.Repeat("a", 64), GuestExecutorDigest: strings.Repeat("b", 64), BaseImageDigest: strings.Repeat("c", 64),
		KernelKey: "kernel/linux", BaseKey: "base/python-data-v1.ext4", LayerKey: "execution/python-data-v1.ext4", FCVersion: "1.10.0",
	}}
	resolver := NewExecutionClaimResolver(store, NewRuntimeSnapshotCatalog(NewMemoryRuntimeSnapshotIndex(), nil), artifacts, "node-a")
	if _, err := resolver.ResolveExecutionClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	row, err := store.ExecutionByID(ctx, account.ID, created.ID)
	if err != nil || row.RuntimeImageDigest != "sha256:"+strings.Repeat("c", 64) {
		t.Fatalf("unpinned restore: %+v, %v", row, err)
	}
	if swept, err := store.SweepExecutions(ctx, base.Add(2*time.Second), 10); err != nil || swept.RequeuedRestores != 1 {
		t.Fatalf("sweep=%+v, %v", swept, err)
	}
	retry, err := store.ClaimExecution(ctx, "retry", base.Add(2*time.Second), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveExecutionClaim(ctx, retry); err != nil {
		t.Fatal(err)
	}
	changed := artifacts[request.Runtime]
	changed.BaseImageDigest = strings.Repeat("d", 64)
	artifacts[request.Runtime] = changed
	if _, err := resolver.ResolveExecutionClaim(ctx, retry); !errors.Is(err, ErrExecutionRuntimeArtifactsUnavailable) {
		t.Fatalf("changed retry image accepted: %v", err)
	}
}

func TestExecutionProfileMustMatchSealedRequestBeforeGuestDispatch(t *testing.T) {
	for _, sealedProfile := range []api.ExecutionProfile{api.ExecutionProfileStandard, api.ExecutionProfilePythonDataV1} {
		t.Run(string(sealedProfile), func(t *testing.T) {
			transport := &recordingExecutionTransport{result: executionproto.Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null")}}
			backend := NewVmmdExecutionBackendWithBundle(func(context.Context, ExecutionRestoreRequest) (VmmdExecutionTransport, error) { return transport, nil }, func(context.Context, []byte, string) (executionpayload.DecodedPayload, error) {
				return executionpayload.DecodedPayload{Profile: sealedProfile, Source: "def main(input, context): return input", Input: json.RawMessage("null")}, nil
			})
			session, err := backend.Restore(context.Background(), ExecutionRestoreRequest{ID: "profile", Profile: api.ExecutionProfilePythonDataV1, Runtime: api.ExecutionRuntimePython313, NetworkMode: api.ExecutionNetworkNone, Limits: api.ResolvedExecutionLimits{TimeoutMS: 1000, MaxOutputBytes: 4096}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.Execute(context.Background(), ExecutionPayload{Sealed: []byte("opaque")})
			if sealedProfile == api.ExecutionProfileStandard {
				if err == nil || transport.request.ExecutionID != "" {
					t.Fatalf("mismatched sealed profile dispatched: %+v, %v", transport.request, err)
				}
			} else if err != nil || transport.request.Profile != sealedProfile || transport.request.Version != executionproto.ProfileVersion {
				t.Fatalf("profile lost before guest: %+v, %v", transport.request, err)
			}
		})
	}
}

func TestExecutionProfileSnapshotSurvivesDurableCatalog(t *testing.T) {
	entry := validRuntimeSnapshot()
	entry.Identity.Runtime = api.ExecutionRuntimePython313
	entry.Identity.Profile = api.ExecutionProfilePythonDataV1
	record := recordForState(entry)
	store := state.NewMemStore()
	if _, err := store.PublishRuntimeSnapshot(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got, err := NewStateRuntimeSnapshotIndex(store).LookupRuntimeSnapshot(context.Background(), record.CatalogKey)
	if err != nil || !got.Identity.equal(entry.Identity) {
		t.Fatalf("profile catalog=%+v, %v", got, err)
	}
}

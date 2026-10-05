package sched_test

// adr: 633
import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRegionalRuntimeRetirementRecoveryLostReplyAfterCollection(t *testing.T) {
	f := newRetirementCell(t, false)
	source := runtimequota.LaunchSource{Permit: f.p, InstanceKey: strings.ReplaceAll(f.p.InstanceID, "-", "")}
	if err := f.store.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: f.p}, source); err != nil {
		t.Fatal(err)
	}
	first := uuid.NewString()
	if _, err := f.store.BeginRegionalRuntimeRetirement(t.Context(), f.p, first); err != nil {
		t.Fatal(err)
	}
	v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store, wantInstance: source.InstanceKey}
	var lost atomic.Bool
	interceptor := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil && !lost.Swap(true) {
			return nil, status.Error(codes.Unavailable, "lost native reply")
		}
		return resp, err
	}
	router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, interceptor)
	owner, err := sched.NewRegionalRuntimeRetirement(f.store, runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}, router)
	if err != nil {
		t.Fatal(err)
	}
	after, count, err := owner.RecoverPage(t.Context(), state.RegionalRuntimeRetirementCursor{})
	if err == nil || count != 0 || after.PermitID != f.p.ID {
		t.Fatal("lost reply released or pinned recovery cursor", after, count, err)
	}
	r, err := f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
	if err != nil || r.ReleasedAt != nil || r.FenceID != first {
		t.Fatal("uncertainty changed original hold", r, err)
	}
	if err := f.store.DeleteApp(t.Context(), f.p.Grant.Binding.RegionalAppID); err != nil {
		t.Fatal(err)
	}
	// End of traversal resets even when its preceding item failed.
	after, count, err = owner.RecoverPage(t.Context(), after)
	if err != nil || count != 0 || after != (state.RegionalRuntimeRetirementCursor{}) {
		t.Fatal("cursor did not reset", after, count, err)
	}
	_, count, err = owner.RecoverPage(t.Context(), after)
	if err != nil || count != 1 || v.calls.Load() != 2 {
		t.Fatal("collected original source was not recovered", count, err)
	}
	r, err = f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
	if err != nil || r.ReleasedAt == nil || r.StopEnvelope == nil || r.FenceID != first {
		t.Fatal("exact stop not retained", r, err)
	}
	verified, err := f.authority.Cells.VerifyCapacityStop(*r.StopEnvelope)
	if err != nil || verified.Permit != f.p || verified.FenceID != first {
		t.Fatal("recovery signed changed original attempt", err)
	}
}

func TestRegionalRuntimeRetirementRecoveryCancelJoinsNativeAttempt(t *testing.T) {
	f := newRetirementCell(t, false)
	if err := f.store.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: f.p}, runtimequota.LaunchSource{Permit: f.p, InstanceKey: f.p.InstanceID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.BeginRegionalRuntimeRetirement(t.Context(), f.p, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	joined := make(chan struct{})
	interceptor := func(ctx context.Context, _ any, _ *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
		close(entered)
		<-ctx.Done()
		close(joined)
		return nil, ctx.Err()
	}
	v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store}
	router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, interceptor)
	owner, err := sched.NewRegionalRuntimeRetirement(f.store, runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}, router)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- owner.RunRecovery(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("recovery did not join cancelled client attempt", err)
	}
	<-joined // The server simulation observes cancellation; no absence claim.
	r, err := f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
	if err != nil || r.ReleasedAt != nil || r.StopEnvelope != nil || v.calls.Load() != 0 {
		t.Fatal("cancelled recovery released uncertain native hold", r, err)
	}
}

func TestRegionalRuntimeRetirementRecoveryUnreachablePageDoesNotStarveLaterOriginal(t *testing.T) {
	f := newRetirementCell(t, false)
	// Earlier original sources belong to unavailable nodes. No current row or
	// guessed replacement destination can make them safe to release.
	for i := 1; i <= api.RegionalRuntimeRetirementBatch; i++ {
		p := f.p
		p.ID = fmt.Sprintf("00000000-0000-0000-0000-%012x", i)
		p.InstanceID, p.RunID, p.NodeID = uuid.NewString(), uuid.NewString(), uuid.NewString()
		if err := f.store.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: p}, runtimequota.LaunchSource{Permit: p, InstanceKey: p.InstanceID}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.BeginRegionalRuntimeRetirement(t.Context(), p, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: f.p}, runtimequota.LaunchSource{Permit: f.p, InstanceKey: f.p.InstanceID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.BeginRegionalRuntimeRetirement(t.Context(), f.p, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	v := &retirementWireVMM{fakeVMM: &fakeVMM{}, store: f.store}
	router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, nil)
	owner, err := sched.NewRegionalRuntimeRetirement(f.store, runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}, router)
	if err != nil {
		t.Fatal(err)
	}
	after, completed, err := owner.RecoverPage(t.Context(), state.RegionalRuntimeRetirementCursor{})
	if err == nil || completed != 0 || after.PermitID != fmt.Sprintf("00000000-0000-0000-0000-%012x", api.RegionalRuntimeRetirementBatch) {
		t.Fatal("unreachable attempts did not advance bounded page", after, completed, err)
	}
	_, completed, err = owner.RecoverPage(t.Context(), after)
	if err != nil || completed != 1 || v.calls.Load() != 1 {
		t.Fatal("unavailable page starved later original", completed, err)
	}
	usage, err := f.store.RegionalRuntimeCapacityUsage(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.Grant.AllocationID)
	if err != nil || usage.IssuedHeld != int64(api.RegionalRuntimeRetirementBatch) {
		t.Fatal("unreachable holds were released", usage, err)
	}
}

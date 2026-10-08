package sched_test

// adr: 777
import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Portable RPC simulation. It does not stand in for native/KVM acceptance.
type inventoryWireVMM struct {
	*fakeVMM
	host      runtimequota.HostEpoch
	inventory fcvm.NativeRuntimeInventory
	mode      string
	calls     atomic.Int64
	consumed  atomic.Bool
	entered   chan struct{}
	joined    chan struct{}
}

func (v *inventoryWireVMM) RegionalRuntimeHostEpoch() (runtimequota.HostEpoch, error) {
	host := v.host
	if v.mode == "epoch-changed" && v.consumed.Load() {
		host.HostEpochID = uuid.NewString()
	}
	return host, nil
}

func (v *inventoryWireVMM) WithNativeRuntimeInventory(ctx context.Context, consume func(context.Context, fcvm.NativeRuntimeInventory) error) error {
	v.calls.Add(1)
	if v.entered != nil {
		close(v.entered)
	}
	if v.mode == "cancelled" {
		<-ctx.Done()
		close(v.joined)
		return ctx.Err()
	}
	if err := consume(ctx, v.inventory); err != nil {
		return err
	}
	v.consumed.Store(true)
	if v.mode == "post-consumer-error" {
		return errors.New("native final recheck changed")
	}
	if v.mode == "repeated-consumer" {
		return consume(ctx, v.inventory)
	}
	return nil
}

func inventoryWireFixture(f retirementCellFixture) *inventoryWireVMM {
	return &inventoryWireVMM{fakeVMM: &fakeVMM{}, host: runtimequota.HostEpoch{NodeID: f.p.NodeID, HostEpochID: f.p.HostEpochID}, inventory: fcvm.NativeRuntimeInventory{KernelBootID: uuid.NewString()}}
}

func TestNativeInventoryAuthenticatedWireAndCompletion(t *testing.T) {
	for _, mode := range []string{"complete", "multi-chunk", "foreign-peer", "wrong-client", "plaintext", "post-consumer-error", "epoch-changed", "repeated-consumer"} {
		t.Run(mode, func(t *testing.T) {
			f := newRetirementCell(t, false)
			v := inventoryWireFixture(f)
			v.mode = mode
			if mode == "multi-chunk" {
				v.inventory.Unresolved = []string{strings.Repeat("x", api.NativeRuntimeInventoryChunkMaxBytes*2)}
			}
			client, server := "schedd.faas", "native-a"
			if mode == "foreign-peer" {
				server = "native-b"
			}
			if mode == "wrong-client" {
				client = "apid.faas"
			}
			router, _ := newRetirementWire(t, f, v, client, server, mode == "plaintext", nil)
			sample, err := router.NativeRuntimeInventory(t.Context(), f.p.NodeID)
			valid := mode == "complete" || mode == "multi-chunk"
			if (err == nil) != valid {
				t.Fatal("incorrect acceptance", err)
			}
			if valid && (sample.Host != v.host || sample.Challenge == "" || sample.Inventory.KernelBootID != v.inventory.KernelBootID) {
				t.Fatal("lost request or host binding")
			}
			if !valid && sample.Challenge != "" {
				t.Fatal("partial observation escaped")
			}
			if (mode == "wrong-client" || mode == "plaintext") && v.calls.Load() != 0 {
				t.Fatal("unauthenticated observation reached native provider")
			}
		})
	}
}

func TestNativeInventoryRequestValidationPrecedesProvider(t *testing.T) {
	f := newRetirementCell(t, false)
	v := inventoryWireFixture(f)
	_, client := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, nil)
	for _, req := range []*vmmdpb.ObserveNativeRuntimeInventoryRequest{
		{NodeId: uuid.NewString(), Challenge: uuid.NewString()},
		{NodeId: f.p.NodeID, Challenge: uuid.Nil.String()},
		{NodeId: f.p.NodeID, Challenge: "invalid"},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		stream, err := client.ObserveNativeRuntimeInventory(ctx, req)
		if err == nil {
			_, err = stream.Recv()
		}
		cancel()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatal("malformed ownership accepted", err)
		}
	}
	stream, err := client.ObserveNativeRuntimeInventory(t.Context(), &vmmdpb.ObserveNativeRuntimeInventoryRequest{NodeId: f.p.NodeID, Challenge: uuid.NewString()})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.InvalidArgument || v.calls.Load() != 0 {
		t.Fatal("unbounded observation accepted", err)
	}
}

func TestNativeInventoryCancellationJoinsWithoutSample(t *testing.T) {
	f := newRetirementCell(t, false)
	v := inventoryWireFixture(f)
	v.mode = "cancelled"
	v.entered = make(chan struct{})
	v.joined = make(chan struct{})
	router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		sample, err := router.NativeRuntimeInventory(ctx, f.p.NodeID)
		if sample.Challenge != "" {
			done <- errors.New("cancelled partial sample returned")
			return
		}
		done <- err
	}()
	<-v.entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled observation completed")
	}
	<-v.joined
}

func TestNativeInventoryWiredIntoSchedulerRecovery(t *testing.T) {
	f := newRetirementCell(t, false)
	v := inventoryWireFixture(f)
	v.entered = make(chan struct{})
	source := runtimequota.LaunchSource{Permit: f.p, InstanceKey: strings.ReplaceAll(f.p.InstanceID, "-", "")}
	resources := state.RegionalRuntimeResources{RAMMB: 512, VCPU: 4, CPUMillicores: 3000}
	if err := f.store.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: f.p}, source, resources); err != nil {
		t.Fatal(err)
	}
	router, _ := newRetirementWire(t, f, v, "schedd.faas", "native-a", false, nil)
	owner, err := sched.NewRegionalRuntimeRetirement(f.store, runtimequota.AuthorityKeys{Signer: f.signer, Trust: f.authority.Cells}, router)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := sched.NewEngine(t.Context(), f.store, sched.NewNodeLedger(), router, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithRegionalRuntimeRetirement(owner)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- engine.RunRegionalRuntimeFailureRecovery(ctx, nil) }()
	<-v.entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("recovery did not join", err)
	}
	retained, err := f.store.RegionalRuntimeCapacity(t.Context(), f.p.Grant.Binding.RegionalAppID, f.p.ID)
	if err != nil || retained.FenceID != "" || retained.ReleasedAt != nil || retained.Stop != nil {
		t.Fatal("observation fenced or released original hold", err)
	}
}

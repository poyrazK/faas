// adr: 1529
package sched

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/migrationadmissionwire"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// This adapter exercises the durable receiver protocol around the existing VM
// fake. It never treats an ordinary Destroy result as a retirement receipt.
type migrationAdmissionTestVMM struct {
	RoutedVMM
	store      *state.MemStore
	mu         sync.Mutex
	proofs     map[string]string
	cleanupErr error
}

type migrationReplyClient struct {
	vmmdpb.VmmdClient
	reply    *vmmdpb.AdoptMigratedInstanceResponse
	destroys int
}

func (c *migrationReplyClient) Ping(context.Context, *vmmdpb.PingRequest, ...grpc.CallOption) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{SupportsSecretAliases: true, SupportsImageHealthcheck: true, SupportsImageHealthcheckMonitoring: true}, nil
}
func (c *migrationReplyClient) AdoptMigratedInstance(context.Context, *vmmdpb.AdoptMigratedInstanceRequest, ...grpc.CallOption) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	return c.reply, nil
}
func (c *migrationReplyClient) Destroy(context.Context, *vmmdpb.DestroyRequest, ...grpc.CallOption) (*vmmdpb.DestroyResponse, error) {
	c.destroys++
	return &vmmdpb.DestroyResponse{}, nil
}

func TestMigrationAdmissionClientRefusesInvalidReceiptWithoutGenericDestroy(t *testing.T) {
	for _, kind := range []string{"admission", "wake", "healthcheck", "monitoring"} {
		t.Run(kind, func(t *testing.T) {
			admission, wake := uuid.NewString(), uuid.NewString()
			response := &vmmdpb.AdoptMigratedInstanceResponse{AdmissionId: admission, WakeId: wake, SupportsSecretAliases: true, ImageHealthcheckVerified: true, SupportsImageHealthcheckMonitoring: true}
			switch kind {
			case "admission":
				response.AdmissionId = ""
			case "wake":
				response.WakeId = uuid.NewString()
			case "healthcheck":
				response.ImageHealthcheckVerified = false
			case "monitoring":
				response.SupportsImageHealthcheckMonitoring = false
			}
			stub := &migrationReplyClient{reply: response}
			client := &VMMClient{cli: stub}
			app := AppSpec{ImageHealthcheckRequired: true, migrationAdmissionID: admission}
			ctx := wire.WithContext(t.Context(), wire.CorrelationFields{WakeID: wake})
			if _, err := client.AdoptMigratedInstance(ctx, "dest", "instance", app, "mem", "state", "lease"); !errors.Is(err, state.ErrMigrationAdoptionUnresolved) || stub.destroys != 0 {
				t.Fatal("invalid receipt bypassed admission retirement", err, stub.destroys)
			}
		})
	}
}

func admissionTestVMM(store *state.MemStore, vmm RoutedVMM) *migrationAdmissionTestVMM {
	return &migrationAdmissionTestVMM{RoutedVMM: vmm, store: store, proofs: make(map[string]string)}
}

func (v *migrationAdmissionTestVMM) AcknowledgeMigration(ctx context.Context, node, id, lease string) error {
	if err := v.RoutedVMM.AcknowledgeMigration(ctx, node, id, lease); err != nil {
		return err
	}
	r, err := v.store.RequestMigrationSourceRetirement(ctx, lease, node)
	if err != nil {
		return err
	}
	if err := v.store.AcknowledgeMigrationSource(ctx, r.MigrationSourceBinding, state.MigrationSourceRetired, "original-source-retired:"+lease); err != nil {
		return err
	}
	return v.store.DeleteMigrationLease(ctx, lease)
}

func (v *migrationAdmissionTestVMM) CancelLiveMigration(ctx context.Context, node, id, lease string) error {
	if err := v.RoutedVMM.CancelLiveMigration(ctx, node, id, lease); err != nil {
		return err
	}
	r, err := v.store.MigrationSourceByLease(ctx, lease)
	if err != nil {
		return err
	}
	if safe, err := v.store.FenceMigrationSourceResume(ctx, id, lease); err != nil || !safe {
		return errors.Join(err, state.ErrMigrationAdoptionUnresolved)
	}
	outcome := state.MigrationSourceResumed
	i, err := v.store.MigrationInstanceByID(ctx, id)
	if err != nil {
		return err
	}
	if i.State != string(state.StateRunning) && i.State != string(state.StateDraining) {
		r, err = v.store.RequestMigrationSourceRetirement(ctx, lease, node)
		if err != nil {
			return err
		}
		outcome = state.MigrationSourceRetired
	}
	if err := v.store.AcknowledgeMigrationSource(ctx, r.MigrationSourceBinding, outcome, "original-source-result:"+lease); err != nil {
		return err
	}
	return v.store.DeleteMigrationLease(ctx, lease)
}

func (v *migrationAdmissionTestVMM) PrepareLiveMigration(ctx context.Context, node, id, key string) (LiveMigrationPrepare, error) {
	r, err := v.RoutedVMM.PrepareLiveMigration(ctx, node, id, key)
	if err != nil {
		return r, err
	}
	if err := v.store.ReserveMigrationLease(ctx, state.MigrationLease{InstanceID: id, SourceNodeID: node, LeaseToken: r.LeaseToken, CreatedAt: time.Now(), LeaseExpiresAt: time.Now().Add(time.Duration(api.MigrateLiveLeaseSeconds) * time.Second), Pending: true}); err != nil {
		return LiveMigrationPrepare{}, err
	}
	i, err := v.store.MigrationInstanceByID(ctx, id)
	if err != nil {
		return LiveMigrationPrepare{}, err
	}
	if _, err := v.store.BindMigrationSource(ctx, state.MigrationSourceBinding{InstanceID: id, SourceNodeID: node, SourceWakeID: i.WakeID, LeaseToken: r.LeaseToken, NativeGeneration: uuid.NewString(), PhysicalBinding: strings.Repeat("c", 64)}); err != nil {
		return LiveMigrationPrepare{}, err
	}
	if err := v.store.CompleteMigrationLease(ctx, r.LeaseToken, r.MemStorageKey, r.VMStateStorageKey); err != nil {
		return LiveMigrationPrepare{}, err
	}
	return r, nil
}

func (v *migrationAdmissionTestVMM) AdoptMigratedInstance(ctx context.Context, node, id string, spec AppSpec, mem, key, lease string) (LiveMigrationAdopt, error) {
	digest, err := migrationadmissionwire.Digest(migrationAdoptionRequest(ctx, id, spec, mem, key, lease))
	if err != nil {
		return LiveMigrationAdopt{}, err
	}
	a, execute, err := v.store.StartMigrationAdoption(ctx, spec.migrationAdmissionID, node, digest)
	if err != nil {
		return LiveMigrationAdopt{}, err
	}
	if !execute {
		response := new(vmmdpb.AdoptMigratedInstanceResponse)
		if err := proto.Unmarshal(a.AdoptionReceipt, response); err != nil {
			return LiveMigrationAdopt{}, err
		}
		return LiveMigrationAdopt{WakeID: response.WakeId, HostIP: response.HostIp, Netns: response.Netns, GuestUID: int(response.GuestUid), Method: response.Method}, nil
	}
	r, err := v.RoutedVMM.AdoptMigratedInstance(ctx, node, id, spec, mem, key, lease)
	if err != nil {
		return r, err
	}
	data, err := proto.Marshal(&vmmdpb.AdoptMigratedInstanceResponse{WakeId: r.WakeID, HostIp: r.HostIP, Netns: r.Netns, GuestUid: int32(r.GuestUID), Method: r.Method})
	if err != nil {
		return r, err
	}
	if err := v.store.AcknowledgeMigrationAdoption(ctx, a.ID, node, digest, data); err != nil {
		return r, err
	}
	return r, nil
}

func (v *migrationAdmissionTestVMM) RetireMigrationAdoption(ctx context.Context, node, id string) (string, error) {
	if v.cleanupErr != nil {
		return "", v.cleanupErr
	}
	a, err := v.store.RequestMigrationAdmissionCleanup(ctx, id, node)
	if err != nil {
		return "", err
	}
	if !a.Pending() {
		return a.Phase, nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	proof := v.proofs[id]
	if proof == "" {
		if err := v.RoutedVMM.Destroy(ctx, node, a.InstanceID); err != nil {
			return "", err
		}
		proof = "original-native-retirement:" + id
		v.proofs[id] = proof
	}
	if err := v.store.AcknowledgeMigrationAdmissionCleanup(ctx, id, node, proof); err != nil {
		return "", err
	}
	return state.MigrationCleaned, nil
}

func TestMigrationAdmissionRetainsCapacityWhenCleanupFails(t *testing.T) {
	s := state.NewMemStore()
	_, app, _ := seedApp(t, s, api.PlanHobby, 256, 3)
	vmm := &migrationReceiptVMM{fakeVMM: &fakeVMM{}, method: vmmdpb.WakeMethod_WAKE_COLD_BOOT}
	admissionVMM := admissionTestVMM(s, vmm)
	e := newEngine(t, s, admissionVMM, &fakeNotifier{}, "1.10.0")
	woken, err := e.Wake(t.Context(), app.ID, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.InstanceByID(t.Context(), woken.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	dest := admissionPeer(t, s)
	vmm.adoptHook = func(string) {
		if err := s.NodeSetLifecycle(t.Context(), dest.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
			t.Fatal(err)
		}
	}
	admissionVMM.cleanupErr = errors.New("destination is unreachable")
	ledger := NewNodeLedger()
	h := NewMigrationHarness(t.Context(), s, admissionVMM, wire.NewOpsMetrics("schedd"), testLog(), dest.ID, e.BuildAppSpecForMigration, ledger, nil)
	if err := h.MigrateOne(t.Context(), original.ID, original.NodeID); !errors.Is(err, state.ErrMigrationCommitUnresolved) {
		t.Fatalf("cleanup failure lost uncertainty: %v", err)
	}
	row, err := s.InstanceByID(t.Context(), original.ID)
	if err != nil || row.NodeID != original.NodeID || row.State != string(state.StateMigrating) || ledger.ResidentRAMForNode(dest.ID) == 0 || vmm.cancels != 0 || vmm.destroys != 0 {
		t.Fatalf("unproven cleanup released source/capacity: %+v %v", row, err)
	}
	admissionVMM.cleanupErr = nil
	if _, err := h.resolveMigrationAdmission(t.Context(), row.ID, row.NodeID, row.LeaseToken, vmm.prepared); err != nil {
		t.Fatal(err)
	}
	if ledger.ResidentRAMForNode(dest.ID) != 0 || vmm.cancels != 1 || vmm.destroys != 1 {
		t.Fatal("proven cleanup did not release original reservations")
	}
}

func TestMigrationAdmissionSourceFenceRejectsDelayedReservation(t *testing.T) {
	s := state.NewMemStore()
	_, app, dep := seedApp(t, s, api.PlanHobby, 256, 3)
	source, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	dest := admissionPeer(t, s)
	i, err := s.CreateInstance(t.Context(), app.ID, dep.ID, "running", 256, source.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	vmm := admissionTestVMM(s, &fakeVMM{})
	p, err := vmm.PrepareLiveMigration(t.Context(), source.ID, i.ID, "migration/mem")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInstanceMigrating(t.Context(), i.ID, source.ID, p.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if safe, err := s.FenceMigrationSourceResume(t.Context(), i.ID, p.LeaseToken); err != nil || !safe {
		t.Fatalf("source fence: %t %v", safe, err)
	}
	_, err = s.ReserveMigrationAdmission(t.Context(), state.MigrationAdmissionPlan{ID: uuid.NewString(), InstanceID: i.ID, SourceNodeID: source.ID, DestinationNodeID: dest.ID, SourceWakeID: i.WakeID, DestinationWakeID: uuid.NewString(), DestinationGeneration: dest.LifecycleGeneration, LeaseToken: p.LeaseToken, MemStorageKey: p.MemStorageKey, VMStateStorageKey: p.VMStateStorageKey, RequestDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RAMMB: 264, CPUMillicores: 1000, VCPU: 2, ExpiresAt: time.Now().Add(time.Second)})
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delayed destination passed source-resume fence: %v", err)
	}
}

func TestMigrationSourceLostAcknowledgementBlocksSourceMaintenance(t *testing.T) {
	s := state.NewMemStore()
	_, app, _ := seedApp(t, s, api.PlanHobby, 256, 3)
	vmm := &migrationReceiptVMM{fakeVMM: &fakeVMM{}, method: vmmdpb.WakeMethod_WAKE_RESTORE}
	vmm.ackErr = errors.New("source acknowledgement lost")
	admissionVMM := admissionTestVMM(s, vmm)
	e := newEngine(t, s, admissionVMM, &fakeNotifier{}, "1.10.0")
	woken, err := e.Wake(t.Context(), app.ID, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.InstanceByID(t.Context(), woken.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	dest := admissionPeer(t, s)
	h := NewMigrationHarness(t.Context(), s, admissionVMM, wire.NewOpsMetrics("schedd"), testLog(), dest.ID, e.BuildAppSpecForMigration, NewNodeLedger(), nil)
	if err := h.MigrateOne(t.Context(), original.ID, original.NodeID); err != nil {
		t.Fatal(err)
	}
	moved, err := s.InstanceByID(t.Context(), original.ID)
	if err != nil || moved.NodeID != dest.ID {
		t.Fatalf("destination ownership lost after source ACK failure: %+v %v", moved, err)
	}
	source, err := s.NodeGet(t.Context(), original.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.NodeSetLifecycle(t.Context(), source.ID, source.Lifecycle, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	source, err = s.NodeGet(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	completion := state.NodeLifecycleCompletion{NodeID: source.ID, Generation: source.LifecycleGeneration, Lifecycle: source.Lifecycle}
	if _, err := s.CompleteNodeLifecycle(t.Context(), completion); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("maintenance crossed failed source cleanup: %v", err)
	}
	if used, err := s.ComputeNodeUsedMB(t.Context(), source.ID); err != nil || used < int64(original.RAMMB+api.PerVMOverheadMB) {
		t.Fatalf("source residency lost after commit: %d %v", used, err)
	}
	vmm.ackErr = nil
	if err := admissionVMM.AcknowledgeMigration(t.Context(), source.ID, moved.ID, moved.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteNodeLifecycle(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
}

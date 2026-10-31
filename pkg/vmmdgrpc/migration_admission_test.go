// adr: 1529
package vmmdgrpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/migrationadmissionwire"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/protobuf/proto"
)

type incomingMigrationVMM struct {
	*migrationHandlerVMM
	adopts, retires int
	retireErr       error
	unavailable     bool
	sourceBindings  map[string]state.MigrationSourceBinding
	sourceSnapshot  func(state.MigrationSourceBinding, fcvm.SnapshotSpec) error
}

func (v *incomingMigrationVMM) MigrationAdmissionAvailable(string) bool { return !v.unavailable }

func (v *incomingMigrationVMM) AdoptMigrationAdmission(ctx context.Context, p state.MigrationAdmissionPlan, req fcvm.WakeRequest) (*fcvm.Instance, error) {
	v.adopts++
	if p.InstanceID != req.Instance {
		return nil, errors.New("wrong original instance")
	}
	return v.Wake(ctx, req)
}

func (v *incomingMigrationVMM) RetireMigrationAdmission(_ context.Context, p state.MigrationAdmissionPlan) (string, error) {
	v.retires++
	if v.retireErr != nil {
		return "", v.retireErr
	}
	return "native-original-generation:" + p.ID, nil
}

type incomingMigrationFixture struct {
	store  *state.MemStore
	vmm    *incomingMigrationVMM
	server *Server
	req    *vmmdpb.AdoptMigratedInstanceRequest
	plan   state.MigrationAdmissionPlan
}

func newIncomingMigrationFixture(t *testing.T) incomingMigrationFixture {
	t.Helper()
	s := state.NewMemStore()
	account, err := s.CreateAccount(t.Context(), uuid.NewString()+"@incoming.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "incoming-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:incoming"})
	if err != nil {
		t.Fatal(err)
	}
	node := func() state.ComputeNode {
		n, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: uuid.NewString(), Lifecycle: state.NodeLifecycleActive, VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	source, dest := node(), node()
	i, err := s.CreateInstance(t.Context(), app.ID, dep.ID, "running", 128, source.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	lease := state.MigrationLease{InstanceID: i.ID, SourceNodeID: source.ID, LeaseToken: uuid.NewString(), CreatedAt: time.Now(), LeaseExpiresAt: time.Now().Add(5 * time.Minute), Pending: true}
	if err := s.ReserveMigrationLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindMigrationSource(t.Context(), state.MigrationSourceBinding{InstanceID: i.ID, SourceNodeID: source.ID, SourceWakeID: i.WakeID, LeaseToken: lease.LeaseToken, NativeGeneration: uuid.NewString(), PhysicalBinding: strings.Repeat("c", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteMigrationLease(t.Context(), lease.LeaseToken, "incoming/mem", "incoming/state"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInstanceMigrating(t.Context(), i.ID, source.ID, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	req := &vmmdpb.AdoptMigratedInstanceRequest{AdmissionId: uuid.NewString(), InstanceId: i.ID, WakeId: uuid.NewString(), LeaseToken: lease.LeaseToken, MemStorageKey: "incoming/mem", VmstateStorageKey: "incoming/state", Plan: string(api.PlanPro), AccountId: account.ID, DeploymentId: dep.ID, FcVersion: "1.10.0", AppSpec: &vmmdpb.AppSpec{AppId: app.ID, BaseKey: "base", LayerKey: "layer", VcpuCount: 4, MemSizeMib: 128}}
	digest, err := migrationadmissionwire.Digest(req)
	if err != nil {
		t.Fatal(err)
	}
	p := state.MigrationAdmissionPlan{ID: req.AdmissionId, InstanceID: i.ID, SourceNodeID: source.ID, DestinationNodeID: dest.ID, SourceWakeID: i.WakeID, DestinationWakeID: req.WakeId, LeaseToken: lease.LeaseToken, MemStorageKey: req.MemStorageKey, VMStateStorageKey: req.VmstateStorageKey, RequestDigest: digest, DestinationGeneration: dest.LifecycleGeneration, RAMMB: 128 + api.PerVMOverheadMB, CPUMillicores: api.DefaultAppCPUMillicores, VCPU: 4, ExpiresAt: time.Now().Add(time.Minute).UTC().Truncate(time.Microsecond)}
	if _, err := s.ReserveMigrationAdmission(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	v := &incomingMigrationVMM{migrationHandlerVMM: &migrationHandlerVMM{wakeMethod: fcvm.WakeRestore}}
	server := New(v, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithMigrationStore(s).WithNodeID(dest.ID)
	return incomingMigrationFixture{store: s, vmm: v, server: server, req: req, plan: p}
}

type incomingAckFailure struct {
	state.MigrationAdmissionStore
	before, after bool
}

func (s *incomingAckFailure) AcknowledgeMigrationAdoption(ctx context.Context, id, node, digest string, receipt []byte) error {
	if s.before {
		return errors.New("ack write unavailable")
	}
	if err := s.MigrationAdmissionStore.AcknowledgeMigrationAdoption(ctx, id, node, digest, receipt); err != nil {
		return err
	}
	if s.after {
		return errors.New("ack reply lost")
	}
	return nil
}

// ADR-570: the adopted instance keeps its destination node and the caller's
// correlation, as a wake does, so routing and telemetry follow it.
func TestIncomingMigrationAdoptionCarriesDestinationNodeAndCorrelation(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	ctx := wire.WithContext(t.Context(), wire.CorrelationFields{AppID: f.req.AppSpec.AppId, WakeID: "caller-wake"})
	if _, err := f.server.AdoptMigratedInstance(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if f.vmm.wakeReq.NodeID != f.plan.DestinationNodeID || f.vmm.wakeCorrelation.WakeID != f.plan.DestinationWakeID || f.vmm.wakeCorrelation.AppID != f.req.AppSpec.AppId {
		t.Fatalf("adopted routing lifetime: node=%q correlation=%+v", f.vmm.wakeReq.NodeID, f.vmm.wakeCorrelation)
	}
}

func TestIncomingMigrationLostReplyReplaysOriginalAfterDaemonReplacement(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	f.server.migrationAdmissions = &incomingAckFailure{MigrationAdmissionStore: f.store, after: true}
	if _, err := f.server.AdoptMigratedInstance(t.Context(), f.req); err == nil {
		t.Fatal("lost acknowledgement returned success")
	}
	if f.vmm.wakeFields.WakeID != f.plan.DestinationWakeID {
		t.Fatal("destination wake lineage was lost")
	}
	f.vmm.unavailable = true // Retained receipts precede mutable native availability.
	next := New(f.vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithMigrationStore(f.store).WithNodeID(f.plan.DestinationNodeID)
	first, err := next.AdoptMigratedInstance(t.Context(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := next.AdoptMigratedInstance(t.Context(), f.req)
	if err != nil || !proto.Equal(first, second) || first.AdmissionId != f.plan.ID || first.WakeId != f.plan.DestinationWakeID || f.vmm.adopts != 1 {
		t.Fatalf("replay restarted VM: %+v %v adopts=%d", second, err, f.vmm.adopts)
	}
}

func TestIncomingMigrationUnknownAcknowledgementRequiresProvenCleanup(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	f.server.migrationAdmissions = &incomingAckFailure{MigrationAdmissionStore: f.store, before: true}
	for range 2 {
		if _, err := f.server.AdoptMigratedInstance(t.Context(), f.req); err == nil {
			t.Fatal("unknown dispatch was accepted")
		}
	}
	if f.vmm.adopts != 1 {
		t.Fatal("unknown VM start replayed")
	}
	if safe, err := f.store.FenceMigrationSourceResume(t.Context(), f.plan.InstanceID, f.plan.LeaseToken); err != nil || safe {
		t.Fatal("source resumed before destination proof", err)
	}
	f.vmm.retireErr = errors.New("original process exit unconfirmed")
	retire := &vmmdpb.RetireMigrationAdoptionRequest{AdmissionId: f.plan.ID}
	if _, err := f.server.RetireMigrationAdoption(t.Context(), retire); err == nil {
		t.Fatal("unknown cleanup was acknowledged")
	}
	a, err := f.store.MigrationAdmissionByID(t.Context(), f.plan.ID)
	if err != nil || a.Phase != state.MigrationCleaning {
		t.Fatal("cleanup failure released capacity", err)
	}
	f.vmm.retireErr = nil
	if _, err := f.server.RetireMigrationAdoption(t.Context(), retire); err != nil {
		t.Fatal(err)
	}
	retires := f.vmm.retires
	if _, err := f.server.RetireMigrationAdoption(t.Context(), retire); err != nil || f.vmm.retires != retires {
		t.Fatal("retained proof repeated physical cleanup", err)
	}
	if _, err := f.server.AdoptMigratedInstance(t.Context(), f.req); err == nil || f.vmm.adopts != 1 {
		t.Fatal("delayed creation followed cleanup", err)
	}
	if safe, err := f.store.FenceMigrationSourceResume(t.Context(), f.plan.InstanceID, f.plan.LeaseToken); err != nil || !safe {
		t.Fatal("proven cleanup did not release source", err)
	}
}

func TestIncomingMigrationRejectsChangedRequestOrReceiverBeforeEffects(t *testing.T) {
	for _, change := range []string{"missing_admission", "changed_body", "wrong_node", "native_unavailable"} {
		t.Run(change, func(t *testing.T) {
			f := newIncomingMigrationFixture(t)
			switch change {
			case "missing_admission":
				f.req.AdmissionId = ""
			case "changed_body":
				f.req.AppSpec.MemSizeMib++
			case "wrong_node":
				f.server.WithNodeID(f.plan.SourceNodeID)
			case "native_unavailable":
				f.vmm.unavailable = true
			}
			if _, err := f.server.AdoptMigratedInstance(t.Context(), f.req); err == nil || f.vmm.adopts != 0 {
				t.Fatal("invalid admission reached VM", err)
			}
			a, err := f.store.MigrationAdmissionByID(t.Context(), f.plan.ID)
			if err != nil || a.Phase != state.MigrationReserved {
				t.Fatal("invalid request consumed dispatch", err)
			}
		})
	}
}

func TestIncomingMigrationRecoveryRetainsFailureAndAdvances(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	if _, err := f.server.AdoptMigratedInstance(t.Context(), f.req); err != nil {
		t.Fatal(err)
	}
	f.vmm.retireErr = errors.New("cleanup unavailable")
	f.server.reconcileMigrationAdmissions(t.Context(), time.Now().Add(time.Hour))
	if f.server.migrationAdmissionAfter != f.plan.ID {
		t.Fatal("failed attempt did not advance recovery cursor")
	}
	a, err := f.store.MigrationAdmissionByID(t.Context(), f.plan.ID)
	if err != nil || a.Phase != state.MigrationCleaning {
		t.Fatal("recovery released failed attempt", err)
	}
	f.server.reconcileMigrationAdmissions(t.Context(), time.Now().Add(time.Hour))
	if f.server.migrationAdmissionAfter != "" {
		t.Fatal("recovery cursor did not wrap")
	}
	f.vmm.retireErr = nil
	f.server.reconcileMigrationAdmissions(t.Context(), time.Now().Add(time.Hour))
	a, err = f.store.MigrationAdmissionByID(t.Context(), f.plan.ID)
	if err != nil || a.Phase != state.MigrationCleaned {
		t.Fatal("restart recovery did not consume original proof", err)
	}
}

func TestIncomingMigrationFencesSourceCancelAndExpiryUntilCleanup(t *testing.T) {
	f := newIncomingMigrationFixture(t)
	if _, err := f.server.AdoptMigratedInstance(t.Context(), f.req); err != nil {
		t.Fatal(err)
	}
	source := New(f.vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithMigrationStore(f.store).WithNodeID(f.plan.SourceNodeID)
	cancel := &vmmdpb.CancelLiveMigrationRequest{InstanceId: f.plan.InstanceID, LeaseToken: f.plan.LeaseToken}
	if _, err := source.CancelLiveMigration(t.Context(), cancel); err == nil || f.vmm.resumes != 0 {
		t.Fatal("cancel resumed source beside unknown destination", err)
	}
	entry, err := source.migrationLeaseForPhase(t.Context(), f.plan.InstanceID, f.plan.LeaseToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.cleanupExpiredMigration(t.Context(), entry); err == nil || f.vmm.resumes != 0 {
		t.Fatal("expiry resumed source beside destination", err)
	}
	if _, err := f.server.RetireMigrationAdoption(t.Context(), &vmmdpb.RetireMigrationAdoptionRequest{AdmissionId: f.plan.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CancelInstanceMigration(t.Context(), f.plan.InstanceID, f.plan.SourceNodeID, f.plan.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := source.CancelLiveMigration(t.Context(), cancel); err != nil || f.vmm.resumes != 0 || f.vmm.destroys != 1 {
		t.Fatal("unpublished source could not retire after proof", err)
	}
}

func (v *incomingMigrationVMM) BindMigrationSource(_ context.Context, b state.MigrationSourceBinding) (state.MigrationSourceBinding, error) {
	if old, ok := v.sourceBindings[b.LeaseToken]; ok {
		return old, nil
	}
	b.NativeGeneration = uuid.NewString()
	b.PhysicalBinding = strings.Repeat("c", 64)
	if v.sourceBindings == nil {
		v.sourceBindings = make(map[string]state.MigrationSourceBinding)
	}
	v.sourceBindings[b.LeaseToken] = b
	return b, nil
}
func (v *incomingMigrationVMM) SnapshotMigrationSource(ctx context.Context, b state.MigrationSourceBinding, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) {
	if v.sourceSnapshot != nil {
		if err := v.sourceSnapshot(b, spec); err != nil {
			return fcvm.SnapshotInfo{}, err
		}
	}
	return v.SnapshotKeepAlive(ctx, b.InstanceID, spec)
}
func (v *incomingMigrationVMM) FinishMigrationSource(ctx context.Context, b state.MigrationSource, retire bool) (string, string, error) {
	if v.retireErr != nil {
		return "", "", v.retireErr
	}
	outcome := state.MigrationSourceResumed
	if retire {
		v.destroys++
		outcome = state.MigrationSourceRetired
	} else {
		v.resumes++
	}
	return outcome, "physical-source:" + b.NativeGeneration + ":" + outcome, nil
}

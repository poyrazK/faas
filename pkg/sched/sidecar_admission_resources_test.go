package sched

// adr: 766
// adr: 791
import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func sidecarAdmissionFixture(t *testing.T, disableBoost bool) (*state.MemStore, state.App, state.Deployment) {
	t.Helper()
	s := state.NewMemStore()
	_, app, _ := seedApp(t, s, api.PlanPro, 128, 5)
	raw, err := json.Marshal(api.Sidecars{{Name: "cache", Type: api.SidecarTypeSidecar, Image: "ghcr.io/org/cache@sha256:abc", RamMB: 64}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:sidecar", Sidecars: raw, DisableStartupCPUBoost: disableBoost})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDeploymentSidecarLayer(t.Context(), state.DeploymentSidecarLayer{DeploymentID: dep.ID, SidecarName: "cache", StorageKey: "apps/app/cache.ext4"}); err != nil {
		t.Fatal(err)
	}
	return s, app, dep
}

func TestSidecarAdmissionWakeAndPrimeChargeGuestFootprint(t *testing.T) {
	for _, prime := range []bool{false, true} {
		t.Run(map[bool]string{false: "wake", true: "prime"}[prime], func(t *testing.T) {
			s, app, dep := sidecarAdmissionFixture(t, false)
			vmm := &fakeVMM{}
			e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
			observed := false
			vmm.coldBootHook = func() {
				observed = true
				if e.ledger.ResidentRAM() != 200 {
					t.Error("native dispatch omitted allocated sidecar RAM", e.ledger.ResidentRAM())
				}
			}
			if prime {
				if err := e.Prime(t.Context(), app.ID, dep.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := e.Wake(t.Context(), app.ID, dep.ID, "", ""); err != nil {
					t.Fatal(err)
				}
				if e.ledger.ResidentRAM() != 200 {
					t.Fatal("wake omitted allocated sidecar RAM", e.ledger.ResidentRAM())
				}
			}
			if !observed {
				t.Fatal("fixture never reached the charged dispatch boundary")
			}
		})
	}
}

func TestSidecarAdmissionStartupChargesOriginalDeployment(t *testing.T) {
	s, app, dep := sidecarAdmissionFixture(t, false)
	if _, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, state.DefaultLocalNodeName, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(t.Context()); err != nil || e.ledger.ResidentRAM() != 200 {
		t.Fatal("startup omitted original sidecar allocation", e.ledger.ResidentRAM(), err)
	}
}

type sidecarPausedRestoreVMM struct {
	*fakeVMM
	beforeRestore func(string, AppSpec)
}

func (v sidecarPausedRestoreVMM) CreatePausedFromSnapshot(ctx context.Context, node, instance string, spec AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	v.beforeRestore(instance, spec)
	return v.CreateFromSnapshot(ctx, node, instance, spec, snap)
}

func TestSidecarAdmissionWarmRestoreCapturesResourcesAndOriginalRun(t *testing.T) {
	for _, disableBoost := range []bool{false, true} {
		t.Run(map[bool]string{false: "boosted", true: "configured"}[disableBoost], func(t *testing.T) {
			s, app, dep := sidecarAdmissionFixture(t, disableBoost)
			configuredCPU := 250
			var err error
			app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{CPUMillicores: &configuredCPU})
			if err != nil {
				t.Fatal(err)
			}
			acct, err := s.AccountByID(t.Context(), app.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			vmm := sidecarPausedRestoreVMM{fakeVMM: &fakeVMM{}}
			e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
			observed := false
			vmm.beforeRestore = func(instance string, spec AppSpec) {
				observed = true
				row, err := s.InstanceByID(t.Context(), instance)
				if err != nil {
					t.Fatal(err)
				}
				resources, valid := regionalRuntimeLaunchResources(spec)
				e.ledger.mu.Lock()
				defer e.ledger.mu.Unlock()
				r := e.ledger.entries[instance]
				if !valid || r == nil || r.kind != KindWarmPool || r.runtimeRunID == "" || r.runtimeRunID != row.WakeID || r.admissionMB != resources.RAMMB+api.PerVMOverheadMB || r.admissionMB != 200 || r.vcpu != resources.VCPU || r.cpuMillicores+r.cpuBoostMillicores != resources.CPUMillicores {
					t.Fatal("warm dispatch and original reservation disagree", r, resources, row.WakeID)
				}
			}
			if err := e.restoreWarmInstance(t.Context(), app, acct, api.MustLimitsFor(acct.Plan), dep, state.Snapshot{FCVersion: "1.10.0", StorageKey: "snap/sidecar.mem"}, vmm); err != nil {
				t.Fatal(err)
			}
			if !observed || e.ledger.ResidentRAM() != 200 {
				t.Fatal("warm restore omitted sidecar RAM", observed, e.ledger.ResidentRAM())
			}
		})
	}
}

func TestSidecarAdmissionWarmRowRepairKeepsAllocationAsShadow(t *testing.T) {
	for _, regional := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "regional"}[regional], func(t *testing.T) {
			s, app, dep := sidecarAdmissionFixture(t, false)
			node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "sidecar-repair", TargetURL: "unix:///run/faas/repair.sock", Active: true, AdmissionCeilingMB: 256, VCPUBudget: api.VCPUSlots, VPCPUs: api.VCPUSlots})
			if err != nil {
				t.Fatal(err)
			}
			target := 1
			app, err = s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{WarmPoolSize: &target, SetWarmPoolSize: true})
			if err != nil {
				t.Fatal(err)
			}
			warm, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateWaking), app.RAMMB, node.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			values, err := s.RuntimeAppValuesForDeployment(t.Context(), app.AccountID, app.ID, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			fence, err := state.NewRuntimeAppConfigFence(values)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishOwnedInstanceRuntime(t.Context(), state.RuntimeInstancePublication{
				AccountID: app.AccountID, AppID: app.ID, InstanceID: warm.ID, NodeID: warm.NodeID, WakeID: warm.WakeID,
				ExpectedState: string(state.StateWaking), TargetState: string(state.StateWarm), Fence: fence.SecretFence, ConfigFence: fence,
				Netns: "fc-" + warm.ID, HostIP: "10.100.0.2", GuestUID: 20001,
			}); err != nil {
				t.Fatal(err)
			}
			acct, err := s.AccountByID(t.Context(), app.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			vmm := &warmResumeFakeVMM{fakeVMM: &fakeVMM{}}
			e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
			if regional {
				err = e.repairRegionalWarmLedger(t.Context(), app, acct, dep, warm)
			} else {
				_, err = e.Wake(t.Context(), app.ID, dep.ID, "", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			r := e.ledger.entries[warm.ID]
			if r == nil || r.admissionMB != 200 || !r.rowShadow || r.regionalSource != nil {
				t.Fatal("warm row repair lost allocation or fabricated native provenance", r)
			}
		})
	}
}

type unavailableSidecarDeploymentStore struct{ state.Store }

func (s unavailableSidecarDeploymentStore) DeploymentByID(_ context.Context, _ string) (state.Deployment, error) {
	return state.Deployment{}, state.ErrNotFound
}

func TestSidecarAdmissionUnknownDeploymentRetainsPartialChargeAndBlocks(t *testing.T) {
	s, app, dep := sidecarAdmissionFixture(t, false)
	if _, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, state.DefaultLocalNodeName, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, unavailableSidecarDeploymentStore{s}, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(t.Context()); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) || e.ledger.ResidentRAM() != 136 {
		t.Fatal("missing allocation silently lost known resident charge", e.ledger.ResidentRAM(), err)
	}
	if err := e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, Plan: api.PlanPro, RAMMB: 128, VCPU: 1}); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
		t.Fatal("missing sidecar allocation reopened admission", err)
	}
}

type malformedSidecarDeploymentStore struct{ state.Store }

func (s malformedSidecarDeploymentStore) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	dep, err := s.Store.DeploymentByID(ctx, id)
	dep.Sidecars = json.RawMessage(`[{"name":"cache","type":"sidecar","ram_mb":-64}]`)
	return dep, err
}

func TestSidecarAdmissionMalformedAllocationRefusesWakeAndRetainsStartupCharge(t *testing.T) {
	s, app, dep := sidecarAdmissionFixture(t, false)
	vmm := &fakeVMM{}
	e := newEngine(t, malformedSidecarDeploymentStore{s}, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(t.Context(), app.ID, dep.ID, "", ""); err == nil {
		t.Fatal("negative allocation admitted a wake")
	}
	if e.ledger.ResidentRAM() != 0 || vmm.coldBoots != 0 || vmm.restores != 0 {
		t.Fatal("malformed allocation reached native dispatch")
	}
	if _, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, state.DefaultLocalNodeName, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := e.SeedLedger(t.Context()); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) || e.ledger.ResidentRAM() != 136 {
		t.Fatal("malformed allocation lost known resident charge", e.ledger.ResidentRAM(), err)
	}
	if err := e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, Plan: api.PlanPro, RAMMB: 128, VCPU: 1}); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
		t.Fatal("malformed allocation reopened admission", err)
	}
}

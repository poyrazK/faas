package sched

// adr: 490
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func regionalQuotaSchedulerFixture(t *testing.T) (*state.MemStore, state.Account, state.App, state.Deployment) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "quota-scheduler@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "quota-scheduler", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MaxConcurrency: 5, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	return store, account, app, dep
}

func freezeRegionalSchedulerApp(t *testing.T, store *state.MemStore, account state.Account, app state.App) {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuer := uuid.NewString()
	signer, err := runtimequota.NewSigner(issuer, private)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: issuer, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	policy, ok := api.RuntimeQuotaPolicyForPlan(account.Plan)
	if !ok {
		t.Fatal("fixture has no runtime quota policy")
	}
	binding := runtimequota.Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.MustParse(account.ID).String(), RegionalAppID: app.ID, RegionalAppSlug: app.Slug, APIBase: "https://fsn.example.com"}
	if app.OrgID != "" {
		binding.RegionalOrgID = uuid.MustParse(app.OrgID).String()
	}
	grant := runtimequota.Grant{IssuerID: issuer, GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, Binding: binding, Policy: policy, Concurrency: 2, IssuedAtMicros: time.Now().UnixMicro()}
	envelope, err := signer.SignGrant(grant)
	if err != nil {
		t.Fatal(err)
	}
	receiver := state.RegionalRuntimeQuotaAuthority{Store: store, Directories: trust, Binding: binding}
	if _, err = receiver.AcceptGrant(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
}

func TestRegionalRuntimeQuotaSchedulerRefusesBeforeWork(t *testing.T) {
	for _, test := range []string{"wake", "coordinated-wake", "admit", "deployment-admit", "burst", "prime", "warm-fill", "app-task"} {
		t.Run(test, func(t *testing.T) {
			store, account, app, dep := regionalQuotaSchedulerFixture(t)
			poolSize := 1
			if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{WarmPoolSize: &poolSize, SetWarmPoolSize: true}); err != nil {
				t.Fatal(err)
			}
			// The Store decorator deliberately has no optional quota interface of its own.
			decorated := struct{ state.Store }{store}
			vmm := &fakeVMM{}
			engine := newEngine(t, decorated, vmm, &fakeNotifier{}, "1.10.0")
			freezeRegionalSchedulerApp(t, store, account, app)
			var err error
			switch test {
			case "wake":
				_, err = engine.Wake(t.Context(), app.ID, "", "", "")
			case "coordinated-wake":
				_, err = engine.EnsureWake(t.Context(), app.ID, "")
			case "admit":
				_, err = engine.AdmitInstance(t.Context(), app.ID, "", "", "")
			case "deployment-admit":
				_, err = engine.AdmitInstanceForDeployment(t.Context(), app.ID, dep.ID, "", "")
			case "burst":
				_, err = engine.AdmitInstances(t.Context(), app.ID, "", "", 4)
			case "prime":
				err = engine.Prime(t.Context(), app.ID, dep.ID)
			case "warm-fill":
				err = engine.ReconcileWarmPool(t.Context(), app.ID)
			case "app-task":
				_, err = engine.ResolveAppTaskRuntime(t.Context(), AppTaskRestoreRequest{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID})
			}
			if !errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
				t.Fatalf("admission: %v", err)
			}
			if problem := api.AsProblem(err); problem == nil || problem.Code != api.CodeCapacity || problem.Status != 503 {
				t.Fatalf("retryable capacity error: %v", err)
			}
			instances, listErr := store.ListInstancesForApp(t.Context(), app.ID)
			if listErr != nil || len(instances) != 0 || engine.ledger.ResidentRAM() != 0 {
				t.Fatalf("refusal created resident work: %v %v", instances, listErr)
			}
			engine.wakeCoord.mu.Lock()
			queued := len(engine.wakeCoord.inflight)
			engine.wakeCoord.mu.Unlock()
			if queued != 0 || vmm.coldBoots != 0 || vmm.restores != 0 || vmm.prepares != 0 || vmm.adopts != 0 {
				t.Fatal("refusal queued or dispatched runtime work")
			}
		})
	}
}

func TestRegionalRuntimeQuotaSchedulerRetainsWarmAndMigrationSource(t *testing.T) {
	for _, test := range []string{"warm-promotion", "migration", "migration-projection", "running-fast-path"} {
		t.Run(test, func(t *testing.T) {
			store, account, app, dep := regionalQuotaSchedulerFixture(t)
			app.WarmPoolSize = 1
			initial := state.StateRunning
			if test == "warm-promotion" {
				initial = state.StateWarm
			}
			ins, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(initial), app.RAMMB, state.DefaultLocalNodeName, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetInstanceRuntime(t.Context(), ins.ID, "fc-quota", "10.100.0.2", 20001); err != nil {
				t.Fatal(err)
			}
			vmm := &warmResumeFakeVMM{fakeVMM: &fakeVMM{}}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			freezeRegionalSchedulerApp(t, store, account, app)
			switch test {
			case "warm-promotion":
				_, _, err = engine.promoteWarmInstanceLocked(t.Context(), app, account, api.MustLimitsFor(account.Plan), dep, string(state.InstanceModeNormal))
			case "migration":
				h := newHarnessForTest(t, store, vmm.fakeVMM, "destination")
				err = h.MigrateOne(t.Context(), ins.ID, state.DefaultLocalNodeName)
			case "migration-projection":
				_, err = engine.BuildAppSpecForMigration(t.Context(), ins.ID)
			case "running-fast-path":
				_, err = engine.Wake(t.Context(), app.ID, "", "", "")
			}
			if !errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
				t.Fatalf("resident admission: %v", err)
			}
			fresh, err := store.InstanceByID(t.Context(), ins.ID)
			if err != nil || fresh.State != string(initial) {
				t.Fatal("refusal hid or changed resident source", fresh, err)
			}
			if vmm.resumeCalls != 0 || vmm.prepares != 0 || vmm.adopts != 0 || vmm.destroys != 0 {
				t.Fatal("refusal touched resident guest")
			}
		})
	}
}

type unavailableRegionalSchedulerStore struct {
	state.Store
	err error
}

func (s unavailableRegionalSchedulerStore) RegionalRuntimeQuotaHead(context.Context, string) (state.RegionalRuntimeQuotaHead, error) {
	return state.RegionalRuntimeQuotaHead{}, s.err
}

func TestRegionalRuntimeQuotaSchedulerStorageFailureAndIndependentApp(t *testing.T) {
	store, _, app, _ := regionalQuotaSchedulerFixture(t)
	unavailable := errors.New("regional authority unavailable")
	vmm := &fakeVMM{}
	engine := newEngine(t, unavailableRegionalSchedulerStore{Store: store, err: unavailable}, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := engine.AdmitInstance(t.Context(), app.ID, "", "", ""); !errors.Is(err, unavailable) {
		t.Fatal("unknown authority fell back to plan", err)
	}
	if vmm.coldBoots != 0 || engine.ledger.ResidentRAM() != 0 {
		t.Fatal("storage failure admitted work")
	}
	independent := newEngine(t, struct{ state.Store }{store}, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := independent.AdmitInstance(t.Context(), app.ID, "", "", ""); err != nil {
		t.Fatal("independent admission changed", err)
	}
	if vmm.coldBoots != 1 {
		t.Fatal("independent app did not boot")
	}
}

type quotaAcceptedDuringCreateStore struct {
	state.Store
	accept func()
}

func (s quotaAcceptedDuringCreateStore) CreateInstanceWithMode(ctx context.Context, app, deployment, phase string, ram int, node, wake, mode string) (state.Instance, error) {
	ins, err := s.Store.CreateInstanceWithMode(ctx, app, deployment, phase, ram, node, wake, mode)
	if err == nil {
		s.accept()
	}
	return ins, err
}
func TestRegionalRuntimeQuotaSchedulerRechecksBeforeDispatch(t *testing.T) {
	store, account, app, _ := regionalQuotaSchedulerFixture(t)
	decorated := quotaAcceptedDuringCreateStore{Store: store, accept: func() { freezeRegionalSchedulerApp(t, store, account, app) }}
	vmm := &fakeVMM{}
	engine := newEngine(t, decorated, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := engine.AdmitInstance(t.Context(), app.ID, "", "", ""); !errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
		t.Fatal("prepared work bypassed updated quota", err)
	}
	instances, err := store.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 1 || instances[0].State != string(state.StateFailed) {
		t.Fatal("prepared row was not unwound", instances, err)
	}
	if vmm.coldBoots != 0 || vmm.restores != 0 || engine.ledger.ResidentRAM() != 0 {
		t.Fatal("late fence reached runtime or retained ledger reservation")
	}
}

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalRuntimeQuotaAuthorizerUsesDurableApp(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "vm-quota@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "vm-quota-app", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, "deployment", string(state.StateWaking), 128, "node", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := regionalRuntimeQuotaAuthorizer(store)
	if err != nil {
		t.Fatal(err)
	}
	request := fcvm.RuntimeAdmission{Instance: instance.ID, AppID: app.ID}
	if err = authorize(t.Context(), request); err != nil {
		t.Fatal("independent app refused", err)
	}
	wrong := request
	wrong.AppID = uuid.NewString()
	if err = authorize(t.Context(), wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatal("wire app replaced original app", err)
	}
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
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	binding := runtimequota.Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.MustParse(account.ID).String(), RegionalAppID: app.ID, RegionalAppSlug: app.Slug, APIBase: "https://fsn.example.com"}
	grant := runtimequota.Grant{IssuerID: issuer, GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, Binding: binding, Policy: policy, Concurrency: 2, IssuedAtMicros: time.Now().UnixMicro()}
	request.Plan = policy.Plan
	permit := runtimequota.CapacityPermit{Grant: grant, ID: uuid.NewString(), InstanceID: uuid.MustParse(instance.ID).String(), RunID: instance.WakeID, DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityServing}
	seat := runtimequota.ServingSeat{Permit: permit, ID: uuid.NewString()}
	request.RegionalRuntime = &runtimequota.Placement{Permit: permit, Seat: &seat}
	if err = authorize(t.Context(), request); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatalf("metadata on independent app bypassed cell authority: %v", err)
	}
	envelope, err := signer.SignGrant(grant)
	if err != nil {
		t.Fatal(err)
	}
	receiver := state.RegionalRuntimeQuotaAuthority{Store: store, Directories: trust, Binding: binding}
	if _, err = receiver.AcceptGrant(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	if err = authorize(t.Context(), request); !errors.Is(err, state.ErrRegionalRuntimePlacementUnavailable) {
		t.Fatalf("host accepted a fabricated physical permit: %v", err)
	}
	if _, err = store.ReserveRegionalRuntimeCapacity(t.Context(), permit); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReserveRegionalRuntimeServingSeat(t.Context(), seat); err != nil {
		t.Fatal(err)
	}
	if err = authorize(t.Context(), request); !errors.Is(err, state.ErrRegionalRuntimePlacementUnavailable) {
		t.Fatalf("host accepted planned capacity: %v", err)
	}
	if _, err = store.IssueRegionalRuntimeCapacity(t.Context(), app.ID, permit.ID); err != nil {
		t.Fatal(err)
	}
	if err = authorize(t.Context(), request); !errors.Is(err, state.ErrRegionalRuntimePlacementUnavailable) {
		t.Fatalf("host accepted an unissued serving seat: %v", err)
	}
	if _, err = store.IssueRegionalRuntimeServingSeat(t.Context(), app.ID, seat.ID); err != nil {
		t.Fatal(err)
	}
	storageErr := errors.New("placement receipt storage unavailable")
	wrapped, err := regionalRuntimeQuotaAuthorizer(quotaVMPlacementStore{Store: store, err: storageErr})
	if err != nil {
		t.Fatal(err)
	}
	if err = wrapped(t.Context(), request); !errors.Is(err, storageErr) {
		t.Fatalf("store decorator lost mandatory receipt check: %v", err)
	}
	for _, op := range []fcvm.RuntimeAdmission{request} {
		if err = authorize(t.Context(), op); !errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("journal did not fence %+v: %v", op, err)
		}
	}
	wrongGrant := request
	placement := request.RegionalRuntime.Clone()
	placement.Permit.Grant.AllocationID = uuid.NewString()
	wrongGrant.RegionalRuntime = &placement
	if err = authorize(t.Context(), wrongGrant); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatalf("metadata replaced durable allocation: %v", err)
	}
	if err = authorize(t.Context(), fcvm.RuntimeAdmission{Instance: "missing", AppID: app.ID}); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal("regional instance without placement allowed", err)
	}
	wrongPlan := request
	wrongPlan.Plan = api.PlanScale
	if err = authorize(t.Context(), wrongPlan); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal("wire plan replaced canonical grant plan", err)
	}
	if _, err = store.FenceRegionalRuntimeServingSeat(t.Context(), app.ID, seat.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = authorize(t.Context(), request); !errors.Is(err, state.ErrRegionalRuntimePlacementUnavailable) {
		t.Fatal("host accepted fenced serving capacity", err)
	}
	if err = authorize(t.Context(), fcvm.RuntimeAdmission{Instance: "builder", IsBuilder: true}); err != nil {
		t.Fatal("independent builder claim path changed", err)
	}
	if err = authorize(t.Context(), fcvm.RuntimeAdmission{Instance: "execution", ExecutionOnly: true}); err != nil {
		t.Fatal("disposable execution claim path changed", err)
	}
}

func TestRegionalRuntimeHostJournalStartupPinsKernelIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	nodeID, bootID := uuid.NewString(), uuid.NewString()
	reader := func() ([]byte, error) { return []byte(bootID + "\n"), nil }
	if j, err := openRegionalRuntimeHostJournal("", "", reader); err != nil || j != nil {
		t.Fatalf("disabled config: %v", err)
	}
	if j, err := openRegionalRuntimeHostJournal(dir, "", reader); err == nil {
		if j != nil {
			_ = j.Close()
		}
		t.Fatal("unregistered node owns journal")
	}
	if j, err := openRegionalRuntimeHostJournal(dir, nodeID, func() ([]byte, error) { return nil, errors.New("kernel unavailable") }); err == nil {
		if j != nil {
			_ = j.Close()
		}
		t.Fatal("missing kernel identity admitted")
	}
	j, err := openRegionalRuntimeHostJournal(dir, nodeID, reader)
	if err != nil {
		t.Fatal(err)
	}
	i := j.Identity()
	_ = j.Close()
	j, err = openRegionalRuntimeHostJournal(dir, nodeID, reader)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	if j.Identity() != i {
		t.Fatal("daemon restart replaced host incarnation")
	}
}

type quotaVMTaskStore struct {
	state.Store
	task state.AppTask
}

type quotaVMPlacementStore struct {
	state.Store
	err error
}

func (s quotaVMPlacementStore) CheckRegionalRuntimeIssuedPlacement(context.Context, runtimequota.Placement) error {
	return s.err
}

func (s quotaVMTaskStore) AppTaskByID(_ context.Context, account, app, id string) (state.AppTask, error) {
	if s.task.ID != id || s.task.AccountID != account || s.task.AppID != app {
		return state.AppTask{}, state.ErrNotFound
	}
	return s.task, nil
}
func TestRegionalRuntimeQuotaTaskAuthorizerPinsLease(t *testing.T) {
	base := state.NewMemStore()
	expires := time.Now().Add(time.Minute)
	token := uuid.NewString()
	task := state.AppTask{ID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), Status: state.AppTaskRestoring, LeaseToken: &token, LeaseExpiresAt: &expires}
	reader := quotaVMTaskStore{Store: base, task: task}
	authorize, err := regionalRuntimeQuotaAuthorizer(reader)
	if err != nil {
		t.Fatal(err)
	}
	op := fcvm.RuntimeAdmission{Instance: task.ID, AccountID: task.AccountID, AppID: task.AppID, DeploymentID: task.DeploymentID, AppTaskOnly: true}
	if err = authorize(t.Context(), op); err != nil {
		t.Fatal("valid independent task refused", err)
	}
	bad := op
	bad.DeploymentID = uuid.NewString()
	if err = authorize(t.Context(), bad); !errors.Is(err, state.ErrAppTaskLeaseLost) {
		t.Fatal("task deployment pin bypassed", err)
	}
	bad = op
	bad.Command = true
	if err = authorize(t.Context(), bad); !errors.Is(err, state.ErrAppTaskLeaseLost) {
		t.Fatal("command before running fence", err)
	}
	reader.task.Status = state.AppTaskRunning
	authorize, err = regionalRuntimeQuotaAuthorizer(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = authorize(t.Context(), bad); err != nil {
		t.Fatal("valid running task refused", err)
	}
	reader.task.CancelRequested = &expires
	authorize, err = regionalRuntimeQuotaAuthorizer(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = authorize(t.Context(), bad); !errors.Is(err, state.ErrAppTaskLeaseLost) {
		t.Fatal("cancelled task ran", err)
	}
}

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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
	envelope, err := signer.SignGrant(grant)
	if err != nil {
		t.Fatal(err)
	}
	receiver := state.RegionalRuntimeQuotaAuthority{Store: store, Directories: trust, Binding: binding}
	if _, err = receiver.AcceptGrant(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	for _, op := range []fcvm.RuntimeAdmission{request} {
		if err = authorize(t.Context(), op); !errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("journal did not fence %+v: %v", op, err)
		}
	}
	if err = authorize(t.Context(), fcvm.RuntimeAdmission{Instance: "missing", AppID: app.ID}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unknown instance allowed", err)
	}
	if err = authorize(t.Context(), fcvm.RuntimeAdmission{Instance: "builder", IsBuilder: true}); err != nil {
		t.Fatal("independent builder claim path changed", err)
	}
	if err = authorize(t.Context(), fcvm.RuntimeAdmission{Instance: "execution", ExecutionOnly: true}); err != nil {
		t.Fatal("disposable execution claim path changed", err)
	}
}

type quotaVMTaskStore struct {
	state.Store
	state.RegionalRuntimeQuotaAdmissionReader
	task state.AppTask
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
	reader := quotaVMTaskStore{Store: base, RegionalRuntimeQuotaAdmissionReader: base, task: task}
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

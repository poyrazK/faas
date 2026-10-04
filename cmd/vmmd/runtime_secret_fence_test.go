// adr: 583
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type changingRuntimeSecretReportStore struct {
	*state.MemStore
	change func() error
}

func (s *changingRuntimeSecretReportStore) RecordAppSecretRuntimeReload(ctx context.Context, result state.AppSecretRuntimeReloadResult) (int, error) {
	if err := s.change(); err != nil {
		return 0, err
	}
	return s.MemStore.RecordAppSecretRuntimeReload(ctx, result)
}

func (s *changingRuntimeSecretReportStore) RecordAppSecretRuntimeReloadAck(ctx context.Context, result state.AppSecretRuntimeReloadAckResult) (int, error) {
	if err := s.change(); err != nil {
		return 0, err
	}
	return s.MemStore.RecordAppSecretRuntimeReloadAck(ctx, result)
}

type runtimeSecretFenceFixture struct {
	store    *state.MemStore
	receiver *runtimeConfigReceiver
	account  state.Account
	project  state.Project
	app      state.App
	dep      state.Deployment
	instance state.Instance
	cipher   []byte
	revision string
}

func seedRuntimeSecretFenceFixture(t *testing.T) runtimeSecretFenceFixture {
	t.Helper()
	ctx := t.Context()
	f := runtimeSecretFenceFixture{store: state.NewMemStore()}
	var err error
	f.account, err = f.store.CreateAccount(ctx, "secret-report@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = f.store.CreateProject(ctx, state.Project{AccountID: f.account.ID, Slug: "secret-report"})
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = f.store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "secret-report", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	f.dep, err = f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetDeploymentSecretReloadSignal(ctx, f.dep.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	f.instance, err = f.store.CreateInstance(ctx, f.app.ID, f.dep.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f.cipher, err = secretbox.Seal(identity.Recipient(), secretbox.Envelope{"TOKEN": "stage-private"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", f.cipher); err != nil {
		t.Fatal(err)
	}
	manager := fcvm.NewManager(nil, nil, fcvm.Paths{}, "test", nil, nil)
	manager.SetHostIdentity(identity)
	manager.RegisterInstanceForTest(f.instance.ID, f.dep.ID, f.app.ID, f.account.ID)
	f.receiver = &runtimeConfigReceiver{ctx: ctx, mgr: manager, store: f.store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	selection, err := selectRuntimeSecretRowsForWorkload(ctx, f.store, f.dep.ID, f.app.ID, f.account.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	f.revision = selection.Revision
	if _, err := f.store.RecordAppSecretRuntimeReload(ctx, state.AppSecretRuntimeReloadResult{Fence: selection.Fence,
		AccountID: f.account.ID, AppID: f.app.ID, InstanceID: f.instance.ID, Revision: selection.Revision,
		Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent,
		Candidates: []state.AppSecretDeliveryCandidate{{Scope: "stage", Key: "TOKEN", Version: 1}}}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRuntimeSecretAcknowledgementsRejectChangesBeforeWrite(t *testing.T) {
	for _, kind := range []string{"secret_reload_status", "secret_reload_ack"} {
		for _, change := range []string{"secret_recreated", "grants_changed", "environment_recreated"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				ctx := t.Context()
				f := seedRuntimeSecretFenceFixture(t)
				f.receiver.store = &changingRuntimeSecretReportStore{MemStore: f.store, change: func() error {
					switch change {
					case "grants_changed":
						if err := f.store.SetDeploymentSecretReloadSignal(ctx, f.dep.ID, "SIGUSR1"); err != nil {
							return err
						}
					case "secret_recreated":
						if err := f.store.DeleteAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN"); err != nil {
							return err
						}
						if err := f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", f.cipher); err != nil {
							return err
						}
					case "environment_recreated":
						if err := f.store.MarkDeploymentSuperseded(ctx, f.dep.ID); err != nil {
							return err
						}
						if err := f.store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
							return err
						}
						if _, err := f.store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
							return err
						}
						if err := f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", f.cipher); err != nil {
							return err
						}
					}
					return nil
				}}
				request := runtimeConfigRequest{Kind: kind, Revision: f.revision}
				if kind == "secret_reload_status" {
					request.Projection, request.Signal = "updated", "sent"
				} else {
					request.ApplicationAck = "applied"
				}
				response := sendRuntimeConfigTestRequestForInstance(t, f.receiver, f.instance.ID, request)
				if response.Accepted || response.Error != "secret_reload_stale" {
					t.Fatalf("stale report response: %+v", response)
				}
				observations, err := f.store.ListAppSecretRuntimeReloadObservations(ctx, f.account.ID, f.app.ID, "stage")
				if err != nil || (change != "grants_changed" && len(observations) != 0) || (len(observations) != 0 && observations[0].ApplicationAckVersion != 0) {
					t.Fatalf("stale report changed observations: %+v %v", observations, err)
				}
			})
		}
	}
}

func TestRuntimeSecretRevisionRejectsRecreatedIdenticalEnvelope(t *testing.T) {
	f := seedRuntimeSecretFenceFixture(t)
	ctx := t.Context()
	if err := f.store.DeleteAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", f.cipher); err != nil {
		t.Fatal(err)
	}
	response := sendRuntimeConfigTestRequestForInstance(t, f.receiver, f.instance.ID, runtimeConfigRequest{
		Kind: "secret_reload_status", Revision: f.revision, Projection: "updated", Signal: "sent",
	})
	if response.Accepted || response.Error != "secret_reload_stale" {
		t.Fatalf("old guest revision acquired a fresh host fence: %+v", response)
	}
}

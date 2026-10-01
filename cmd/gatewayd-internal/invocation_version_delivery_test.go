// adr: 375
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func invocationDeliveryFixture(t *testing.T) (*state.MemStore, state.App, state.Deployment, gateway.Target) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "inv-delivery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "inv-delivery", Type: state.AppTypeApp, RAMMB: 128,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "default", ImageDigest: "sha256:inv-delivery"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, "node-a", "jail-a")
	if err != nil {
		t.Fatal(err)
	}
	return store, app, dep, gateway.Target{AppID: app.ID, InstanceID: instance.ID, NodeID: instance.NodeID, DeploymentID: dep.ID}
}

func TestSynthAdapterPrewokenInvocationRechecksVersionAndOwner(t *testing.T) {
	for _, kind := range []string{"valid", "legacy account", "wrong deployment", "disabled revision", "foreign account", "deleted app"} {
		t.Run(kind, func(t *testing.T) {
			store, app, dep, target := invocationDeliveryFixture(t)
			inv := state.Invocation{AppID: app.ID, AccountID: app.AccountID, ID: uuid.NewString(), Source: state.InvocationAsyncInvoke,
				Headers: json.RawMessage(`{"x-gregale-revision":"` + dep.ID + `"}`)}
			switch kind {
			case "legacy account":
				inv.AccountID = ""
			case "wrong deployment":
				target.DeploymentID = uuid.NewString()
			case "disabled revision":
				manifest := app.Manifest
				manifest.RevisionPinTTLSeconds = 0
				if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
					t.Fatal(err)
				}
			case "foreign account":
				inv.AccountID = uuid.NewString()
			case "deleted app":
				if err := store.DeleteApp(t.Context(), app.ID); err != nil {
					t.Fatal(err)
				}
			}
			forwards := 0
			adapter := &synthAdapter{store: store, forward: func(gateway.Target) http.Handler {
				forwards++
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) })
			}}
			out, status, err := adapter.InvokeWithTargetStatus(t.Context(), app.ID, inv, target)
			if kind == "valid" || kind == "legacy account" {
				if err != nil || forwards != 1 || status != http.StatusOK || out.InstanceID != target.InstanceID || string(out.Result) != `{"ok":true}` {
					t.Fatalf("valid delivery: forwards=%d status=%d out=%+v err=%v", forwards, status, out, err)
				}
			} else if err == nil || forwards != 0 || status != 0 {
				t.Fatalf("unsafe delivery: forwards=%d status=%d err=%v", forwards, status, err)
			}
			forwards = 0
			body, err := json.Marshal(map[string]any{"invocation_id": inv.ID, "app_id": app.ID, "account_id": inv.AccountID,
				"source": inv.Source, "instance_id": target.InstanceID, "node_id": target.NodeID, "deployment_id": target.DeploymentID,
				"headers": map[string]string{api.RevisionHeader: dep.ID}})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			server := gateway.NewSynthServer("", adapter, nil)
			server.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", bytes.NewReader(body)))
			if kind == "valid" || kind == "legacy account" {
				if rec.Code != http.StatusOK || forwards != 1 {
					t.Fatalf("valid HTTP delivery: forwards=%d status=%d body=%s", forwards, rec.Code, rec.Body.String())
				}
			} else if rec.Code != http.StatusBadGateway || forwards != 0 {
				t.Fatalf("unsafe HTTP delivery: forwards=%d status=%d body=%s", forwards, rec.Code, rec.Body.String())
			}
		})
	}
}

type retiredAsyncSnapshotStore struct{ *state.MemStore }

type syntheticTargetRecorder struct {
	gateway.Backend
	targets []gateway.Target
}

func (r *syntheticTargetRecorder) RecordTarget(_ string, target gateway.Target) {
	r.targets = append(r.targets, target)
}

func TestSynthAdapterUnpinnedTargetMustMatchCommittedInstance(t *testing.T) {
	for _, kind := range []string{"valid", "missing instance", "wrong node", "wrong deployment", "stopped instance", "superseded deployment"} {
		t.Run(kind, func(t *testing.T) {
			store, app, dep, target := invocationDeliveryFixture(t)
			switch kind {
			case "missing instance":
				target.InstanceID = uuid.NewString()
			case "wrong node":
				target.NodeID = "node-foreign"
			case "wrong deployment":
				target.DeploymentID = uuid.NewString()
			case "stopped instance":
				if err := store.UpdateInstanceState(t.Context(), target.InstanceID, string(state.StateStopped)); err != nil {
					t.Fatal(err)
				}
			case "superseded deployment":
				if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeploySuperseded, ""); err != nil {
					t.Fatal(err)
				}
			}
			forwards := 0
			recorder := &syntheticTargetRecorder{}
			adapter := &synthAdapter{store: store, backend: recorder, forward: func(gateway.Target) http.Handler {
				forwards++
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) })
			}}
			body, err := json.Marshal(map[string]any{"invocation_id": uuid.NewString(), "app_id": app.ID, "account_id": app.AccountID,
				"source": state.InvocationAsyncInvoke, "instance_id": target.InstanceID, "node_id": target.NodeID, "deployment_id": target.DeploymentID})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			gateway.NewSynthServer("", adapter, nil).Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", bytes.NewReader(body)))
			if kind == "valid" {
				if forwards != 1 || rec.Code != http.StatusOK {
					t.Fatalf("valid target: forwards=%d status=%d body=%s", forwards, rec.Code, rec.Body)
				}
			} else if forwards != 0 || rec.Code != http.StatusBadGateway {
				t.Fatalf("unsafe target: forwards=%d status=%d body=%s", forwards, rec.Code, rec.Body)
			}
			if len(recorder.targets) != forwards {
				t.Fatalf("unverified target entered placement cache: records=%d forwards=%d", len(recorder.targets), forwards)
			}
		})
	}
}

func (s retiredAsyncSnapshotStore) WithInvocationVersionSnapshot(ctx context.Context, read func(state.InvocationVersionReader) error) error {
	app, err := s.AppBySlug(ctx, "inv-delivery")
	if err != nil {
		return err
	}
	if err := s.DeleteApp(ctx, app.ID); err != nil {
		return err
	}
	return s.MemStore.WithInvocationVersionSnapshot(ctx, read)
}

func TestAsyncRouteEnqueuerRetiredAppBetweenLookupAndSelection(t *testing.T) {
	store, app, _, _ := invocationDeliveryFixture(t)
	enqueuer := &asyncRouteEnqueuer{store: retiredAsyncSnapshotStore{store}}
	accepted, err := enqueuer.EnqueueAsyncRoute(t.Context(), gateway.AsyncRouteRequest{AppID: app.ID, AccountID: app.AccountID,
		Method: "POST", Path: "/", IdempotencyKey: "retired-during-selection"})
	if !errors.Is(err, state.ErrNotFound) || accepted.ID != "" {
		t.Fatalf("retired app enqueued: %+v %v", accepted, err)
	}
	id := uuid.NewSHA1(asyncRouteInvocationNamespace, []byte(app.ID+"\x00retired-during-selection")).String()
	if _, err := store.InvocationByID(t.Context(), id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused selection published invocation: %v", err)
	}
}

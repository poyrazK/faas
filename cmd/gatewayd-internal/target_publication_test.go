// adr: 570
package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTargetPublicationSyntheticChecksReadinessBeforeForward(t *testing.T) {
	for _, kind := range []string{"primary unready", "sidecar unready", "missing source", "missing configuration", "foreign owner", "partial result with error", "ready", "verified disabled probe"} {
		t.Run(kind, func(t *testing.T) {
			store, app, _, target := invocationDeliveryFixture(t)
			at := time.Now().UTC()
			snapshot := gateway.TargetReadinessSnapshot{AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID,
				NodeID: target.NodeID, WakeID: target.WakeID, RequiredSources: []string{"primary_app", "sidecar:proxy"},
				States: map[string]gateway.ReadinessState{"primary_app": {Ready: true, UpdatedAt: at, EventID: 1}, "sidecar:proxy": {Ready: true, UpdatedAt: at, EventID: 2}}}
			var readErr error
			switch kind {
			case "primary unready", "sidecar unready":
				source := "primary_app"
				if kind == "sidecar unready" {
					source = "sidecar:proxy"
				}
				current := snapshot.States[source]
				current.Ready = false
				snapshot.States[source] = current
			case "missing source":
				delete(snapshot.States, "sidecar:proxy")
			case "missing configuration":
				snapshot = gateway.TargetReadinessSnapshot{}
			case "foreign owner":
				snapshot.AppID = uuid.NewString()
			case "partial result with error":
				readErr = errors.New("readiness store unavailable")
			case "verified disabled probe":
				snapshot.RequiredSources, snapshot.States = nil, nil
			}
			reads, forwards := 0, 0
			backend := gateway.NewPGBackend(nil, nil, discardLogger()).WithTargetReadinessLoader(func(ctx context.Context, targets []gateway.Target) (map[string]gateway.TargetReadinessSnapshot, error) {
				reads++
				if len(targets) != 1 || targets[0].AppID != app.ID || targets[0].InstanceID != target.InstanceID {
					t.Fatal("synthetic readiness read lost captured target identity")
				}
				if _, bounded := ctx.Deadline(); !bounded {
					t.Fatal("synthetic readiness read has no deadline")
				}
				return map[string]gateway.TargetReadinessSnapshot{target.InstanceID: snapshot}, readErr
			})
			adapter := &synthAdapter{store: store, backend: backend, forward: func(gateway.Target) http.Handler {
				forwards++
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			}}
			inv := state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke}
			_, status, err := adapter.InvokeWithTargetStatus(t.Context(), app.ID, inv, target)
			ready := kind == "ready" || kind == "verified disabled probe"
			if reads != 1 || backend.CapacityCount(app.ID) != 1 || backend.Pick(app.ID).OK != ready {
				t.Fatalf("publication skipped configuration/capacity: reads=%d capacity=%d pick=%+v", reads, backend.CapacityCount(app.ID), backend.Pick(app.ID))
			}
			if ready {
				if err != nil || status != http.StatusNoContent || forwards != 1 {
					t.Fatalf("verified target refused: status=%d forwards=%d err=%v", status, forwards, err)
				}
			} else if err == nil || forwards != 0 || status != 0 {
				t.Fatalf("unverified target reached guest: status=%d forwards=%d err=%v", status, forwards, err)
			}
		})
	}
}

func TestTargetPublicationMirrorReadinessDoesNotPublishPublicResident(t *testing.T) {
	_, app, _, target := invocationDeliveryFixture(t)
	ready := false
	reads, forwards := 0, 0
	backend := gateway.NewPGBackend(nil, nil, discardLogger()).WithTargetReadinessLoader(func(context.Context, []gateway.Target) (map[string]gateway.TargetReadinessSnapshot, error) {
		reads++
		return map[string]gateway.TargetReadinessSnapshot{target.InstanceID: {AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID,
			NodeID: target.NodeID, WakeID: target.WakeID, RequiredSources: []string{"primary_app"},
			States: map[string]gateway.ReadinessState{"primary_app": {Ready: ready, UpdatedAt: time.Now(), EventID: int64(reads)}}}}, nil
	})
	adapter := &synthAdapter{backend: backend, forward: func(gateway.Target) http.Handler {
		forwards++
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}}
	inv := state.Invocation{ID: uuid.NewString(), AppID: app.ID, Source: state.InvocationReplay}
	for _, state := range []bool{false, true} {
		ready = state
		_, status, _, err := adapter.forwardInvocationWithStatusAndBody(t.Context(), target, inv, false)
		if backend.CapacityCount(app.ID) != 0 || backend.Pick(app.ID).OK {
			t.Fatal("dedicated mirror entered public placement or capacity")
		}
		if ready {
			if err != nil || status != http.StatusNoContent || forwards != 1 {
				t.Fatalf("ready mirror refused: status=%d forwards=%d err=%v", status, forwards, err)
			}
		} else if err == nil || status != 0 || forwards != 0 {
			t.Fatalf("unready mirror forwarded: status=%d forwards=%d err=%v", status, forwards, err)
		}
	}
	if reads != 2 {
		t.Fatalf("mirror readiness was not checked before each forward: %d", reads)
	}
}

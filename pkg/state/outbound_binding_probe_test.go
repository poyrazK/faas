// adr: 430 — probe evidence and promotion fences invalidate on catalog mutations.
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOutboundBindingProbeMem(t *testing.T) { outboundBindingProbeSuite(t, state.NewMemStore()) }
func TestOutboundBindingProbePG(t *testing.T) {
	store, _ := pgStore(t)
	outboundBindingProbeSuite(t, store)
}
func outboundBindingProbeSuite(t *testing.T, store state.Store) {
	ctx := context.Background()
	acct, app, serving, target := bindingPromotionFixture(t, store)
	catalog := store.(state.OutboundBindingStore)
	probes := store.(state.OutboundBindingProbeStore)
	gate := store.(state.BindingPromotionStore)
	id := uuid.NewString()
	_, err := catalog.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{ID: id, AccountID: acct.ID, Name: "health-api", Origin: "https://example.com", Enabled: true, CredentialSource: "customer_sealed", OwnerKind: "customer", AllowedMethods: []string{"GET", "HEAD"}, AllowedPathPrefixes: []string{"/"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.BindOutboundIntegration(ctx, acct.ID, app.ID, id); err != nil {
		t.Fatal(err)
	}
	policy := api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}
	if err := probes.SetOutboundBindingProbePolicy(ctx, acct.ID, id, &policy); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SetOutboundCredential(ctx, acct.ID, id, []byte("PRIVATE_CREDENTIAL")); err != nil {
		t.Fatal(err)
	}
	snapshot := func() state.OutboundBindingProbeSnapshot {
		t.Helper()
		rows, err := probes.ListOutboundBindingProbeSnapshots(ctx, acct.ID, app.ID)
		if err != nil || len(rows) != 1 || rows[0].Policy == nil || len(rows[0].Revision) != 64 {
			t.Fatalf("snapshot: %+v %v", rows, err)
		}
		return rows[0]
	}
	previous := snapshot()
	for _, mutate := range []func() error{
		func() error { return catalog.SetOutboundCredential(ctx, acct.ID, id, []byte("PRIVATE_CREDENTIAL")) },
		func() error {
			return catalog.UpdateOutboundBindingPolicy(ctx, acct.ID, app.ID, id, []string{"HEAD"}, []string{"/health"})
		},
		func() error {
			policy.Method = "HEAD"
			return probes.SetOutboundBindingProbePolicy(ctx, acct.ID, id, &policy)
		},
		func() error {
			policy.Path = "/other"
			return probes.SetOutboundBindingProbePolicy(ctx, acct.ID, id, &policy)
		},
	} {
		fence := bindingPromotionFence(t, gate, acct, app, target)
		if err := mutate(); err != nil {
			t.Fatal(err)
		}
		current := snapshot()
		if current.Revision == previous.Revision {
			t.Fatal("catalog mutation retained evidence revision")
		}
		previous = current
		if _, err := gate.PromoteDeploymentWithBindings(ctx, target.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatalf("accepted changed catalog: %v", err)
		}
	}
	if _, err := probes.GetOutboundBindingProbePolicy(ctx, uuid.NewString(), id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign read: %v", err)
	}
	spec := api.OutboundBindingProbeSpec{IntegrationID: id, GatewayURL: "https://outbound.example.com", Policy: policy}
	raw, _ := json.Marshal(spec)
	pin := &state.BindingVerificationPin{Type: api.BindingTypeOutbound, Binding: id, Revision: strings.Repeat("a", 64), OutboundProbe: &spec}
	params := state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: target.ID, Kind: state.AppTaskKindManual, Command: []string{api.AppTaskOutboundBindingProbeCommand, id, string(raw)}, TimeoutSeconds: 15, MaxOutputBytes: 4096, CreatedAt: time.Now(), BindingVerification: pin, RequireLiveDeployment: true}
	task, err := store.CreateAppTask(ctx, params)
	if err != nil || task.BindingVerification == nil {
		t.Fatalf("outbound admission: %+v %v", task, err)
	}
	params.Command[2] = `{}`
	if _, err := store.CreateAppTask(ctx, params); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("accepted forged spec: %v", err)
	}
	if err := probes.SetOutboundBindingProbePolicy(ctx, acct.ID, id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := probes.GetOutboundBindingProbePolicy(ctx, acct.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("removed policy survived: %v", err)
	}
}

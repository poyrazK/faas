package edgerules

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// phaseNotifier records each published convergence payload.
type phaseNotifier struct {
	payloads []db.EdgeRuleChangedPayload
}

func (n *phaseNotifier) Notify(_ context.Context, _, payload string) error {
	var p db.EdgeRuleChangedPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	n.payloads = append(n.payloads, p)
	return nil
}

func (n *phaseNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	return make(chan db.Notification), func() {}, nil
}

type phaseHost struct {
	attackModeHost
	notif *phaseNotifier
}

func (h phaseHost) Notifier() Notifier { return h.notif }

// adr: 091 — amendment 2026-10-28 (review round 3): a CORS preset is baked
// into the rules that use it, so changing it fences those rules' hosts on
// every gateway before the write and releases them after, like a list edit.
// A preset no rule uses serves no policy and needs no barrier.
func TestCorsPresetChangeGoesThroughTheBarrier(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "presets@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "presets-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	used, err := mem.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: acct.ID, Name: "web", AllowOrigins: []string{"https://a.example"}, AllowMethods: []string{"GET"}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	unused, err := mem.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: acct.ID, Name: "spare", AllowOrigins: []string{"https://b.example"}, AllowMethods: []string{"GET"}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	presetID := used.ID
	if _, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "api.example.com", MatchPath: "/", Enabled: true, Kind: state.EdgeRuleKindCORSA,
		CorsPresetID: &presetID,
		Action:       state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{CorsPresetID: &presetID}},
	}); err != nil {
		t.Fatal(err)
	}
	change := func(id string) (*httptest.ResponseRecorder, *phaseNotifier) {
		notif := &phaseNotifier{}
		svc := &Service{Host: phaseHost{attackModeHost: attackModeHost{store: mem, audits: &[]string{}}, notif: notif}}
		convergences, release, prob := svc.PrepareCorsPresetChange(ctx, acct.ID, id)
		if prob != nil {
			t.Fatalf("prepare %s: %v", id, prob)
		}
		defer release(ctx)
		rec := httptest.NewRecorder()
		if prob := svc.ApplyCorsPresetChange(ctx, rec, id, convergences); prob != nil {
			t.Fatalf("apply %s: %v", id, prob)
		}
		return rec, notif
	}

	rec, notif := change(used.ID)
	var phases []string
	for _, p := range notif.payloads {
		phases = append(phases, p.Phase)
		if p.AppID != app.ID || !slices.Equal(p.MatchHosts, []string{"api.example.com"}) {
			t.Errorf("%s fenced app %q hosts %v, want %q [api.example.com]", p.Phase, p.AppID, p.MatchHosts, app.ID)
		}
	}
	if !slices.Equal(phases, []string{"prepare", "apply"}) {
		t.Fatalf("published phases = %v, want [prepare apply]", phases)
	}
	if got := rec.Header().Get(edgeRulesStateHeader); got != "active" {
		t.Errorf("%s = %q, want active", edgeRulesStateHeader, got)
	}

	if _, notif = change(unused.ID); len(notif.payloads) != 0 {
		t.Fatalf("an unused preset published %d phases, want none", len(notif.payloads))
	}
}

// adr: 091 — amendment 2026-10-28 (review round 3): a credentialed CORS
// policy may not allow every subdomain of a domain the platform owns, since
// every other customer's app is served there.
func TestSharedDomainCORSOriginProblem(t *testing.T) {
	domains, err := hostidentity.NewDomains("platform.example", "apps.example", "deploy.example", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer hostidentity.Configure(domains)()
	for _, tc := range []struct {
		name    string
		origins []string
		creds   bool
		refused bool
	}{
		{name: "every app on the apps domain", origins: []string{"https://a.example", "https://*.apps.example"}, creds: true, refused: true},
		{name: "with a port", origins: []string{"https://*.apps.example:8443"}, creds: true, refused: true},
		{name: "every deployment host", origins: []string{"https://*.deploy.example"}, creds: true, refused: true},
		{name: "without credentials", origins: []string{"https://*.apps.example"}},
		{name: "the customer's own domain", origins: []string{"https://*.shop.example.com"}, creds: true},
		{name: "one app's origin", origins: []string{"https://shop.apps.example"}, creds: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SharedDomainCORSOriginProblem(tc.origins, tc.creds) != nil; got != tc.refused {
				t.Fatalf("refused = %v, want %v", got, tc.refused)
			}
		})
	}
}

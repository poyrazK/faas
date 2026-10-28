package edgerules

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// payloadNotifier records every published convergence payload.
type payloadNotifier struct{ payloads []db.EdgeRuleChangedPayload }

func (n *payloadNotifier) Notify(_ context.Context, _, payload string) error {
	var p db.EdgeRuleChangedPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	n.payloads = append(n.payloads, p)
	return nil
}

func (n *payloadNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	return make(chan db.Notification), func() {}, nil
}

type payloadHost struct {
	attackModeHost
	notif *payloadNotifier
}

func (h payloadHost) Notifier() Notifier { return h.notif }

// TestCorsPresetEditFencesRulesUsingThePreset is issue #2683 for CORS
// presets: editing or deleting a preset changes the CORS policy of every rule
// using it, so those rules' hosts are fenced before the write and released
// after it; a preset no rule uses needs no barrier.
func TestCorsPresetEditFencesRulesUsingThePreset(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "cors-barrier@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cors-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	preset := "preset-1"
	for _, rule := range []state.CreateEdgeRuleParams{
		{AccountID: acct.ID, AppID: app.ID, MatchHost: "api.example.com", MatchPath: "/", Enabled: true, Kind: state.EdgeRuleKindThrottle, CorsPresetID: &preset},
		{AccountID: acct.ID, AppID: app.ID, MatchHost: "other.example.com", MatchPath: "/", Enabled: true, Kind: state.EdgeRuleKindThrottle},
	} {
		if _, err := mem.CreateEdgeRule(ctx, rule); err != nil {
			t.Fatal(err)
		}
	}
	notif := &payloadNotifier{}
	svc := &Service{Host: payloadHost{attackModeHost: attackModeHost{store: mem, audits: &[]string{}}, notif: notif}}

	edit := svc.CorsPresetEdit(acct.ID, preset, "cors_preset_updated")
	convergences, release, prob := svc.PrepareReferencedEdit(ctx, edit)
	if prob != nil {
		t.Fatalf("prepare: %v", prob)
	}
	rec := httptest.NewRecorder()
	if prob := svc.ApplyReferencedEdit(ctx, rec, edit, preset, convergences); prob != nil {
		t.Fatalf("apply: %v", prob)
	}
	release(ctx)
	var phases []string
	for _, p := range notif.payloads {
		phases = append(phases, p.Phase)
		if p.Operation != "cors_preset_updated" || p.AppID != app.ID || !slices.Equal(p.MatchHosts, []string{"api.example.com"}) {
			t.Errorf("%s payload = %+v, want op cors_preset_updated, app %s, hosts [api.example.com]", p.Phase, p, app.ID)
		}
	}
	if !slices.Equal(phases, []string{"prepare", "apply"}) {
		t.Fatalf("published phases = %v, want [prepare apply]", phases)
	}
	if got := rec.Header().Get(edgeRulesStateHeader); got != "active" {
		t.Errorf("%s = %q, want active", edgeRulesStateHeader, got)
	}

	notif.payloads = nil
	unused, release, prob := svc.PrepareReferencedEdit(ctx, svc.CorsPresetEdit(acct.ID, "unused-preset", "cors_preset_deleted"))
	release(ctx)
	if prob != nil || len(unused) != 0 || len(notif.payloads) != 0 {
		t.Fatalf("unused preset: problem %v, %d convergences, %d payloads; want none", prob, len(unused), len(notif.payloads))
	}
}

package edgerules

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// listBarrierNotifier records each published convergence phase and, at
// prepare, the list's items, so a test can see the fence went up before the
// write.
type listBarrierNotifier struct {
	mem            *state.MemStore
	accountID      string
	payloads       []db.EdgeRuleChangedPayload
	itemsAtPrepare []string
}

func (n *listBarrierNotifier) Notify(ctx context.Context, _, payload string) error {
	var p db.EdgeRuleChangedPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	if p.Phase == "prepare" {
		lists, err := n.mem.EdgeRuleListsByName(ctx, n.accountID, []string{"blocked"})
		if err != nil || len(lists) != 1 {
			return err
		}
		n.itemsAtPrepare = lists[0].Items
	}
	n.payloads = append(n.payloads, p)
	return nil
}

func (n *listBarrierNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	return make(chan db.Notification), func() {}, nil
}

type listBarrierHost struct {
	attackModeHost
	notif *listBarrierNotifier
}

func (h listBarrierHost) Notifier() Notifier { return h.notif }

// TestUpdateEdgeRuleListGoesThroughTheBarrier is issue #2683: a list edit
// changes the policy of every rule that references it, so like a rule edit
// it fences those rules' hosts on every gateway before the write and releases
// them after. An unreferenced list serves no policy and needs no barrier.
func TestUpdateEdgeRuleListGoesThroughTheBarrier(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "lists@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "lists-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"blocked", "unused"} {
		if _, err := mem.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: acct.ID, Name: name, Kind: "ip", Items: []string{"203.0.113.1"}}, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "api.example.com", MatchPath: "/", Enabled: true, Kind: state.EdgeRuleKindThrottle,
		Match: &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "blocked"},
	}); err != nil {
		t.Fatal(err)
	}
	patch := func(name string) (*httptest.ResponseRecorder, *listBarrierNotifier) {
		notif := &listBarrierNotifier{mem: mem, accountID: acct.ID}
		svc := &Service{Host: listBarrierHost{attackModeHost: attackModeHost{store: mem, audits: &[]string{}}, notif: notif}}
		req := httptest.NewRequest(http.MethodPatch, "/v1/edge-rule-lists/"+name, strings.NewReader(`{"add":["198.51.100.9"]}`))
		req.SetPathValue("name", name)
		rec := httptest.NewRecorder()
		svc.UpdateEdgeRuleList(rec, req, acct)
		return rec, notif
	}

	rec, notif := patch("blocked")
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH blocked: %d %s", rec.Code, rec.Body.String())
	}
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
	if slices.Contains(notif.itemsAtPrepare, "198.51.100.9") {
		t.Fatalf("the list was written before the fence went up: items at prepare = %v", notif.itemsAtPrepare)
	}
	if got := rec.Header().Get(edgeRulesStateHeader); got != "active" {
		t.Errorf("%s = %q, want active", edgeRulesStateHeader, got)
	}

	rec, notif = patch("unused")
	if rec.Code != http.StatusOK || len(notif.payloads) != 0 {
		t.Fatalf("PATCH unused: %d, published %d phases, want 200 and none", rec.Code, len(notif.payloads))
	}
}

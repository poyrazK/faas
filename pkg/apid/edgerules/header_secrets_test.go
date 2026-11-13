package edgerules

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261108110000 — a secret or credential-named request header value is
// sealed into edge_rule_header_secrets, the rule keeps only its id, and no
// response returns the value; ids round-trip, foreign ids are refused, and a
// value stored before sealing is redacted and sealed on its next write.
func TestHeaderValuesAreSealedAndNeverReturned(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "sealed-headers@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug string) state.App {
		t.Helper()
		a, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	shop, blog := newApp("shop"), newApp("blog")
	var sealed []string
	svc := &Service{
		Host: attackModeHost{store: mem, app: shop, audits: &[]string{}, notif: &recordingNotifier{}},
		SealHeaderValue: func(plaintext string) ([]byte, error) {
			sealed = append(sealed, plaintext)
			return []byte("sealed:" + plaintext), nil
		},
	}
	create := func(app state.App, action string) (api.EdgeRuleResponse, *api.Problem) {
		return svc.CreateRule(ctx, acct, app, api.CreateEdgeRuleRequest{MatchHost: "@app", MatchPath: "/", Kind: "headers", Action: json.RawMessage(action)})
	}
	requestOps := func(t *testing.T, raw json.RawMessage) []api.EdgeRuleHeaderOp {
		t.Helper()
		// Responses carry the stored action shape: {"kind", "headers": {...}}.
		var a struct {
			Headers api.EdgeRuleHeadersAction `json:"headers"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		if len(a.Headers.RequestHeaders) == 0 {
			t.Fatalf("no request headers in %s", raw)
		}
		return a.Headers.RequestHeaders
	}

	created, prob := create(shop, `{"request_headers":[
		{"name":"Authorization","action":"set","value":"Bearer origin-token"},
		{"name":"X-Region","action":"set","value":"eu","secret":true},
		{"name":"X-Trace","action":"set","value":"plain"}]}`)
	if prob != nil {
		t.Fatalf("create: %v", prob)
	}
	if strings.Contains(string(created.Action), "origin-token") || strings.Contains(string(created.Action), `"eu"`) {
		t.Fatalf("response carries a sealed value: %s", created.Action)
	}
	ops := requestOps(t, created.Action)
	if !ops[0].Secret || ops[0].SecretID == "" || !ops[1].Secret || ops[1].SecretID == "" {
		t.Fatalf("credential and secret ops not sealed: %+v", ops)
	}
	if ops[2].Secret || ops[2].Value != "plain" {
		t.Fatalf("a plain header was sealed: %+v", ops[2])
	}
	if len(sealed) != 2 || sealed[0] != "Bearer origin-token" || sealed[1] != "eu" {
		t.Fatalf("sealed = %q", sealed)
	}
	row, err := mem.GetEdgeRuleByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored, _ := json.Marshal(row.Action); strings.Contains(string(stored), "origin-token") {
		t.Fatalf("stored action carries the value: %s", stored)
	}
	secrets, _ := mem.EdgeRuleHeaderSecretsByID(ctx, shop.ID, []string{ops[0].SecretID})
	if len(secrets) != 1 || string(secrets[0].Ciphertext) != "sealed:Bearer origin-token" {
		t.Fatalf("secret row = %+v", secrets)
	}

	t.Run("an update that sends the secret_id back keeps the value", func(t *testing.T) {
		sealed = nil
		action := json.RawMessage(`{"request_headers":[{"name":"Authorization","action":"set","secret":true,"secret_id":"` + ops[0].SecretID + `"}]}`)
		updated, prob := svc.UpdateRule(ctx, acct, created.ID, api.UpdateEdgeRuleRequest{Action: &action})
		if prob != nil {
			t.Fatalf("update: %v", prob)
		}
		if got := requestOps(t, updated.Action); len(got) != 1 || got[0].SecretID != ops[0].SecretID || len(sealed) != 0 {
			t.Fatalf("ops = %+v, sealed again = %q", got, sealed)
		}
	})

	t.Run("another app's secret_id is refused", func(t *testing.T) {
		other, prob := create(blog, `{"request_headers":[{"name":"X-Api-Key","action":"set","value":"blog-key"}]}`)
		if prob != nil {
			t.Fatal(prob)
		}
		foreign := requestOps(t, other.Action)[0].SecretID
		_, prob = create(shop, `{"request_headers":[{"name":"X-Api-Key","action":"set","secret_id":"`+foreign+`"}]}`)
		if prob == nil || prob.Status != http.StatusBadRequest {
			t.Fatalf("foreign secret_id = %v, want 400", prob)
		}
	})

	t.Run("refused shapes", func(t *testing.T) {
		for name, action := range map[string]string{
			"secret response header": `{"response_headers":[{"name":"X-Key","action":"set","value":"v","secret":true}]}`,
			"secret template":        `{"request_headers":[{"name":"X-Key","action":"set","value":"${client_ip}","template":true,"secret":true}]}`,
			"secret with no value":   `{"request_headers":[{"name":"X-Key","action":"set","secret":true}]}`,
		} {
			if _, prob := create(shop, action); prob == nil || prob.Status != http.StatusBadRequest {
				t.Errorf("%s: %v, want 400", name, prob)
			}
		}
	})

	t.Run("a value stored before sealing is redacted, then sealed on write", func(t *testing.T) {
		legacy, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
			AccountID: acct.ID, AppID: shop.ID, MatchHost: "@app", MatchPath: "/legacy", Priority: 50, Enabled: true,
			Kind: state.EdgeRuleKindHeaders,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
				RequestHeaders: []state.EdgeRuleHeaderOp{{Name: "Authorization", Action: "set", Value: "Bearer legacy"}},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp := EdgeRuleResponse(legacy)
		got := requestOps(t, resp.Action)
		if strings.Contains(string(resp.Action), "legacy\"") || !got[0].Redacted || got[0].Value != "" {
			t.Fatalf("legacy response = %s", resp.Action)
		}
		sealed = nil
		roundTrip, _ := json.Marshal(api.EdgeRuleHeadersAction{RequestHeaders: got})
		action := json.RawMessage(roundTrip)
		updated, prob := svc.UpdateRule(ctx, acct, legacy.ID, api.UpdateEdgeRuleRequest{Action: &action})
		if prob != nil {
			t.Fatalf("update: %v", prob)
		}
		after := requestOps(t, updated.Action)
		if len(sealed) != 1 || sealed[0] != "Bearer legacy" || after[0].SecretID == "" || after[0].Redacted {
			t.Fatalf("sealed = %q, ops = %+v", sealed, after)
		}
	})
}

package edgerules

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// attackModeHost serves one app from a MemStore and records audits and
// convergence notifications.
type attackModeHost struct {
	store  *state.MemStore
	app    state.App
	audits *[]string
	notif  *recordingNotifier
	// unreleased lists the platform flags left off; every other flag is on.
	unreleased map[string]bool
}

func (h attackModeHost) LoadApp(w http.ResponseWriter, _ *http.Request, _ state.Account, slug string) (state.App, bool) {
	if slug != h.app.Slug {
		w.WriteHeader(http.StatusNotFound)
		return state.App{}, false
	}
	return h.app, true
}
func (h attackModeHost) Store() state.Store     { return h.store }
func (h attackModeHost) Logger() *slog.Logger   { return slog.New(slog.DiscardHandler) }
func (h attackModeHost) Notifier() Notifier     { return h.notif }
func (h attackModeHost) PromQL() *promql.Client { return nil }
func (h attackModeHost) PlatformFlagEnabled(flag string, _ state.Account) bool {
	return !h.unreleased[flag]
}
func (h attackModeHost) EmitAudit(_ context.Context, kind string, _ *string, _ map[string]any) {
	*h.audits = append(*h.audits, kind)
}

// recordingNotifier records published edge-rule phases. With no serving
// gateways registered nothing waits for acknowledgements.
type recordingNotifier struct{ phases []string }

func (n *recordingNotifier) Notify(_ context.Context, _, payload string) error {
	var p db.EdgeRuleChangedPayload
	_ = json.Unmarshal([]byte(payload), &p)
	n.phases = append(n.phases, p.Phase)
	return nil
}
func (n *recordingNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	return make(chan db.Notification), func() {}, nil
}

type attackModeFixture struct {
	t      *testing.T
	svc    *Service
	mem    *state.MemStore
	acct   state.Account
	app    state.App
	audits []string
	notif  *recordingNotifier
}

// newAttackModeFixture seeds a Free account: no challenge rules of its own,
// and its one-rule-per-kind caps do not apply to attack mode.
func newAttackModeFixture(t *testing.T) *attackModeFixture {
	t.Helper()
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "attack-mode@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	f := &attackModeFixture{t: t, mem: mem, acct: acct, app: app, notif: &recordingNotifier{}}
	f.svc = &Service{Host: attackModeHost{store: mem, app: app, audits: &f.audits, notif: f.notif}}
	return f
}

func (f *attackModeFixture) call(method, body string) (*httptest.ResponseRecorder, api.EdgeAttackModeStatus) {
	f.t.Helper()
	req := httptest.NewRequest(method, "/v1/apps/shop/edge-protection/attack-mode", strings.NewReader(body))
	req.SetPathValue("slug", "shop")
	rec := httptest.NewRecorder()
	switch method {
	case http.MethodPut:
		f.svc.EnableAppEdgeAttackMode(rec, req, f.acct)
	case http.MethodDelete:
		f.svc.DisableAppEdgeAttackMode(rec, req, f.acct)
	default:
		f.svc.GetAppEdgeAttackMode(rec, req, f.acct)
	}
	var out api.EdgeAttackModeStatus
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			f.t.Fatalf("decode %s: %v", method, err)
		}
	}
	return rec, out
}

func (f *attackModeFixture) rules() []state.EdgeRule {
	f.t.Helper()
	rules, err := f.mem.ListEdgeRulesForApp(context.Background(), f.app.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return rules
}

func TestAttackModeOnExtendOff(t *testing.T) {
	f := newAttackModeFixture(t)

	rec, on := f.call(http.MethodPut, `{"duration_seconds": 3600}`)
	if rec.Code != http.StatusOK || !on.Active || on.RuleID == "" || on.StartedAt == nil || on.ExpiresAt == nil {
		t.Fatalf("on: %d %s", rec.Code, rec.Body)
	}
	if left := time.Until(*on.ExpiresAt); left < 59*time.Minute || left > time.Hour {
		t.Fatalf("on: expires in %s, want about an hour", left)
	}
	if on.Difficulty != api.EdgeChallengeDefaultDifficulty || on.PassTTLSeconds != api.EdgeChallengeDefaultPassTTLSeconds {
		t.Fatalf("on: challenge %d/%d, want the defaults", on.Difficulty, on.PassTTLSeconds)
	}
	rules := f.rules()
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want the one attack-mode rule", len(rules))
	}
	r := rules[0]
	if !r.AttackMode() || r.ID != on.RuleID || r.Kind != state.EdgeRuleKindChallenge || r.MatchHost != api.EdgeRuleAppHosts ||
		r.MatchPath != "/**" || r.Priority != 0 || r.Mode != state.EdgeRuleModeEnforce || !r.Enabled {
		t.Fatalf("attack-mode rule = %+v", r)
	}
	if r.Match == nil || r.Match.Field != "verified_bot" || r.Match.Op != "missing" {
		t.Fatalf("attack-mode rule condition = %+v, want verified_bot missing", r.Match)
	}
	if got := EdgeRuleResponse(r).ManagedBy; got != api.EdgeRuleManagedByAttackMode {
		t.Fatalf("managed_by = %q", got)
	}

	// Switching on again moves the end and keeps the rule and its start.
	rec, extended := f.call(http.MethodPut, `{"duration_seconds": 21600}`)
	if rec.Code != http.StatusOK || extended.RuleID != on.RuleID || !extended.StartedAt.Equal(*on.StartedAt) ||
		time.Until(*extended.ExpiresAt) < 5*time.Hour {
		t.Fatalf("extend: %d %s", rec.Code, rec.Body)
	}

	// PATCH cannot turn the rule into something else.
	patch := httptest.NewRequest(http.MethodPatch, "/v1/edge-rules/"+on.RuleID, strings.NewReader(`{"enabled": false}`))
	patch.SetPathValue("id", on.RuleID)
	patchRec := httptest.NewRecorder()
	f.svc.UpdateEdgeRule(patchRec, patch, f.acct)
	if patchRec.Code != http.StatusConflict || !strings.Contains(patchRec.Body.String(), "attack-mode") {
		t.Fatalf("patch: %d %s, want 409 naming the switch", patchRec.Code, patchRec.Body)
	}

	if rec, got := f.call(http.MethodGet, ""); rec.Code != http.StatusOK || !got.Active || got.RuleID != on.RuleID {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	rec, off := f.call(http.MethodDelete, "")
	if rec.Code != http.StatusOK || off.Active || len(f.rules()) != 0 {
		t.Fatalf("off: %d %s, %d rules left", rec.Code, rec.Body, len(f.rules()))
	}
	if rec, again := f.call(http.MethodDelete, ""); rec.Code != http.StatusOK || again.Active {
		t.Fatalf("off again: %d %s", rec.Code, rec.Body)
	}
	want := []string{"edge_attack_mode.enabled", "edge_attack_mode.enabled", "edge_attack_mode.disabled"}
	if strings.Join(f.audits, ",") != strings.Join(want, ",") {
		t.Fatalf("audits = %v, want %v", f.audits, want)
	}
	if !strings.Contains(strings.Join(f.notif.phases, ","), "prepare,apply") {
		t.Fatalf("convergence phases = %v, want prepare then apply", f.notif.phases)
	}
}

func TestAttackModeDefaultsAndBounds(t *testing.T) {
	f := newAttackModeFixture(t)
	for _, body := range []string{`{"duration_seconds": 60}`, `{"duration_seconds": 90000}`, `{"duration": "1h"}`} {
		if rec, _ := f.call(http.MethodPut, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", body, rec.Code)
		}
	}
	if len(f.rules()) != 0 {
		t.Fatal("a refused switch created a rule")
	}
	rec, on := f.call(http.MethodPut, "")
	if rec.Code != http.StatusOK || time.Until(*on.ExpiresAt) < 59*time.Minute {
		t.Fatalf("empty body: %d %s, want the one-hour default", rec.Code, rec.Body)
	}
}

// Once attack mode has run out, switching it on starts a fresh one: a new
// rule and start time instead of reviving the expired row.
func TestAttackModeAfterExpiryStartsFresh(t *testing.T) {
	f := newAttackModeFixture(t)
	past := time.Now().Add(-time.Minute)
	params := attackModeRuleParams(f.acct, f.app, past)
	expired, err := f.mem.CreateEdgeRule(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if rec, got := f.call(http.MethodGet, ""); rec.Code != http.StatusOK || got.Active || got.RuleID != expired.ID {
		t.Fatalf("lapsed: %d %s, want inactive with the expired rule", rec.Code, rec.Body)
	}
	rec, on := f.call(http.MethodPut, "")
	if rec.Code != http.StatusOK || !on.Active || on.RuleID == expired.ID {
		t.Fatalf("restart: %d %s, want a new rule", rec.Code, rec.Body)
	}
	if rules := f.rules(); len(rules) != 1 || rules[0].ID != on.RuleID {
		t.Fatalf("rules after restart = %+v, want only the new rule", rules)
	}
}

// adr: 20261021220456 — amendment 1: attack mode is switched on only while
// both the edge_attack_mode and edge_challenge flags include the account;
// switching it off always works.
func TestAttackModeNeedsItsFlags(t *testing.T) {
	for _, off := range []string{platformflags.EdgeAttackMode, platformflags.EdgeChallenge} {
		f := newAttackModeFixture(t)
		host := f.svc.Host.(attackModeHost)
		host.unreleased = map[string]bool{off: true}
		f.svc.Host = host
		rec, _ := f.call(http.MethodPut, `{"duration_seconds":3600}`)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), api.CodeEdgeFeatureUnavailable) {
			t.Fatalf("%s off: on = %d %s, want 422 %s", off, rec.Code, rec.Body, api.CodeEdgeFeatureUnavailable)
		}
		if rules := f.rules(); len(rules) != 0 {
			t.Fatalf("%s off: refused switch left %d rules", off, len(rules))
		}
		if rec, _ := f.call(http.MethodDelete, ""); rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
			t.Fatalf("%s off: off = %d %s", off, rec.Code, rec.Body)
		}
	}
}

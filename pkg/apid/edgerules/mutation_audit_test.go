package edgerules

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestAppBoundTokenCannotEditOrDeleteStoredBroadRule(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "audit-token@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "audit-token", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Host: attackModeHost{store: mem, app: app, audits: &[]string{}, notif: &recordingNotifier{}}}
	rule, prob := svc.CreateRule(ctx, acct, app, api.CreateEdgeRuleRequest{MatchHost: "*", MatchPath: "/**", Kind: "ip", Action: json.RawMessage(`{"deny":["192.0.2.0/24"]}`)})
	if prob != nil {
		t.Fatal(prob)
	}
	token := authmw.WithPrincipal(ctx, acct, &state.APIKey{ID: "deploy", AccountID: acct.ID, AppID: app.ID}, nil)
	disabled := false
	if _, prob := svc.UpdateRule(token, acct, rule.ID, api.UpdateEdgeRuleRequest{Enabled: &disabled}); prob == nil || prob.Status != http.StatusForbidden {
		t.Fatalf("broad rule update was not refused: %v", prob)
	}
	req := httptest.NewRequest(http.MethodDelete, "/", nil).WithContext(token)
	req.SetPathValue("id", rule.ID)
	rec := httptest.NewRecorder()
	svc.DeleteEdgeRule(rec, req, acct)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("broad rule delete: %d %s", rec.Code, rec.Body)
	}
	stored, err := mem.GetEdgeRuleByID(ctx, rule.ID)
	if err != nil || !stored.Enabled {
		t.Fatalf("refused edits changed the rule: %+v %v", stored, err)
	}
	// Account credentials still have authority over the same rule.
	if _, prob := svc.UpdateRule(ctx, acct, rule.ID, api.UpdateEdgeRuleRequest{Enabled: &disabled}); prob != nil {
		t.Fatal(prob)
	}
}

type auditMutationReadStore struct {
	MemStore *state.MemStore
	read     chan struct{}
	once     sync.Once
}

func (s *auditMutationReadStore) GetEdgeRuleByID(ctx context.Context, id string) (state.EdgeRule, error) {
	row, err := s.MemStore.GetEdgeRuleByID(ctx, id)
	s.once.Do(func() { close(s.read) })
	return row, err
}

func TestMutationInvalidatesTheHostReadUnderItsLock(t *testing.T) {
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			mem := state.NewMemStore()
			acct, err := mem.CreateAccount(ctx, "audit-race@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "audit-race", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			rule, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID, MatchHost: "old.example.com", MatchPath: "/**", Enabled: true, Kind: state.EdgeRuleKindIP, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"192.0.2.0/24"}}}})
			if err != nil {
				t.Fatal(err)
			}
			store := &auditMutationReadStore{MemStore: mem, read: make(chan struct{})}
			notif := &payloadNotifier{}
			svc := &Service{Host: rollbackLockHost{attackModeHost: attackModeHost{store: mem, app: app, audits: &[]string{}}, store: statetest.NewMem(store), notif: notif}}
			unlock, err := svc.LockEdgeRuleMutations(ctx, []string{app.ID})
			if err != nil {
				t.Fatal(err)
			}
			defer unlock(ctx)
			req := httptest.NewRequest(method, "/", strings.NewReader(`{"enabled":false}`))
			req.SetPathValue("id", rule.ID)
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				if method == http.MethodPatch {
					svc.UpdateEdgeRule(rec, req, acct)
				} else {
					svc.DeleteEdgeRule(rec, req, acct)
				}
			}()
			select {
			case <-store.read:
			case <-time.After(5 * time.Second):
				t.Fatal("mutation did not read its initial row")
			}
			moved := "current.example.com"
			if _, err := mem.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{MatchHost: &moved}); err != nil {
				t.Fatal(err)
			}
			unlock(ctx)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("mutation did not finish after releasing its lock")
			}
			if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
				t.Fatalf("mutation: %d %s", rec.Code, rec.Body)
			}
			if len(notif.payloads) != 2 {
				t.Fatalf("fleet phases: %+v", notif.payloads)
			}
			for _, phase := range notif.payloads {
				if !slices.Contains(phase.MatchHosts, moved) {
					t.Fatalf("phase omitted the current host: %+v", phase)
				}
			}
		})
	}
}

func TestInvalidGlobsAndSchemasAreRejectedBeforePersistence(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "audit-write@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "audit-write", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Host: attackModeHost{store: mem, app: app, audits: &[]string{}, notif: &recordingNotifier{}}}
	for _, req := range []api.CreateEdgeRuleRequest{
		{MatchHost: "@app", MatchPath: "/admin/[", Kind: "ip", Action: json.RawMessage(`{"deny":["192.0.2.0/24"]}`)},
		{MatchHost: "@app", MatchPath: "/**", Kind: "validate", Action: json.RawMessage(`{"schema":{"type":"integre"}}`)},
	} {
		if _, problem := svc.CreateRule(ctx, acct, app, req); problem == nil || problem.Status != http.StatusBadRequest {
			t.Fatalf("invalid create accepted: %+v: %v", req, problem)
		}
	}
	if rules, err := mem.ListEdgeRulesForApp(ctx, app.ID); err != nil || len(rules) != 0 {
		t.Fatalf("rejected create persisted: %+v %v", rules, err)
	}
	rule, problem := svc.CreateRule(ctx, acct, app, api.CreateEdgeRuleRequest{MatchHost: "@app", MatchPath: "/**", Kind: "validate", Action: json.RawMessage(`{"schema":{"type":"object"}}`)})
	if problem != nil {
		t.Fatal(problem)
	}
	badPath := "/admin/["
	badAction := json.RawMessage(`{"schema":{"type":"integer","minimum":"zero"}}`)
	for _, req := range []api.UpdateEdgeRuleRequest{{MatchPath: &badPath}, {Action: &badAction}} {
		if _, problem := svc.UpdateRule(ctx, acct, rule.ID, req); problem == nil || problem.Status != http.StatusBadRequest {
			t.Fatalf("invalid update accepted: %+v: %v", req, problem)
		}
	}
	stored, err := mem.GetEdgeRuleByID(ctx, rule.ID)
	if err != nil || stored.MatchPath != "/**" || string(stored.Action.Validate.Schema) != `{"type":"object"}` {
		t.Fatalf("rejected update changed stored rule: %+v %v", stored, err)
	}
}

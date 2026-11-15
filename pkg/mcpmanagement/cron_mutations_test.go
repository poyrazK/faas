package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type guardedCronFixture struct {
	store        *state.MemStore
	account      state.Account
	app          state.App
	cron         state.Cron
	backend      *httptest.Server
	mu           sync.Mutex
	lost, legacy bool
	bodies       []string
}

func newGuardedCronFixture(t *testing.T) *guardedCronFixture {
	t.Helper()
	f := &guardedCronFixture{store: state.NewMemStore()}
	var err error
	f.account, err = f.store.CreateAccount(t.Context(), "guarded@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = f.store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "alpha", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	f.cron, err = f.store.CreateCron(t.Context(), f.app.ID, "0 3 * * *", "/private-path", true)
	if err != nil {
		t.Fatal(err)
	}
	base := newLocalFixture(t, true, "")
	base.app.ID = f.app.ID
	f.backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: f.account.ID})
		case r.URL.Path == "/v1/crons/"+f.cron.ID:
			c, err := f.store.CronByID(r.Context(), f.cron.ID)
			if err != nil {
				w.WriteHeader(404)
				return
			}
			token := state.CronReviewToken(c)
			if f.legacy {
				token = ""
			}
			_ = json.NewEncoder(w).Encode(api.CronResponse{ID: c.ID, AppID: c.AppID, Schedule: c.Schedule, Path: c.Path, Timezone: c.Timezone, Enabled: c.Enabled, ReviewToken: token})
		case r.URL.Path == "/v1/apps/alpha/cron-mutations" || r.URL.Path == "/v1/apps/alpha/cron-deletions":
			var request api.CronMutationRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			body, _ := json.Marshal(request)
			f.bodies = append(f.bodies, string(body))
			out, err := f.store.ApplyCronMutation(r.Context(), f.account.ID, f.app.ID, request)
			if err != nil {
				api.WriteProblem(w, api.NewProblem(409, "cron_review_conflict", "refused", "review changed"))
				return
			}
			if f.lost {
				f.lost = false
				api.WriteProblem(w, api.NewProblem(502, "lost_response", "lost", request.ReceiptProof))
				return
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/cron-mutations/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/apps/alpha/cron-mutations/")
			out, err := f.store.CronMutationReceipt(r.Context(), f.account.ID, f.app.ID, id, r.Header.Get(api.CronMutationReceiptProofHeader))
			if err != nil {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			base.serveHTTP(w, r)
		}
	}))
	t.Cleanup(f.backend.Close)
	return f
}

func TestGuardedCronLostResponseRestoresOriginalAttempt(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		t.Run(map[bool]string{false: "update", true: "delete"}[deletion], func(t *testing.T) {
			f := newGuardedCronFixture(t)
			journal, path, key := testStateStore(t)
			opts := Options{APIBase: f.backend.URL, AllowWrites: true, Toolsets: []string{ToolsetAll}, planStore: journal, planNamespace: "guarded-cron"}
			_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			newPath := "/updated"
			args := cronChangeArgs{App: "alpha", CronID: f.cron.ID, Reason: "reviewed recovery", Delete: deletion}
			if !deletion {
				args.Path = &newPath
			}
			value, err := m.planCronChange(t.Context(), args)
			if err != nil {
				t.Fatal(err)
			}
			id := value.(releasePlan).ID
			f.mu.Lock()
			f.lost = true
			f.mu.Unlock()
			if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost response was hidden")
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			opts.planStore = reopened
			_, restored, err := newManagementServer(api.NewClient(f.backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 10; i++ {
				_, err = restored.applyOperation(t.Context(), applyArgs{PlanID: id})
				if err != nil {
					t.Fatal(err)
				}
				if restored.plans[id].localReceipt != nil {
					break
				}
			}
			if restored.plans[id].localReceipt == nil {
				t.Fatal("mutation did not finish")
			}
			f.mu.Lock()
			first := f.bodies[0]
			for _, body := range f.bodies {
				if body != first {
					t.Fatal("recovery replaced reviewed inputs or attempt identity")
				}
			}
			count := len(f.bodies)
			f.mu.Unlock()
			read, err := restored.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			out := read.(*guardedRead).value.(actionOutcome)
			f.mu.Lock()
			gotCount := len(f.bodies)
			f.mu.Unlock()
			if out.CronMutation == nil || out.CronMutation.Status != "completed" || gotCount != count {
				t.Fatalf("passive recovery: %+v", out)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), restored.plans[id].Operation.CronReceiptProof) || strings.Contains(string(encoded), "/private-path") {
				t.Fatal("recovery exposed private data")
			}
		})
	}
}

func TestGuardedCronRefusesOldServerAndLegacyPlans(t *testing.T) {
	f := newGuardedCronFixture(t)
	f.legacy = true
	_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), Options{APIBase: f.backend.URL, AllowWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.planCronChange(t.Context(), cronChangeArgs{App: "alpha", CronID: f.cron.ID, Reason: "old server", Delete: true}); err == nil {
		t.Fatal("old server accepted a new plan")
	}
	p, err := m.operationBase(t.Context(), operationArgs{App: "alpha", Reason: "legacy"}, "cron_delete", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	p.Operation.ResourceID = f.cron.ID
	value, err := m.savePlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: value.(releasePlan).ID}); err == nil {
		t.Fatal("legacy plan performed an unguarded mutation")
	}
	if len(f.bodies) != 0 {
		t.Fatal("compatibility refusal performed a write")
	}
}

func TestGuardedCronPartialReceiptNeverClaimsCompletion(t *testing.T) {
	f := newGuardedCronFixture(t)
	_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), Options{APIBase: f.backend.URL, AllowWrites: true, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := m.planCronChange(t.Context(), cronChangeArgs{App: "alpha", CronID: f.cron.ID, Reason: "bounded deletion", Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	id := value.(releasePlan).ID
	out, err := m.applyOperation(t.Context(), applyArgs{PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	receipt := out.(planReceipt)
	if receipt.Status != "partial" || receipt.Recovery.Action != "retry_same_plan" || receipt.Recovery.Tool != "gregale_apply_operation" {
		t.Fatalf("partial: %+v", receipt)
	}
	changed := "/concurrent"
	if _, err := f.store.UpdateCron(t.Context(), f.cron.ID, nil, &changed, nil, nil); err != nil {
		t.Fatal(err)
	}
	out, err = m.applyOperation(t.Context(), applyArgs{PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	receipt = out.(planReceipt)
	if receipt.Status != "refused" || receipt.Recovery.Action != "inspect" {
		t.Fatalf("conflict: %+v", receipt)
	}
	if _, err := f.store.CronByID(t.Context(), f.cron.ID); err != nil {
		t.Fatal("concurrently edited cron was deleted")
	}
}

func TestGuardedCronProtocolReceiptAndReadOnlyRecovery(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f := newGuardedCronFixture(t)
			journal, _, _ := testStateStore(t)
			opts := Options{APIBase: f.backend.URL, AllowWrites: true, Toolsets: []string{ToolsetProvision}, planStore: journal, planNamespace: "protocol-cron"}
			cs, ctx := connectSelectedTestServer(t, f.backend, opts, protocol)
			planned := callTool(t, ctx, cs, "gregale_plan_cron_change", cronChangeArgs{App: "alpha", CronID: f.cron.ID, Reason: "protocol deletion", Delete: true}, false)
			var id string
			if json.Unmarshal(planned["plan_id"], &id) != nil || id == "" {
				t.Fatalf("plan=%v", planned)
			}
			completed := false
			for i := 0; i < 8; i++ {
				out := callTool(t, ctx, cs, "gregale_apply_operation", applyArgs{PlanID: id}, false)
				var r api.CronMutationReceipt
				_ = json.Unmarshal(out["result"], &r)
				if r.Status == "completed" {
					completed = true
					break
				}
			}
			if !completed {
				t.Fatal("protocol did not finish deletion")
			}
			// A newly connected read-only client can recover the original receipt.
			opts.AllowWrites = false
			reads, readCtx := connectSelectedTestServer(t, f.backend, opts, protocol)
			out := callTool(t, readCtx, reads, "gregale_get_action_outcome", getActionArgs{App: "alpha", PlanID: id}, false)
			var r api.CronMutationReceipt
			if json.Unmarshal(out["cron_mutation"], &r) != nil || r.Status != "completed" {
				t.Fatalf("read-only receipt=%v", out)
			}
		})
	}
}

func TestGuardedCronOutcomeAfterPlanExpiryAndRestart(t *testing.T) {
	f := newGuardedCronFixture(t)
	journal, path, key := testStateStore(t)
	opts := Options{APIBase: f.backend.URL, AllowWrites: true, Toolsets: []string{ToolsetProvision}, planStore: journal, planNamespace: "expired-cron"}
	_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	value, err := m.planCronChange(t.Context(), cronChangeArgs{App: "alpha", CronID: f.cron.ID, Reason: "expiry recovery", Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	id := value.(releasePlan).ID
	m.plans[id].ExpiresAt = time.Now().Add(500 * time.Millisecond)
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(m.plans[id].ExpiresAt) + time.Millisecond)
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	opts.planStore, opts.AllowWrites = reopened, false
	_, restored, err := newManagementServer(api.NewClient(f.backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	read, err := restored.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	out := read.(*guardedRead).value.(actionOutcome)
	if out.CronMutation == nil || out.CronMutation.Status != "accepted" || out.NativeCompletion == "completed" {
		t.Fatalf("expired recovery: %+v", out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != 1 {
		t.Fatal("passive expiry recovery resumed cleanup")
	}
}

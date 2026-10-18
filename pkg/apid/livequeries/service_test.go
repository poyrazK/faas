package livequeries_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/livequeries"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

type databases struct {
	db  managedpostgres.Database
	hub string
}

func (d *databases) Get(_ context.Context, accountID, id string) (managedpostgres.Database, error) {
	if accountID != d.db.AccountID || id != d.db.ID {
		return managedpostgres.Database{}, managedpostgres.ErrNotFound
	}
	return d.db, nil
}

func (d *databases) LiveHub(managedpostgres.Database) (string, error) { return d.hub, nil }

// keys is an in-memory livekeys.Store.
type keys struct {
	sealed map[string]livekeys.NewKey
	list   []livekeys.Key // newest first
}

func (k *keys) Enable(_ context.Context, _, _ string, key livekeys.NewKey) error {
	for _, x := range k.list {
		if x.RetiresAt.IsZero() {
			return livekeys.ErrEnabled
		}
	}
	k.add(key)
	return nil
}

func (k *keys) Rotate(_ context.Context, _, _ string, key livekeys.NewKey, overlap time.Duration) error {
	if len(k.list) == 0 || !k.list[0].RetiresAt.IsZero() {
		return livekeys.ErrDisabled
	}
	prev := k.list[0]
	k.list = nil
	if overlap > 0 {
		prev.RetiresAt = time.Now().Add(overlap)
		k.list = []livekeys.Key{prev}
	}
	k.add(key)
	return nil
}

func (k *keys) Disable(context.Context, string, string) (bool, error) {
	removed := len(k.list) > 0
	k.list = nil
	return removed, nil
}

func (k *keys) Keys(context.Context, string, string) ([]livekeys.Key, error) { return k.list, nil }

func (k *keys) add(key livekeys.NewKey) {
	k.sealed[key.KID] = key
	k.list = append([]livekeys.Key{{KID: key.KID, CreatedAt: time.Now()}}, k.list...)
}

type harness struct {
	svc      *livequeries.Service
	db       *databases
	keys     *keys
	identity *age.X25519Identity
	changed  int
	audits   []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		db: &databases{db: managedpostgres.Database{ID: "db-1", AccountID: "acct", Name: "main", State: managedpostgres.StateReady},
			hub: "wss://live.internal/db-1/live"},
		keys:     &keys{sealed: map[string]livekeys.NewKey{}},
		identity: identity,
	}
	h.svc = &livequeries.Service{
		Databases: h.db, Keys: h.keys, Recipient: identity.Recipient(),
		DatabaseProblem: func(w http.ResponseWriter, err error) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "managed_postgres_not_found", "not found", err.Error()))
		},
		Audit:   func(_ context.Context, action, _ string, _ map[string]any) { h.audits = append(h.audits, action) },
		Changed: func(context.Context, string, string) { h.changed++ },
	}
	return h
}

type response struct {
	status int
	header http.Header
	state  api.ManagedPostgresLiveQueries
	code   string
	limit  *int64
}

func (h *harness) do(t *testing.T, method, path, body string, handler func(http.ResponseWriter, *http.Request, state.Account)) response {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetPathValue("id", "db-1")
	w := httptest.NewRecorder()
	handler(w, r, state.Account{ID: "acct"})
	out := response{status: w.Code, header: w.Header()}
	if w.Code >= 400 {
		var p struct {
			Code  string `json:"code"`
			Limit *int64 `json:"limit"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatalf("problem body %q: %v", w.Body.String(), err)
		}
		out.code, out.limit = p.Code, p.Limit
	} else if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out.state); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// opens checks a returned secret is exactly what livequeryd will unseal.
func (h *harness) opens(t *testing.T, secret string) {
	t.Helper()
	parsed, err := seal.ParseSecret(secret)
	if err != nil {
		t.Fatalf("secret %q: %v", secret, err)
	}
	stored, ok := h.keys.sealed[parsed.KID]
	if !ok || stored.Recipient != h.identity.Recipient().String() {
		t.Fatalf("no key %s sealed to livequeryd's recipient", parsed.KID)
	}
	namespace, plain, err := secretbox.OpenBytes(h.identity, stored.Sealed)
	if err != nil || namespace != livekeys.SealNamespace || string(plain) != secret {
		t.Fatalf("sealed key opens to %q/%q (%v), want the issued secret", namespace, plain, err)
	}
}

func TestLiveQueriesLifecycle(t *testing.T) {
	h := newHarness(t)
	const path = "/v1/postgres/databases/db-1/live"

	if r := h.do(t, "GET", path, "", h.svc.Get); r.status != 200 || r.state.Enabled || len(r.state.Keys) != 0 {
		t.Fatalf("get before enable = %+v", r)
	}
	if r := h.do(t, "POST", path+"/rotate", "", h.svc.Rotate); r.status != 409 || r.code != "live_queries_not_enabled" {
		t.Fatalf("rotate before enable = %+v", r)
	}

	enabled := h.do(t, "POST", path, "", h.svc.Enable)
	if enabled.status != 201 || !enabled.state.Enabled || enabled.state.Secret == "" || len(enabled.state.Keys) != 1 || !enabled.state.Keys[0].Active {
		t.Fatalf("enable = %+v", enabled)
	}
	if enabled.header.Get("Cache-Control") != "no-store" {
		t.Fatal("a response carrying a secret is cacheable")
	}
	h.opens(t, enabled.state.Secret)
	if r := h.do(t, "POST", path, "", h.svc.Enable); r.status != 409 || r.code != "live_queries_enabled" {
		t.Fatalf("second enable = %+v", r)
	}
	if r := h.do(t, "GET", path, "", h.svc.Get); r.state.Secret != "" || !r.state.Enabled {
		t.Fatalf("get after enable = %+v; a secret is shown only once", r)
	}

	if r := h.do(t, "POST", path+"/rotate", `{"overlap_seconds": 700000}`, h.svc.Rotate); r.status != 400 || r.code != "live_queries_invalid" || r.limit == nil {
		t.Fatalf("rotate past the overlap limit = %+v", r)
	}
	rotated := h.do(t, "POST", path+"/rotate", `{"overlap_seconds": 3600}`, h.svc.Rotate)
	if rotated.status != 200 || rotated.state.Secret == enabled.state.Secret || len(rotated.state.Keys) != 2 ||
		!rotated.state.Keys[0].Active || rotated.state.Keys[1].Active || rotated.state.Keys[1].RetiresAt == "" {
		t.Fatalf("rotate = %+v", rotated)
	}
	h.opens(t, rotated.state.Secret)
	if r := h.do(t, "POST", path+"/rotate", `{"overlap_seconds": 0}`, h.svc.Rotate); r.status != 200 || len(r.state.Keys) != 1 {
		t.Fatalf("rotate without overlap = %+v", r)
	}

	if r := h.do(t, "DELETE", path, "", h.svc.Disable); r.status != 204 {
		t.Fatalf("disable = %+v", r)
	}
	if r := h.do(t, "DELETE", path, "", h.svc.Disable); r.status != 204 {
		t.Fatalf("second disable = %+v", r)
	}
	if r := h.do(t, "GET", path, "", h.svc.Get); r.state.Enabled {
		t.Fatalf("get after disable = %+v", r)
	}
	if want := "managed_postgres.live.enabled,managed_postgres.live.rotated,managed_postgres.live.rotated,managed_postgres.live.disabled"; strings.Join(h.audits, ",") != want {
		t.Fatalf("audits = %v", h.audits)
	}
	if h.changed != 4 {
		t.Fatalf("Changed ran %d times, want 4", h.changed)
	}
}

func TestLiveQueriesRefusals(t *testing.T) {
	const path = "/v1/postgres/databases/db-1/live"
	for name, tc := range map[string]struct {
		setup func(*harness)
		code  string
	}{
		"no hub":        {func(h *harness) { h.db.hub = "" }, "live_queries_unsupported"},
		"not ready":     {func(h *harness) { h.db.db.State = managedpostgres.StateProvisioning }, "live_queries_database_not_ready"},
		"no recipient":  {func(h *harness) { h.svc.Recipient = nil }, "live_queries_unavailable"},
		"no store":      {func(h *harness) { h.svc.Keys = nil }, "live_queries_unavailable"},
		"other account": {func(h *harness) { h.db.db.AccountID = "someone-else" }, "managed_postgres_not_found"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(h)
			if r := h.do(t, "POST", path, "", h.svc.Enable); r.code != tc.code {
				t.Fatalf("enable = %+v, want %s", r, tc.code)
			}
			if len(h.keys.list) != 0 || h.changed != 0 {
				t.Fatal("a refused enable stored a key")
			}
		})
	}
	var nilService *livequeries.Service
	h := newHarness(t)
	if r := h.do(t, "GET", path, "", nilService.Get); r.code != "live_queries_unavailable" {
		t.Fatalf("unconfigured get = %+v", r)
	}
}

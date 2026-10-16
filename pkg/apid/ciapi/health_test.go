package ciapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCIHealthAPIReadWithComputeAndPublicationOff(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := ci.NewPostgresStore(pool)
	account, other := uuid.NewString(), uuid.NewString()
	for _, id := range []string{account, other} {
		if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan) VALUES ($1,$2,'pro')", id, id+"@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	def := ci.Definition{Version: 1, Repository: "private/health", Jobs: map[string]ci.Job{"test": {Image: "registry.example/ci@sha256:" + strings.Repeat("a", 64), Steps: []ci.Step{{Name: "unit", Run: "secret command text"}}}}}
	if _, err := store.PutRevision(t.Context(), account, "private", def); err != nil {
		t.Fatal(err)
	}
	cfg := ci.ConfigFromEnv(func(key string) string {
		if key == ci.InternalEnv {
			return "1"
		}
		if key == ci.AccountsEnv {
			return account + "," + other
		}
		return ""
	})
	h := ciapi.Handler{Config: cfg, Store: store, VerifyGitHub: func(context.Context, string, int64, int64, string) error { t.Fatal("read called GitHub"); return nil }}
	call := func(owner string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.Health(w, httptest.NewRequest(http.MethodGet, "/v1/internal/ci/health", nil), state.Account{ID: owner})
		return w
	}
	w := call(account)
	if w.Code != http.StatusOK {
		t.Fatalf("health with compute off: %d %s", w.Code, w.Body)
	}
	var out struct {
		Health ci.Health       `json:"health"`
		Gates  map[string]bool `json:"gates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Gates["internal"] || out.Gates["worker"] || out.Gates["github_checks"] || out.Gates["execution_dependencies_ready"] || len(out.Health.Workers) != 6 {
		t.Fatal("health invented enabled compute or missing workers")
	}
	for _, q := range out.Health.Quotas {
		if q.Resource == "revisions" && q.Used != 1 {
			t.Fatal("account usage omitted")
		}
	}
	for _, private := range []string{account, "private/health", "secret command text", "registry.example", "installation_id"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("health exposed private metadata")
		}
	}
	w = call(other)
	if w.Code != http.StatusOK {
		t.Fatal("other account diagnostics unavailable")
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, q := range out.Health.Quotas {
		if q.Used != 0 {
			t.Fatal("foreign account usage leaked")
		}
	}
	if w := call(uuid.NewString()); w.Code != http.StatusNotFound {
		t.Fatal("unlisted account reached health")
	}
	h.Config = ci.Config{}
	if w := call(account); w.Code != http.StatusNotFound {
		t.Fatal("flag-off health exposed")
	}
	h.Config = cfg
	h.Store = ci.NewMemoryStore()
	if w := call(account); w.Code != http.StatusNotFound {
		t.Fatal("missing durable health replaced with healthy zeros")
	}
	h.Store = &unavailableHealth{MemoryStore: ci.NewMemoryStore()}
	w = call(account)
	if w.Code < 500 || strings.Contains(w.Body.String(), "sensitive-provider-url") {
		t.Fatal("failed health query leaked error or returned success")
	}
}

type unavailableHealth struct{ *ci.MemoryStore }

func (*unavailableHealth) CIHealth(context.Context, string) (ci.Health, error) {
	return ci.Health{}, errors.New("sensitive-provider-url")
}

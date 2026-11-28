package staticegress_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/staticegress"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type notes struct{ channels, payloads []string }

func (n *notes) Notify(_ context.Context, channel, payload string) error {
	n.channels, n.payloads = append(n.channels, channel), append(n.payloads, payload)
	return nil
}

type audit struct{ kinds []string }

func (a *audit) Emit(_ context.Context, kind string, _ *string, _ map[string]any) {
	a.kinds = append(a.kinds, kind)
}

type fixture struct {
	mux     *http.ServeMux
	st      *state.MemStore
	notes   *notes
	audit   *audit
	node    state.ComputeNode
	account state.Account
}

func newFixture(t *testing.T, operator bool) *fixture {
	t.Helper()
	st := state.NewMemStore()
	node, err := st.CreateComputeNode(t.Context(), state.ComputeNode{Name: "bhs-2", TargetURL: "tcp://127.0.0.1:1", AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	acct, err := st.CreateAccount(t.Context(), "pin@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{mux: http.NewServeMux(), st: st, notes: &notes{}, audit: &audit{}, node: node, account: acct}
	ops := staticegress.Operations{
		AdminAllows: func(state.Account) (bool, *api.Problem) {
			if !operator {
				return false, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Forbidden", "operators only")
			}
			return true, nil
		},
		Store: st, Notifier: f.notes, Audit: f.audit,
	}
	pass := func(h http.Handler) http.Handler { return h }
	auth := func(h httpkit.AccountHandler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { h(w, r, state.Account{ID: "operator"}) }
	}
	same := func(h httpkit.AccountHandler) httpkit.AccountHandler { return h }
	ops.Register(f.mux, pass, auth, same, same)
	return f
}

func (f *fixture) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestInventoryLifecycle(t *testing.T) {
	f := newFixture(t, true)
	body := `{"node":"bhs-2","account_id":"` + f.account.ID + `","note":"ovh 1"}`
	if rec := f.do(t, http.MethodPut, "/v1/admin/ops/static-egress-ips/203.0.113.42", body); rec.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, http.MethodPut, "/v1/admin/ops/static-egress-ips/203.0.113.42", body); rec.Code != http.StatusOK {
		t.Fatalf("replace = %d %s", rec.Code, rec.Body)
	}
	if len(f.notes.channels) != 2 || f.notes.channels[0] != db.NotifyStaticEgressAddresses {
		t.Fatalf("notifications = %v", f.notes.channels)
	}
	if len(f.audit.kinds) != 2 || f.audit.kinds[0] != "admin.static_egress_address_put" {
		t.Fatalf("audit = %v", f.audit.kinds)
	}
	rec := f.do(t, http.MethodGet, "/v1/admin/ops/static-egress-ips", "")
	var list api.ObsStaticEgressAddressList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Addresses) != 1 {
		t.Fatalf("list = %d %s (%v)", rec.Code, rec.Body, err)
	}
	if a := list.Addresses[0]; a.IP != "203.0.113.42" || a.Node != "bhs-2" || a.AccountID != f.account.ID || a.Note != "ovh 1" {
		t.Fatalf("listed = %+v", a)
	}

	// A live pin blocks removal; once cleared the address can go.
	app, err := f.st.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "pinned", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := f.st.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{StaticEgressIP: &ip, SetStaticEgressIP: true}); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(t, http.MethodDelete, "/v1/admin/ops/static-egress-ips/203.0.113.42?confirm=true", ""); rec.Code != http.StatusConflict {
		t.Fatalf("remove pinned = %d %s", rec.Code, rec.Body)
	}
	if _, err := f.st.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{StaticEgressIP: nil, SetStaticEgressIP: true}); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(t, http.MethodDelete, "/v1/admin/ops/static-egress-ips/203.0.113.42", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("remove without confirm = %d", rec.Code)
	}
	if rec := f.do(t, http.MethodDelete, "/v1/admin/ops/static-egress-ips/203.0.113.42?confirm=true", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, http.MethodDelete, "/v1/admin/ops/static-egress-ips/203.0.113.42?confirm=true", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("remove twice = %d", rec.Code)
	}
}

func TestInventoryRefusals(t *testing.T) {
	f := newFixture(t, true)
	for _, tc := range []struct {
		name, path, body string
		code             int
	}{
		{"private address", "/v1/admin/ops/static-egress-ips/10.0.0.5", `{"node":"bhs-2"}`, http.StatusBadRequest},
		{"ipv6", "/v1/admin/ops/static-egress-ips/2001:db8::1", `{"node":"bhs-2"}`, http.StatusBadRequest},
		{"no node", "/v1/admin/ops/static-egress-ips/203.0.113.43", `{}`, http.StatusBadRequest},
		{"unknown node", "/v1/admin/ops/static-egress-ips/203.0.113.43", `{"node":"nope"}`, http.StatusNotFound},
		{"bad account", "/v1/admin/ops/static-egress-ips/203.0.113.43", `{"node":"bhs-2","account_id":"x"}`, http.StatusBadRequest},
		{"unknown account", "/v1/admin/ops/static-egress-ips/203.0.113.43", `{"node":"bhs-2","account_id":"11111111-1111-4111-8111-111111111111"}`, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := f.do(t, http.MethodPut, tc.path, tc.body); rec.Code != tc.code {
				t.Fatalf("code = %d, want %d: %s", rec.Code, tc.code, rec.Body)
			}
		})
	}
	if len(f.notes.channels) != 0 {
		t.Fatalf("a refused change notified vmmd: %v", f.notes.channels)
	}
}

func TestInventoryNeedsAnOperator(t *testing.T) {
	f := newFixture(t, false)
	if rec := f.do(t, http.MethodGet, "/v1/admin/ops/static-egress-ips", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("customer list = %d", rec.Code)
	}
	if rec := f.do(t, http.MethodPut, "/v1/admin/ops/static-egress-ips/203.0.113.42", `{"node":"bhs-2"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("customer put = %d", rec.Code)
	}
}

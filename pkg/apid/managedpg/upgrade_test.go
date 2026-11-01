// adr: 1027 — a customer's major upgrade is journaled through the service
// and reported by request UUID; a failed one reports why.
package managedpg

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeUpgrades struct {
	got       managedpostgres.UpgradeMajorRequest
	operation managedpostgres.ResizeOperation
	err       error
}

func (f *fakeUpgrades) UpgradeMajor(_ context.Context, r managedpostgres.UpgradeMajorRequest) (managedpostgres.ResizeOperation, error) {
	f.got = r
	return f.operation, f.err
}

func (f *fakeUpgrades) GetMajorUpgrade(context.Context, string, string, string) (managedpostgres.ResizeOperation, error) {
	return f.operation, f.err
}

func TestUpgradesJournalAndReport(t *testing.T) {
	const id = "44444444-4444-4444-8444-444444444444"
	created := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	service := &fakeUpgrades{operation: managedpostgres.ResizeOperation{ID: id, DatabaseID: "orders", SourceSpec: managedpostgres.Spec{PostgresMajor: 14},
		TargetPostgresMajor: 17, Generation: 3, State: managedpostgres.ResizePending, CreatedAt: created}}
	var problems []error
	audit := &fakeAudit{}
	u := Upgrades{Service: service, Problem: func(w http.ResponseWriter, err error) {
		problems = append(problems, err)
		w.WriteHeader(http.StatusTeapot)
	},
		NotConfigured: func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }, Audit: audit}
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/postgres/databases/orders/upgrade", strings.NewReader(body))
		r.SetPathValue("id", "orders")
		w := httptest.NewRecorder()
		u.Post(w, r, state.Account{ID: "acct-1"})
		return w
	}
	w := post(`{"request_id":"` + id + `","postgres_major":17}`)
	var view api.ManagedPostgresMajorUpgrade
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.FromPostgresMajor != 14 || view.TargetPostgresMajor != 17 ||
		view.State != "pending" || !view.ConnectionInterruptionExpected || w.Header().Get("Location") != "/v1/postgres/databases/orders/upgrades/"+id {
		t.Fatalf("post = %d %s %+v", w.Code, w.Body.String(), view)
	}
	if service.got != (managedpostgres.UpgradeMajorRequest{AccountID: "acct-1", DatabaseID: "orders", RequestID: id, PostgresMajor: 17}) || len(audit.kinds) != 1 || audit.kinds[0] != "managed_postgres.database.upgrade_requested" {
		t.Fatalf("service got %+v, audit %v", service.got, audit.kinds)
	}
	for _, body := range []string{`{"request_id":"` + id + `"}`, `not json`, `{"request_id":"` + id + `","postgres_major":-1}`} {
		if w := post(body); w.Code != http.StatusTeapot || !errors.Is(problems[len(problems)-1], managedpostgres.ErrInvalid) {
			t.Errorf("%s = %d", body, w.Code)
		}
	}

	service.operation.State, service.operation.ErrorCode, service.operation.CompletedAt = managedpostgres.ResizeFailed, "upgrade_refused", created.Add(time.Minute)
	r := httptest.NewRequest(http.MethodGet, "/v1/postgres/databases/orders/upgrades/"+id, nil)
	r.SetPathValue("id", "orders")
	r.SetPathValue("upgrade_id", id)
	w = httptest.NewRecorder()
	u.Get(w, r, state.Account{ID: "acct-1"})
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.State != "failed" || view.LastErrorCode != "upgrade_refused" || view.CompletedAt == "" {
		t.Fatalf("get = %d %+v", w.Code, view)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("progress cached")
	}
	unconfigured := Upgrades{NotConfigured: u.NotConfigured, Problem: u.Problem}
	w = httptest.NewRecorder()
	unconfigured.Get(w, r, state.Account{ID: "acct-1"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured = %d", w.Code)
	}
}

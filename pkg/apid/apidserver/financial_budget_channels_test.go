package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 20261019193500 — a budget's alert channels are validated like alert
// rule channels, replaced as a set, and unavailable once the budget is gone.
func TestFinancialBudgetChannelsAPI(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanHobby)
	spec := financialDraftSpec()
	spec.Enabled, spec.Action, spec.DrainSeconds = true, "notify", 0
	p := decodeFinancialBudget(t, e.Do(t, "POST", "/v1/billing/budgets", api.CreateFinancialBudgetRequest{Spec: spec}, map[string]string{"Idempotency-Key": "channels-budget"}), http.StatusCreated)
	path := "/v1/billing/budgets/" + p.ID + "/channels"
	ops, finance := createTestChannel(t, e, "ops"), createTestChannel(t, e, "finance")
	put := func(key string, ids ...string) *httptest.ResponseRecorder {
		if ids == nil {
			ids = []string{}
		}
		return e.Do(t, http.MethodPut, path, api.SetFinancialBudgetChannelsRequest{ChannelIDs: ids}, map[string]string{"Idempotency-Key": key})
	}
	decode := func(rec *httptest.ResponseRecorder) api.FinancialBudgetChannelsResponse {
		t.Helper()
		var out api.FinancialBudgetChannelsResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("channels response: %d %s", rec.Code, rec.Body)
		}
		return out
	}
	if got := decode(e.Do(t, http.MethodGet, path, nil, nil)); got.PolicyID != p.ID || len(got.Channels) != 0 {
		t.Fatalf("initial channels: %+v", got)
	}
	got := decode(put("bind", ops, finance))
	if len(got.Channels) != 2 || got.Channels[0].Name != "finance" || got.Channels[1].Name != "ops" || got.Channels[0].Kind != "slack" {
		t.Fatalf("bound channels: %+v", got)
	}
	if got := decode(e.Do(t, http.MethodGet, path, nil, nil)); len(got.Channels) != 2 {
		t.Fatalf("read back: %+v", got)
	}
	read := decodeFinancialBudget(t, e.Do(t, "GET", "/v1/billing/budgets/"+p.ID, nil, nil), http.StatusOK)
	if read.Revision != p.Revision {
		t.Fatalf("binding channels changed the policy revision: %d -> %d", p.Revision, read.Revision)
	}

	for name, tc := range map[string]struct {
		ids  []string
		want string
	}{
		"duplicate": {[]string{ops, ops}, "twice"},
		"unknown":   {[]string{uuid.NewString()}, "not one of this account's notification channels"},
		"too many":  {[]string{ops, finance, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}, "at most 5 channels"},
	} {
		if rec := put("invalid-"+name, tc.ids...); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := e.Do(t, http.MethodPut, path, api.SetFinancialBudgetChannelsRequest{ChannelIDs: []string{ops}}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key: %d %s", rec.Code, rec.Body)
	}
	if got := decode(put("clear")); len(got.Channels) != 0 {
		t.Fatalf("cleared channels: %+v", got)
	}

	decodeFinancialBudget(t, e.Do(t, "DELETE", "/v1/billing/budgets/"+p.ID, api.DeleteFinancialBudgetRequest{ExpectedRevision: p.Revision}, map[string]string{"Idempotency-Key": "channels-delete"}), http.StatusOK)
	if rec := e.Do(t, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted budget channels: %d %s", rec.Code, rec.Body)
	}
	if rec := put("after-delete", ops); rec.Code != http.StatusNotFound {
		t.Fatalf("bound a deleted budget: %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/billing/budgets/not-a-uuid/channels", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid budget id: %d %s", rec.Code, rec.Body)
	}
}

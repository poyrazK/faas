package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func withConsumersTestAPI(t *testing.T, handler http.HandlerFunc) *bytes.Buffer {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := osStdout, jsonOutput
	var stdout bytes.Buffer
	osStdout, jsonOutput = &stdout, false
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	return &stdout
}

// adr: 843 — the CLI drives the consumer monetization lifecycle end to end.
func TestCmdConsumersMonetizationLifecycle(t *testing.T) {
	var key api.CreateConsumerKeyRequest
	var card api.CreateAPIConsumerRateCardRequest
	var period api.CreateAPIConsumerUsageStatementRequest
	var claim api.ClaimAPIConsumerUsageStatementRequest
	base := "/v1/apps/my-api/consumers/c1"
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST " + base + "/keys":
			_ = json.NewDecoder(r.Body).Decode(&key)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.ConsumerKeyResponse{ID: "k1", Name: key.Name, Scopes: key.Scopes, Key: "ck_secret_once"})
		case "POST /v1/apps/my-api/rate-cards":
			_ = json.NewDecoder(r.Body).Decode(&card)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.APIConsumerRateCardResponse{ID: "rc1", Currency: card.Currency, PriceMillicentsPerUnit: card.PriceMillicentsPerUnit})
		case "POST " + base + "/usage-statements":
			_ = json.NewDecoder(r.Body).Decode(&period)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.APIConsumerUsageStatementResponse{ID: "s3", Revision: 3, Status: "draft", Currency: "EUR", BillableUnits: 4, AmountMillicents: 100})
		case "POST " + base + "/usage-statements/s3/handoff":
			_ = json.NewDecoder(r.Body).Decode(&claim)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.APIConsumerUsageStatementHandoffResponse{StatementID: "s3", ExternalInvoiceID: claim.ExternalInvoiceID, Currency: "EUR", AmountMillicents: 100})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	if code := cmdConsumers([]string{"key-create", "my-api", "c1", "--name", "prod", "--scopes", "read,write"}); code != 0 {
		t.Fatalf("key-create exit = %d", code)
	}
	if key.Name != "prod" || strings.Join(key.Scopes, ",") != "read,write" || !strings.Contains(stdout.String(), "ck_secret_once") {
		t.Fatalf("key-create body=%+v output=%q", key, stdout.String())
	}
	if code := cmdConsumers([]string{"rate-card-create", "my-api", "--currency", "eur", "--price-millicents", "25", "--included-units", "10000"}); code != 0 {
		t.Fatalf("rate-card-create exit = %d", code)
	}
	if card.Currency != "EUR" || card.PriceMillicentsPerUnit != 25 || card.IncludedUnitsPerMonth != 10000 || !strings.Contains(stdout.String(), "EUR 0.00025") {
		t.Fatalf("rate card body=%+v output=%q", card, stdout.String())
	}
	if code := cmdConsumers([]string{"statement-draft", "my-api", "c1", "--month", "2026-09"}); code != 0 {
		t.Fatalf("statement-draft exit = %d", code)
	}
	wantStart, wantEnd := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if period.PeriodStart == nil || !period.PeriodStart.Equal(wantStart) || period.PeriodEnd == nil || !period.PeriodEnd.Equal(wantEnd) {
		t.Fatalf("statement period = %+v", period)
	}
	if !strings.Contains(stdout.String(), "r3 draft") || !strings.Contains(stdout.String(), "only units not covered") {
		t.Fatalf("adjustment revision not explained: %q", stdout.String())
	}
	if code := cmdConsumers([]string{"statement-handoff", "my-api", "c1", "s3", "--invoice-id", "INV-1001"}); code != 0 || claim.ExternalInvoiceID != "INV-1001" {
		t.Fatalf("handoff exit=%d body=%+v", code, claim)
	}
}

func TestCmdConsumersRejectsBadArgumentsBeforeCallingAPI(t *testing.T) {
	withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call %s %s", r.Method, r.URL.Path)
	})
	for name, args := range map[string][]string{
		"unknown verb":            {"bill", "my-api"},
		"missing consumer":        {"statements", "my-api"},
		"misplaced flag":          {"list", "my-api", "--month", "2026-09"},
		"missing period":          {"statement-draft", "my-api", "c1"},
		"month and period":        {"statement-draft", "my-api", "c1", "--month", "2026-09", "--period-start", "2026-09-01T00:00:00Z"},
		"sub-minute period":       {"statement-draft", "my-api", "c1", "--period-start", "2026-09-01T00:00:30Z", "--period-end", "2026-09-02T00:00:00Z"},
		"unknown scope":           {"key-create", "my-api", "c1", "--name", "k", "--scopes", "root"},
		"missing price":           {"rate-card-create", "my-api", "--currency", "EUR"},
		"negative allowance":      {"rate-card-create", "my-api", "--currency", "EUR", "--price-millicents", "1", "--included-units", "-1"},
		"tenant allowance":        {"rate-card-create", "--id", "t1", "--currency", "EUR", "--price-millicents", "1", "--included-units", "5"},
		"bad currency":            {"rate-card-create", "my-api", "--currency", "EURO", "--price-millicents", "1"},
		"missing invoice":         {"statement-handoff", "my-api", "c1", "s1"},
		"tenant without id":       {"rate-cards"},
		"tenant without stmt id":  {"statement-show", "--id", "t1"},
		"tenant misplaced flag":   {"rate-cards", "--id", "t1", "--month", "2026-09"},
		"tenant handoff no input": {"statement-handoff", "--id", "t1", "--statement-id", "s1"},
	} {
		var code int
		if strings.HasPrefix(name, "tenant") {
			code = cmdPlatformTenants(args)
		} else {
			code = cmdConsumers(args)
		}
		if code == 0 {
			t.Errorf("%s: accepted %v", name, args)
		}
	}
}

func TestFormatMillicents(t *testing.T) {
	for _, tc := range []struct {
		currency   string
		millicents int64
		want       string
	}{
		{"EUR", 0, "EUR 0.00"},
		{"EUR", 25, "EUR 0.00025"},
		{"EUR", 1000, "EUR 0.01"},
		{"EUR", 123456, "EUR 1.23456"},
		{"USD", 1_000_000, "USD 10.00"},
		{"", 50, "--- 0.0005"},
		{"EUR", -1500, "EUR -0.015"},
	} {
		if got := formatMillicents(tc.currency, tc.millicents); got != tc.want {
			t.Errorf("formatMillicents(%q, %d) = %q, want %q", tc.currency, tc.millicents, got, tc.want)
		}
	}
}

func TestCmdPlatformTenantStatementVerbs(t *testing.T) {
	var period api.CreateAPIConsumerUsageStatementRequest
	var listQuery string
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/account/platform-tenants/t1/usage-statements":
			_ = json.NewDecoder(r.Body).Decode(&period)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.PlatformTenantStatementResponse{ID: "ts1", Revision: 1, Status: "draft", UnpricedUnits: 2})
		case "GET /v1/account/platform-tenants/t1/usage-statements":
			listQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode(api.PlatformTenantStatementListResponse{Statements: []api.PlatformTenantStatementResponse{{ID: "ts1", Revision: 1, Status: "superseded"}}})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	if code := cmdPlatformTenants([]string{"statement-draft", "--id", "t1", "--period-start", "2026-09-01T00:00:00Z", "--period-end", "2026-09-02T00:00:00Z"}); code != 0 {
		t.Fatalf("statement-draft exit = %d", code)
	}
	if period.PeriodEnd == nil || !period.PeriodEnd.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) || !strings.Contains(stdout.String(), "Unpriced units") {
		t.Fatalf("period=%+v output=%q", period, stdout.String())
	}
	if code := cmdPlatformTenants([]string{"statements", "--id", "t1", "--month", "2026-09"}); code != 0 {
		t.Fatalf("statements exit = %d", code)
	}
	if !strings.Contains(listQuery, "period_start=2026-09-01") || !strings.Contains(stdout.String(), "r1 superseded") {
		t.Fatalf("list query=%q output=%q", listQuery, stdout.String())
	}
}

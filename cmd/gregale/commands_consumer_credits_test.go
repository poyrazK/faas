package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1306 — credit-grant and spend-cap-set send the flags as requests, a
// zero cap is sent explicitly, and missing amounts never reach the API.
func TestCmdConsumerCreditsAndSpendCap(t *testing.T) {
	var grant api.CreateAPIConsumerCreditRequest
	var spendCap api.PutAPIConsumerSpendCapRequest
	due := int64(125000)
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/apps/my-api/consumers/c1/credits":
			_ = json.NewDecoder(r.Body).Decode(&grant)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.APIConsumerCreditResponse{ID: "cr1", Currency: grant.Currency, AmountMillicents: grant.AmountMillicents,
				RemainingMillicents: grant.AmountMillicents, Status: "active"})
		case "PUT /v1/apps/my-api/consumers/c1/spend-cap":
			_ = json.NewDecoder(r.Body).Decode(&spendCap)
			checked := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
			_ = json.NewEncoder(w).Encode(api.APIConsumerSpendCapResponse{ConsumerID: "c1", Currency: spendCap.Currency,
				MonthlyCapMillicents: *spendCap.MonthlyCapMillicents, Capped: true, Month: "2026-10", AmountDueMillicents: &due, CheckedAt: &checked})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	if code := cmdConsumers([]string{"credit-grant", "my-api", "c1", "--currency", "eur", "--amount-millicents", "5000000", "--note", "Starter"}); code != 0 {
		t.Fatalf("credit-grant exit %d", code)
	}
	if grant.Currency != "EUR" || grant.AmountMillicents != 5000000 || grant.Note != "Starter" || !strings.Contains(stdout.String(), "EUR 50.00") {
		t.Fatalf("grant = %+v, out = %s", grant, stdout)
	}
	if code := cmdConsumers([]string{"spend-cap-set", "my-api", "c1", "--currency", "EUR", "--monthly-cap-millicents", "0"}); code != 0 {
		t.Fatalf("spend-cap-set exit %d", code)
	}
	if spendCap.MonthlyCapMillicents == nil || *spendCap.MonthlyCapMillicents != 0 || !strings.Contains(stdout.String(), "reached") {
		t.Fatalf("spend cap = %+v, out = %s", spendCap, stdout)
	}
	if code := cmdConsumers([]string{"credit-grant", "my-api", "c1", "--currency", "EUR"}); code == 0 {
		t.Fatal("credit-grant without an amount succeeded")
	}
	if code := cmdConsumers([]string{"spend-cap-set", "my-api", "c1", "--currency", "EUR"}); code == 0 {
		t.Fatal("spend-cap-set without a cap succeeded")
	}
}

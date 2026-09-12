package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestUpdateAccountBillingInfo_NormalizesAndAuditsFieldNames(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "billing-handler@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, nil, nil, 15*24, "")
	body, _ := json.Marshal(api.UpdateAccountBillingInfoRequest{
		BusinessName:   stringPtr("  Acme GmbH  "),
		BillingAddress: stringPtr("  Hauptstrasse 1, Berlin  "),
		TaxID:          stringPtr("DE123456789"),
	})
	req := httptest.NewRequest(http.MethodPatch, "/v1/account/billing", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.updateAccountBillingInfo(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out api.AccountResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.BusinessName != "Acme GmbH" || out.BillingAddress != "Hauptstrasse 1, Berlin" || out.TaxID != "DE123456789" {
		t.Fatalf("response billing identity = %+v", out)
	}

	// Audit payloads must contain the changed field names only; the values
	// are legal identity data and must never be emitted to the audit stream.
	updated, err := store.AccountByID(context.Background(), acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(updated.BusinessName, "  ") {
		t.Fatalf("business name was not normalized: %q", updated.BusinessName)
	}
	rows, err := store.ListEvents(context.Background(), acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := findEventByKind(rows, "account.billing_info_updated")
	if found == nil {
		t.Fatalf("billing identity audit event missing; rows = %+v", rows)
	}
	if strings.Contains(string(found.Data), "Acme") || strings.Contains(string(found.Data), "DE123") || strings.Contains(string(found.Data), "Hauptstrasse") {
		t.Fatalf("billing identity audit event leaked submitted values: %s", found.Data)
	}
}

func TestUpdateAccountBillingInfo_HTTPRoute(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.doAdmin(t, http.MethodPatch, "/v1/account/billing", map[string]string{
		"business_name": "Acme GmbH",
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	updated, err := e.store.AccountByID(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.BusinessName != "Acme GmbH" {
		t.Fatalf("stored business name = %q", updated.BusinessName)
	}
}

func TestNormalizeBillingField(t *testing.T) {
	if got, ok := normalizeBillingField("  Acme  ", 10); !ok || got != "Acme" {
		t.Fatalf("normalizeBillingField = %q, %v", got, ok)
	}
	if _, ok := normalizeBillingField(strings.Repeat("x", 11), 10); ok {
		t.Fatal("oversized billing value accepted")
	}
	if got, ok := normalizeBillingField("   ", 10); !ok || got != "" {
		t.Fatalf("empty billing value = %q, %v", got, ok)
	}
}

func stringPtr(v string) *string { return &v }

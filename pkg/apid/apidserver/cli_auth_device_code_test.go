package apidserver_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The 8-hex-character code a user approves was the only secret the exchange
// needed, so guessing a pending code won the user's 30-day admin key (bug
// hunt 2026-10-28 H67). A CLI that asks for a device code can now exchange
// only with it; the approved short code alone yields nothing.
func TestCliAuthExchangeNeedsTheDeviceCode(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "device-code@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	h := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := post("/v1/cli-auth/code", api.CliAuthCodeRequest{Device: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body)
	}
	var minted api.CliAuthCodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	if len(minted.DeviceCode) != 64 {
		t.Fatalf("device code = %q, want 64 hex characters", minted.DeviceCode)
	}
	short, err := hex.DecodeString(strings.ReplaceAll(minted.Code, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimCliAuthCode(ctx, api.HashToken(short), acct.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	if rec := post("/v1/cli-auth/exchange", api.CliAuthExchangeRequest{Code: minted.Code}); rec.Code != http.StatusGone {
		t.Fatalf("exchange with the approved short code: %d %s, want 410 and no key", rec.Code, rec.Body)
	}
	rec = post("/v1/cli-auth/exchange", api.CliAuthExchangeRequest{DeviceCode: minted.DeviceCode})
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange with the device code: %d %s", rec.Code, rec.Body)
	}
	var out api.CliAuthExchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Plaintext == "" || out.Account.ID != acct.ID {
		t.Fatalf("exchange response = %+v, %v", out, err)
	}
	if rec := post("/v1/cli-auth/exchange", api.CliAuthExchangeRequest{DeviceCode: minted.DeviceCode}); rec.Code != http.StatusGone {
		t.Fatalf("second exchange: %d, want 410", rec.Code)
	}

	// A CLI that predates device codes keeps the code-only exchange.
	rec = post("/v1/cli-auth/code", struct{}{})
	var legacy api.CliAuthCodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &legacy); err != nil || legacy.DeviceCode != "" {
		t.Fatalf("legacy mint = %+v, %v; want no device code", legacy, err)
	}
	if rec := post("/v1/cli-auth/exchange", api.CliAuthExchangeRequest{Code: legacy.Code}); rec.Code != http.StatusNotFound {
		t.Fatalf("legacy pending exchange: %d, want 404 cli_auth_pending", rec.Code)
	}
}

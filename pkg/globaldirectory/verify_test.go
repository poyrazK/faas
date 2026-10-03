package globaldirectory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalDirectoryVerifierProof(t *testing.T) {
	token, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	cell := Cell{ID: uuid.NewString(), Region: "eu-fsn", Enabled: true, Generation: 7}
	proof := api.CellAppBindingResponse{CellID: cell.ID, Region: cell.Region, AccountID: uuid.NewString(), AppID: uuid.NewString(), AppSlug: "regional-shop"}
	valid, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	changed := func(change func(*api.CellAppBindingResponse)) string {
		p := proof
		change(&p)
		b, marshalErr := json.Marshal(p)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return string(b)
	}
	for _, tc := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"valid", string(valid), 200, true},
		{"wrong_cell", changed(func(p *api.CellAppBindingResponse) { p.CellID = uuid.NewString() }), 200, false},
		{"wrong_region", changed(func(p *api.CellAppBindingResponse) { p.Region = "eu-hel" }), 200, false},
		{"wrong_slug", changed(func(p *api.CellAppBindingResponse) { p.AppSlug = "other-shop" }), 200, false},
		{"invalid_account", changed(func(p *api.CellAppBindingResponse) { p.AccountID = "not-a-uuid" }), 200, false},
		{"zero_app", changed(func(p *api.CellAppBindingResponse) { p.AppID = uuid.Nil.String() }), 200, false},
		{"unknown_field", strings.TrimSuffix(string(valid), "}") + `,"key":"` + token + `"}`, 200, false},
		{"trailing_document", string(valid) + " {}", 200, false},
		{"malformed", token, 200, false},
		{"oversize", string(valid) + strings.Repeat(" ", api.GlobalDirectoryProofMaxBytes), 200, false},
		{"revoked", token, 401, false},
		{"read_only", token, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/cell/apps/regional-shop/binding" || r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("wrong proof request")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			candidate := cell
			candidate.APIBase = srv.URL
			binding, verifyErr := (Verifier{Client: srv.Client()}).Verify(t.Context(), candidate, proof.AppSlug, token)
			if tc.ok {
				if verifyErr != nil || binding.CellGeneration != 7 || binding.RegionalAppID != proof.AppID || binding.RegionalAccountID != proof.AccountID {
					t.Fatalf("proof=%+v err=%v", binding, verifyErr)
				}
			} else if !errors.Is(verifyErr, ErrVerification) || strings.Contains(verifyErr.Error(), token) || binding != (Binding{}) {
				t.Fatalf("unsafe verification result: %+v %v", binding, verifyErr)
			}
		})
	}
}

func TestGlobalDirectoryVerifierDoesNotFollowRedirect(t *testing.T) {
	var received atomic.Int32
	recipient := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(200) }))
	defer recipient.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, recipient.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	token, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	client := origin.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	cell := Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: origin.URL, Enabled: true, Generation: 1}
	if _, err := (Verifier{Client: client}).Verify(t.Context(), cell, "regional-shop", token); !errors.Is(err, ErrVerification) {
		t.Fatal(err)
	}
	if received.Load() != 0 {
		t.Fatal("redirect recipient received a credential-bearing request")
	}
}

func TestGlobalDirectoryVerifierCancellationAndTimeout(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	token, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	cell := Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: srv.URL, Enabled: true, Generation: 1}
	client := srv.Client()
	client.Timeout = 20 * time.Millisecond
	if _, err := (Verifier{Client: client}).Verify(t.Context(), cell, "regional-shop", token); !errors.Is(err, ErrVerification) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (Verifier{Client: srv.Client()}).Verify(ctx, cell, "regional-shop", token); !errors.Is(err, ErrVerification) {
		t.Fatal(err)
	}
	client.Timeout = 0
	if (Verifier{Client: client}).probeClient().Timeout != api.GlobalDirectoryProofTimeoutSeconds*time.Second {
		t.Fatal("unbounded client timeout")
	}
}

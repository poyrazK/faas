package managedpostgres

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	domain "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type runtimeConnectionRecorder struct {
	authority domain.RuntimeConnectionAuthority
	report    domain.RuntimeConnectionReport
	calls     int
}

func (s *runtimeConnectionRecorder) RecordRuntimeConnections(_ context.Context, a domain.RuntimeConnectionAuthority, r domain.RuntimeConnectionReport) error {
	s.authority, s.report = a, r
	s.calls++
	return nil
}

func TestRuntimeConnectionHandlerAuthenticatesAndBoundsPrivateReports(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "runtime-pools-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	account, app, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	token, err := signer.Mint(time.Now(), account, app, instance, workloadidentity.DataAPIConnectionsAudience)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := signer.Mint(time.Now(), account, app, instance, workloadidentity.QueuesAudience)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &runtimeConnectionRecorder{}
	handler := RuntimeConnectionHandler{Service: recorder, Verifier: verifier, Problem: func(w http.ResponseWriter, _ error) { w.WriteHeader(http.StatusBadRequest) }}
	body, _ := json.Marshal(domain.RuntimeConnectionReport{Version: 1, BindingID: uuid.NewString(), DatabaseIdentity: strings.Repeat("a", 64), DatabaseGeneration: 1, CredentialGeneration: 1, Role: "private-role", State: "observed", RuntimeModel: "postgrest-14.3-pools-v1", EngineConnections: 2, ObservedAt: time.Now()})
	for _, tc := range []struct {
		name, bearer, body string
		status             int
	}{
		{"valid", token.AccessToken, string(body), 204},
		{"missing", "", string(body), 401},
		{"owner key", "fp_private-owner-key", string(body), 401},
		{"wrong audience", wrong.AccessToken, string(body), 401},
		{"trailing document", token.AccessToken, string(body) + " {}", 400},
		{"body authority", token.AccessToken, strings.TrimSuffix(string(body), "}") + `,"instance_id":"forged"}`, 400},
		{"oversized", token.AccessToken, strings.Repeat("x", 8192), 400},
		{"missing zero counter", token.AccessToken, strings.Replace(string(body), `"refresh_connections":0,`, "", 1), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/runtime/data-api/connection-observations", strings.NewReader(tc.body))
			if tc.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			w := httptest.NewRecorder()
			handler.Report(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private-") {
				t.Fatal("private error leaked")
			}
		})
	}
	if recorder.calls != 1 || recorder.authority.AccountID != account || recorder.authority.AppID != app || recorder.authority.InstanceID != instance || recorder.report.Role != "private-role" {
		t.Fatal(recorder)
	}
}

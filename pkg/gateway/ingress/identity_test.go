package ingress

// adr: 612

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestIdentityRequiresPrivateHostSecretNonceAndPreservesAppPath(t *testing.T) {
	slot, session := uuid.NewString(), uuid.NewString()
	identity, err := NewIdentityHandler(testToken, slot, session)
	if err != nil {
		t.Fatal(err)
	}
	appCalls := 0
	wrapped := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { appCalls++; w.WriteHeader(204) }), identity)
	for _, tc := range []struct {
		host, token, nonce, method, query string
		status                            int
	}{
		{IdentityHost, testToken, uuid.NewString(), "GET", "", 200},
		{IdentityHost, "", uuid.NewString(), "GET", "", 404},
		{IdentityHost, testToken, "bad", "GET", "", 404},
		{IdentityHost, testToken, uuid.NewString(), "POST", "", 404},
		{IdentityHost, testToken, uuid.NewString(), "GET", "?extra=1", 404},
		{"app.example", testToken, uuid.NewString(), "GET", "", 204},
	} {
		r := httptest.NewRequest(tc.method, "http://internal"+IdentityPath+tc.query, nil)
		r.Host = tc.host
		r.Header.Set(TokenHeader, requestProof(tc.token, tc.nonce))
		r.Header.Set(NonceHeader, tc.nonce)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), testToken) {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var got Identity
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.SlotID != slot || got.SessionID != session || got.Nonce != tc.nonce || !got.AdmissionFenced || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(got, err)
			}
		}
	}
	if appCalls != 1 {
		t.Fatal("app routes or disabled wiring changed")
	}
}

func TestIdentityRejectsMalformedOversizedUnfencedAndReplayedProof(t *testing.T) {
	nonce := uuid.NewString()
	valid := Identity{SlotID: uuid.NewString(), SessionID: uuid.NewString(), Nonce: nonce, AdmissionFenced: true}
	valid.Proof = responseProof(testToken, valid)
	good, _ := json.Marshal(valid)
	for _, body := range []string{
		string(good[:len(good)-1]) + `,"extra":true}`,
		string(good) + `{}`,
		strings.Replace(string(good), nonce, uuid.NewString(), 1),
		strings.Replace(string(good), `"admission_fenced":true`, `"admission_fenced":false`, 1),
		strings.Replace(string(good), valid.SlotID, uuid.NewString(), 1),
		strings.Replace(string(good), valid.SessionID, uuid.NewString(), 1),
		strings.Replace(string(good), valid.Proof, strings.Repeat("0", 64), 1),
		strings.Repeat("x", api.RuntimeUpgradeIngressIdentityMaxBytes+1),
	} {
		resp := &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body))}
		if _, err := readIdentity(resp, nonce, testToken); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
	for _, token := range []string{"", "short", strings.ToUpper(testToken)} {
		if err := ValidateToken(token); err == nil {
			t.Fatal("invalid private token accepted")
		}
	}
}

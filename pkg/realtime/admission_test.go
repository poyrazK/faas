package realtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An endpoint update keeps only the sockets its new configuration would
// still admit.
func TestConnectionAdmissionAfterEndpointUpdate(t *testing.T) {
	now := time.Now()
	oidc := AuthPolicy{Mode: AuthModeOIDCJWT, Issuer: "https://id.example.com", JWKSURL: "https://id.example.com/jwks", Audience: []string{"a", "b"}, Algorithms: []string{"RS256"}}
	static := Endpoint{ClientAuth: AuthPolicy{Mode: AuthModeStaticBearer}, AuthToken: "new"}
	rotating := Endpoint{ClientAuth: AuthPolicy{Mode: AuthModeStaticBearer}, AuthToken: "new", AuthTokenPrevious: "old", AuthTokenPreviousExpiresAt: now.Add(time.Hour)}
	expired := rotating
	expired.AuthTokenPreviousExpiresAt = now.Add(-time.Second)
	admittedOld := newConnectionAdmission(Endpoint{ClientAuth: AuthPolicy{Mode: AuthModeStaticBearer}, AuthToken: "old"}, false, false, "old", "")
	admittedOIDC := newConnectionAdmission(Endpoint{ClientAuth: oidc}, false, false, "", "https://App.example.com")
	reordered := oidc
	reordered.Audience = []string{"b", "a"}
	reordered.ExtractClaims = []string{"org_id"}
	otherIssuer := oidc
	otherIssuer.Issuer = "https://other.example.com"
	browser := newConnectionAdmission(Endpoint{ClientAuth: oidc}, false, true, "", "https://app.example.com")
	ticket := newConnectionAdmission(Endpoint{ClientAuth: AuthPolicy{Mode: AuthModeNone}}, true, false, "", "")

	for _, tt := range []struct {
		name      string
		admission connectionAdmission
		endpoint  Endpoint
		want      bool
	}{
		{"old token during rotation grace", admittedOld, rotating, true},
		{"old token after finalize", admittedOld, static, false},
		{"old token after the grace deadline", admittedOld, expired, false},
		{"auth mode changed", admittedOld, Endpoint{ClientAuth: AuthPolicy{Mode: AuthModeNone}}, false},
		{"same OIDC policy, reordered audience and new channel-rule claims", admittedOIDC, Endpoint{ClientAuth: reordered}, true},
		{"OIDC issuer changed", admittedOIDC, Endpoint{ClientAuth: otherIssuer}, false},
		{"origin still allowed (normalized)", admittedOIDC, Endpoint{ClientAuth: oidc, AllowedOrigins: []string{"https://app.example.com"}}, true},
		{"origin removed from allowed origins", admittedOIDC, Endpoint{ClientAuth: oidc, AllowedOrigins: []string{"https://other.example.com"}}, false},
		{"browser credential needs an origin policy", browser, Endpoint{ClientAuth: oidc}, false},
		{"ticket survives an auth mode change", ticket, Endpoint{ClientAuth: AuthPolicy{Mode: AuthModeOIDCJWT}}, true},
		{"no admission record is never judged", connectionAdmission{}, static, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.admission.admits(&tt.endpoint, now); got != tt.want {
				t.Fatalf("admits = %v, want %v", got, tt.want)
			}
		})
	}
}

// adr: 156 — an endpoint answers only on hosts of its own account; a request
// from a gateway that names no host account keeps the old behavior.
func TestServedOnHost(t *testing.T) {
	state := &endpointState{}
	state.config.Store(&Endpoint{ID: "ep", AccountID: "acct-a"})
	for _, tt := range []struct {
		name    string
		headers []string
		want    bool
	}{
		{"older gateway names no account", nil, true},
		{"host of the endpoint's account", []string{"acct-a"}, true},
		{"another account's host", []string{"acct-b"}, false},
		{"empty account", []string{""}, false},
		{"ambiguous accounts", []string{"acct-a", "acct-b"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://app.example.com"+ManagedPathPrefix+"ep", nil)
			for _, v := range tt.headers {
				r.Header.Add(HostAccountHeader, v)
			}
			if got := servedOnHost(state, r); got != tt.want {
				t.Fatalf("servedOnHost = %v, want %v", got, tt.want)
			}
		})
	}
}

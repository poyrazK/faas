package devbridge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestContextPropagationBoundaries(t *testing.T) {
	s, creds, err := NewSession(Scope{AccountID: "account", DeveloperID: "alice", EnvironmentID: "dev", TargetAppID: "payments", DependencyAppIDs: []string{"frontend"}}, time.Now(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	c := RequestContext{s.Scope.AccountID, s.ID, creds.RequestToken}
	for _, app := range []string{"payments", "frontend"} {
		if s.AuthorizeContextRoute(time.Now(), c, "dev", app) != nil {
			t.Fatal("selected graph rejected")
		}
	}
	for _, test := range []struct{ token, environment, app string }{
		{creds.AttachmentToken, "dev", "payments"}, {creds.RequestToken, "production", "payments"}, {creds.RequestToken, "dev", "unrelated"},
	} {
		bad := c
		bad.Token = test.token
		if s.AuthorizeContextRoute(time.Now(), bad, test.environment, test.app) == nil {
			t.Fatal("unscoped context accepted")
		}
	}
	for _, value := range []string{"", c.Encode() + ".extra", "account." + s.ID + ".short"} {
		if _, err := ParseRequestContext(value); err == nil {
			t.Fatal("malformed context accepted")
		}
	}
	var host string
	transport := PropagationTransport{Base: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		want := ""
		if host == "payments.internal" || host == "orders.svc.gregale" {
			want = c.Encode()
		}
		if r.Header.Get(ContextHeader) != want || r.Header.Get(TokenHeader) != "" {
			t.Error("routing authority escaped its destination scope")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	handler := PropagationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, destination := range []string{"payments.internal", "orders.svc.gregale", "api.stripe.com", "payments.internal.attacker.example", "extra.payments.internal", "extra.orders.svc.gregale"} {
			host = destination
			out, _ := http.NewRequestWithContext(r.Context(), "GET", "http://"+host+"/", nil)
			out.Header.Set(ContextHeader, "forged")
			out.Header.Set(TokenHeader, creds.AttachmentToken)
			out.Header["x-gregale-dev-bridge-token"] = []string{creds.AttachmentToken}
			out.Header["x-gregale-dev-session-context"] = []string{"forged"}
			response, err := transport.RoundTrip(out)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
		}
	}))
	r := httptest.NewRequest("GET", "http://frontend/", nil)
	r.Header.Set(ContextHeader, c.Encode())
	handler.ServeHTTP(httptest.NewRecorder(), r)
}

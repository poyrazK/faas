package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A policy check that ran out of time saved nothing and asks to be resent
// (Retry-After). The client resends the same request with the same
// Idempotency-Key, up to trafficPolicyCheckAttempts sends; a size ceiling has
// no Retry-After and is returned at once.
func TestClientResendsTimedOutTrafficPolicyCheck(t *testing.T) {
	timedOut := func(w http.ResponseWriter) {
		p := ErrTrafficPolicyTooComplex("time", "milliseconds", 2000, 2001)
		w.Header().Set("Retry-After", "0")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(p.Status)
		_ = json.NewEncoder(w).Encode(p)
	}
	tooLarge := func(w http.ResponseWriter) {
		p := ErrTrafficPolicyTooComplex("states", "states", 100, 101)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(p.Status)
		_ = json.NewEncoder(w).Encode(p)
	}
	cases := []struct {
		name      string
		refusals  int
		refuse    func(http.ResponseWriter)
		wantSends int
		wantCode  string
	}{
		{name: "clears on the third send", refusals: 2, refuse: timedOut, wantSends: 3},
		{name: "gives up after four sends", refusals: 10, refuse: timedOut, wantSends: trafficPolicyCheckAttempts, wantCode: CodeTrafficPolicyTooComplex},
		{name: "size ceiling is not resent", refusals: 10, refuse: tooLarge, wantSends: 1, wantCode: CodeTrafficPolicyTooComplex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var keys, bodies []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				keys, bodies = append(keys, r.Header.Get("Idempotency-Key")), append(bodies, string(body))
				if len(keys) <= tc.refusals {
					tc.refuse(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"id":"dep-1"}`))
			}))
			defer server.Close()
			var out struct{ ID string }
			err := NewClient(server.URL, "token").do(context.Background(), http.MethodPost, "/v1/apps/a/deployments", map[string]string{"image": "x"}, &out)
			if len(keys) != tc.wantSends {
				t.Fatalf("sends = %d, want %d", len(keys), tc.wantSends)
			}
			for i := range keys {
				if keys[i] == "" || keys[i] != keys[0] || bodies[i] != bodies[0] {
					t.Fatalf("send %d key=%q body=%q, want the first send's key %q and body %q", i+1, keys[i], bodies[i], keys[0], bodies[0])
				}
			}
			var apiErr *APIError
			switch {
			case tc.wantCode == "" && (err != nil || out.ID != "dep-1"):
				t.Fatalf("err = %v out = %+v, want the accepted deployment", err, out)
			case tc.wantCode != "" && (!errors.As(err, &apiErr) || apiErr.Problem.Code != tc.wantCode):
				t.Fatalf("err = %v, want %s", err, tc.wantCode)
			}
		})
	}
}

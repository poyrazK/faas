package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeCredentialMatcher struct {
	match  bool
	err    error
	column string
}

func (f *fakeCredentialMatcher) Match(_ context.Context, column string, _, _ []byte) (bool, error) {
	f.column = column
	return f.match, f.err
}

// adr: 20261029202403 — with apid's matcher the gateway no longer compares
// the stored hash itself: a keyed row passes only when apid says the token
// matches, and an apid failure fails closed.
func TestEnforceConsumerAuth_KeyedHashesAskApid(t *testing.T) {
	tests := []struct {
		name   string
		match  bool
		err    error
		ok     bool
		status int
		code   string
	}{
		{name: "apid matches a keyed row", match: true, ok: true, status: http.StatusOK},
		{name: "apid refuses", status: http.StatusUnauthorized, code: api.CodeConsumerKeyInvalid},
		{name: "apid unavailable", err: errors.New("apid down"), status: http.StatusServiceUnavailable, code: api.CodeCapacity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, token := consumerAuthFixture()
			store.key.Hash = append([]byte{1}, store.key.Hash...) // a keyed digest's shape
			matcher := &fakeCredentialMatcher{match: tc.match, err: tc.err}
			h := NewHandlerWith(nil, nil, nil).WithConsumerAuth(store).WithCredentialMatcher(matcher)
			rr, _, ok := runConsumerAuthGate(t, h, App{ID: "app-1", AccountID: "acct-1", ConsumerAuthMode: api.ConsumerAuthModeRequired}, http.MethodGet, token)
			if ok != tc.ok || rr.Code != tc.status {
				t.Fatalf("ok=%v status=%d, want %v/%d", ok, rr.Code, tc.ok, tc.status)
			}
			if tc.code != "" && problemCode(t, rr) != tc.code {
				t.Fatalf("code = %s, want %s", problemCode(t, rr), tc.code)
			}
			if matcher.column != ConsumerKeyCredentialColumn {
				t.Fatalf("asked apid about column %q", matcher.column)
			}
		})
	}
}

package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeHoneytokens struct {
	byToken map[string]state.Honeytoken
	err     error
	calls   int
}

func (f *fakeHoneytokens) MatchHoneytoken(_ context.Context, token string) (state.Honeytoken, error) {
	f.calls++
	if f.err != nil {
		return state.Honeytoken{}, f.err
	}
	h, ok := f.byToken[token]
	if !ok {
		return state.Honeytoken{}, state.ErrNotFound
	}
	return h, nil
}

// TestRequireSession_Honeytoken pins ADR 20261024014500: a presented
// honeytoken is reported, and the caller still gets the ordinary 401.
func TestRequireSession_Honeytoken(t *testing.T) {
	decoy := state.Honeytoken{ID: "ht-1", Kind: state.HoneytokenKindAPIKey, Label: "decoy app GREGALE_DEPLOY_KEY"}
	const realKey = "fp_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name       string
		bearer     string
		lookupErr  error
		wantReport bool
		wantCalls  int
	}{
		{name: "honeytoken", bearer: validBearerKey, wantReport: true, wantCalls: 1},
		{name: "unknown key", bearer: "fp_live_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", wantCalls: 1},
		{name: "real key is not looked up", bearer: realKey, wantCalls: 0},
		{name: "lookup failure stays a 401", bearer: validBearerKey, lookupErr: errors.New("pg down"), wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authn := newFakeAuthn()
			authn.authKey[string(api.HashAPIKey(realKey))] = authResult{acct: mkActiveAccount("acct-1"), key: mkKey("key-1", api.ScopeAdmin)}
			honey := &fakeHoneytokens{byToken: map[string]state.Honeytoken{validBearerKey: decoy}, err: tc.lookupErr}
			mw := newMW(t, authn, nil, nil, nil)
			mw.Honeytokens = honey
			var reported []state.Honeytoken
			mw.OnHoneytoken = func(_ *http.Request, h state.Honeytoken) { reported = append(reported, h) }
			h := mw.RequireSession(func(w http.ResponseWriter, _ *http.Request, _ state.Account) { w.WriteHeader(http.StatusOK) })

			rec := httptest.NewRecorder()
			h(rec, mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + tc.bearer}, nil))

			if honey.calls != tc.wantCalls {
				t.Fatalf("registry lookups = %d, want %d", honey.calls, tc.wantCalls)
			}
			if got := len(reported) == 1 && reported[0].ID == decoy.ID; got != tc.wantReport {
				t.Fatalf("reported = %+v, want reported %v", reported, tc.wantReport)
			}
			want := http.StatusUnauthorized
			if tc.bearer == realKey {
				want = http.StatusOK
			}
			if rec.Code != want {
				t.Fatalf("status = %d, want %d", rec.Code, want)
			}
		})
	}
}

// adr: 20261104085712
package main

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type stubKeyProjectLimits struct {
	ids   map[string][]string
	reads int
}

func (s *stubKeyProjectLimits) APIKeyProjectLimit(_ context.Context, keyID string) ([]string, bool, error) {
	s.reads++
	ids, ok := s.ids[keyID]
	return ids, ok, nil
}

// The adapter carries a key's app binding and, for an agent key, its project
// limit to the gateway, which refuses apps outside them (ADR-20261104085712).
func TestRequireAuthnAdapterCarriesKeyLimits(t *testing.T) {
	limits := &stubKeyProjectLimits{ids: map[string][]string{"agent": {"proj-a"}}}
	for _, tc := range []struct {
		key         state.APIKey
		wantLimited bool
		wantReads   int
	}{
		{state.APIKey{ID: "agent", Scopes: []string{api.ScopeAgent, api.ScopeAppsRead}}, true, 1},
		{state.APIKey{ID: "plain", Scopes: []string{api.ScopeAppsRead}, AppID: "app-1"}, false, 0},
	} {
		limits.reads = 0
		adapter := newRequireAuthnAdapter(stubOnlyAuthnField(&stubAuthn{acct: state.Account{ID: "acct"}, key: tc.key}), limits)
		_, key, err := adapter.AuthenticateKey(context.Background(), api.HashAPIKey("fp_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
		if err != nil {
			t.Fatal(err)
		}
		if key.AppID != tc.key.AppID || key.ProjectLimited != tc.wantLimited || tc.wantLimited && !slices.Equal(key.ProjectIDs, []string{"proj-a"}) || limits.reads != tc.wantReads {
			t.Fatalf("key %s: got %+v after %d limit reads", tc.key.ID, key, limits.reads)
		}
	}
}

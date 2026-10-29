package gateway

import (
	"io"
	"log/slog"
	"slices"
	"testing"
)

// accountInvalidationRecorder records the account-scoped invalidations a
// matcher receives.
type accountInvalidationRecorder struct {
	noOpEdgeRuleMatcher
	calls []string
}

func (m *accountInvalidationRecorder) InvalidateAccountHosts(accountID, appID string, patterns []string) {
	m.calls = append(m.calls, accountID+"/"+appID+":"+patterns[0])
}

// adr: 091 — amendment 2026-10-28 (review round 3): deleting an app drops
// the compiled rule sets of its account, so its rules on "*", wildcards and
// other apps' hosts stop applying to those apps at once. An app the gateway
// never cached leaves the sets to their TTL.
func TestInvalidateEdgeRulesOfDeletedApp(t *testing.T) {
	matcher := &accountInvalidationRecorder{}
	b := NewPGBackend(nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil))).WithEdgeRules(matcher)
	b.putApp(App{ID: "app-gone", AccountID: "acct-1"})
	b.InvalidateEdgeRulesOfDeletedApp("app-gone")
	b.InvalidateEdgeRulesOfDeletedApp("app-never-cached")
	if want := []string{"acct-1/app-gone:*"}; !slices.Equal(matcher.calls, want) {
		t.Fatalf("invalidations = %v, want %v", matcher.calls, want)
	}
}

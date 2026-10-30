package githubd

import (
	"strings"
	"testing"
	"time"
)

// GitHub lists an installation for anyone with access to one of its
// repositories, so any company member could bind the company installation
// and reach every repository it covers. Binding now needs an organization
// admin, or the owner of a personal installation (bug hunt 2026-10-28 H49).
func TestExchangeOAuthCodeRequiresInstallationAuthority(t *testing.T) {
	cases := []struct {
		name      string
		account   map[string]any
		userLogin string
		orgRole   string
		wantErr   string
	}{
		{name: "org admin", account: map[string]any{"login": "acme", "type": "Organization"}, orgRole: "admin"},
		{name: "org member", account: map[string]any{"login": "acme", "type": "Organization"}, orgRole: "member", wantErr: "organization admin"},
		{name: "not a member", account: map[string]any{"login": "acme", "type": "Organization"}, wantErr: "organization admin"},
		// An installation that has not accepted the Members (read) permission.
		{name: "membership not readable", account: map[string]any{"login": "acme", "type": "Organization"}, orgRole: "forbidden", wantErr: "Members (read) permission"},
		{name: "personal owner", account: map[string]any{"login": "octocat", "type": "User"}},
		{name: "collaborator on a personal installation", account: map[string]any{"login": "someone-else", "type": "User"}, wantErr: "only someone-else"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ident, recipient := newTestAgeKeypair(t)
			fake := newFakeGithubServer(t, fakeGithubOpts{
				accessToken:  "user-token",
				installs:     []map[string]any{{"id": float64(77), "account": tc.account}},
				installToken: "ghs_install",
				expiresAt:    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				appID:        "100",
				verifyLogin:  tc.account["login"].(string),
				userLogin:    tc.userLogin,
				orgRole:      tc.orgRole,
			})
			defer fake.Close()
			auth := newTestAppAuth(t, "100", fake.URL)
			svc := NewRealService(auth, NewTokenCache(auth, 5*time.Minute), nil, newMemBindingsStore(), newMemStoreInstalls(), recipient, ident, newRecordingAuditFn().AuditEvent)
			_, _, err := svc.ExchangeOAuthCode("acct-1", "code", "state")
			if tc.wantErr == "" && err != nil {
				t.Fatalf("ExchangeOAuthCode: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("ExchangeOAuthCode err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

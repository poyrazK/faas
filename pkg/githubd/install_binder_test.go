package githubd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
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
		{name: "org member", account: map[string]any{"login": "acme", "type": "Organization"}, orgRole: "member", wantErr: "github_org_admin_required"},
		{name: "not a member", account: map[string]any{"login": "acme", "type": "Organization"}, wantErr: "github_org_admin_required"},
		// An installation that has not accepted the Members (read) permission.
		{name: "membership not readable", account: map[string]any{"login": "acme", "type": "Organization"}, orgRole: "forbidden", wantErr: "github_permission_pending"},
		{name: "personal owner", account: map[string]any{"login": "octocat", "type": "User"}},
		{name: "collaborator on a personal installation", account: map[string]any{"login": "someone-else", "type": "User"}, wantErr: "github_installation_owner_required"},
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
			_, _, err := svc.ExchangeOAuthCode("acct-1", "code", "state.77")
			if tc.wantErr == "" && err != nil {
				t.Fatalf("ExchangeOAuthCode: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("ExchangeOAuthCode err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// A user can see the App on collaborators' accounts before installing it on
// their own. Discovery must offer installation, without connecting or minting
// credentials for those other owners. Explicit selection still refuses above.
func TestExchangeOAuthCodeOtherPersonalInstallationsRequireInstall(t *testing.T) {
	ident, recipient := newTestAgeKeypair(t)
	fake := newFakeGithubServer(t, fakeGithubOpts{
		accessToken: "private-user-token",
		installs: []map[string]any{
			{"id": float64(77), "account": map[string]any{"login": "other-owner", "type": "User"}},
			{"id": float64(88), "account": map[string]any{"login": "another-owner", "type": "User"}},
		},
		appID: "100",
	})
	defer fake.Close()
	auth := newTestAppAuth(t, "100", fake.URL)
	audit := newRecordingAuditFn()
	installs := newMemStoreInstalls()
	svc := NewRealService(auth, NewTokenCache(auth, 5*time.Minute), nil, newMemBindingsStore(), installs, recipient, ident, audit.AuditEvent)
	_, _, err := svc.ExchangeOAuthCode("acct-1", "private-code", "private-state")
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeGitHubAppNotInstalled {
		t.Fatalf("discovery = %v, want installation recovery", err)
	}
	if len(audit.events) != 2 {
		t.Fatalf("refusal diagnostics = %v, want both rejected candidates", audit.events)
	}
	for i, event := range audit.events {
		if event.event != "auth.install.connect_refused" || event.accountID != "acct-1" ||
			event.payload["installation_id"] != []int64{77, 88}[i] ||
			event.payload["github_owner"] != []string{"other-owner", "another-owner"}[i] ||
			event.payload["owner_type"] != "User" || event.payload["reason"] != api.CodeGitHubInstallationOwnerRequired || len(event.payload) != 4 {
			t.Fatalf("refusal diagnostic %d = %+v", i, event)
		}
	}
	if _, err := installs.ForAccount(t.Context(), "acct-1"); err == nil {
		t.Fatal("recovery must not connect another owner's installation")
	}
}

// A GitHub user who can see several installations used to get "choose an
// installation" as a 502 with no way to choose (ADR-20261101160300). The
// installations they may connect are now offered; one is taken directly.
func TestExchangeOAuthCodeChoosesAmongConnectableInstallations(t *testing.T) {
	personal := map[string]any{"id": float64(77), "account": map[string]any{"login": "octocat", "type": "User"}}
	org := map[string]any{"id": float64(88), "account": map[string]any{"login": "acme", "type": "Organization"}}
	cases := []struct {
		name      string
		orgRole   string
		state     string
		wantID    string
		wantIDs   []int64
		wantCode  string
		installed []map[string]any
	}{
		{name: "one connectable", orgRole: "member", state: "state", installed: []map[string]any{personal, org}, wantID: "77"},
		{name: "own personal installation after another owner's", state: "state", installed: []map[string]any{{"id": float64(66), "account": map[string]any{"login": "another-owner", "type": "User"}}, personal}, wantID: "77"},
		{name: "several connectable", orgRole: "admin", state: "state", installed: []map[string]any{personal, org}, wantIDs: []int64{77, 88}},
		{name: "chosen in the state", orgRole: "admin", state: "state.77", installed: []map[string]any{personal, org}, wantID: "77"},
		{name: "chosen but not visible", orgRole: "admin", state: "state.99", installed: []map[string]any{personal, org}, wantCode: api.CodeGitHubInstallationNotAccessible},
		{name: "none installed", state: "state", wantCode: api.CodeGitHubAppNotInstalled},
		{name: "organization member still refused during discovery", orgRole: "member", state: "state", installed: []map[string]any{org}, wantCode: api.CodeGitHubOrgAdminRequired},
		{name: "organization permission still required during discovery", orgRole: "forbidden", state: "state", installed: []map[string]any{org}, wantCode: api.CodeGitHubPermissionPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ident, recipient := newTestAgeKeypair(t)
			fake := newFakeGithubServer(t, fakeGithubOpts{
				accessToken: "user-token", installs: tc.installed, installToken: "ghs_install",
				expiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), appID: "100", orgRole: tc.orgRole,
			})
			defer fake.Close()
			auth := newTestAppAuth(t, "100", fake.URL)
			svc := NewRealService(auth, NewTokenCache(auth, 5*time.Minute), nil, newMemBindingsStore(), newMemStoreInstalls(), recipient, ident, newRecordingAuditFn().AuditEvent)
			id, branch, err := svc.ExchangeOAuthCode("acct-1", "code", tc.state)
			switch {
			case tc.wantID != "":
				if err != nil || id != tc.wantID {
					t.Fatalf("ExchangeOAuthCode = %q, %v; want installation %s", id, err, tc.wantID)
				}
				if branch != "" {
					t.Fatalf("default branch = %q; an installation has none", branch)
				}
			case tc.wantIDs != nil:
				choice, ok := githubdgrpc.AsInstallationChoice(err)
				if !ok || len(choice.Candidates) != len(tc.wantIDs) {
					t.Fatalf("err = %v, want a choice of %v", err, tc.wantIDs)
				}
				for i, candidate := range choice.Candidates {
					if candidate.ID != tc.wantIDs[i] || candidate.Account == "" {
						t.Fatalf("candidate %d = %+v, want installation %d", i, candidate, tc.wantIDs[i])
					}
				}
			default:
				var p *api.Problem
				if !errors.As(err, &p) || p.Code != tc.wantCode {
					t.Fatalf("err = %v, want problem %s", err, tc.wantCode)
				}
				if tc.wantCode == api.CodeGitHubAppNotInstalled && !strings.Contains(err.Error(), "user has no app installations") {
					t.Fatalf("err = %v; an older apid matches \"user has no app installations\"", err)
				}
			}
		})
	}
}

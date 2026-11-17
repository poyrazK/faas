package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pendingGrantsBackend lists only the Store methods this test calls.
type pendingGrantsBackend interface {
	state.AccountPendingGrantsStore
	state.AccountFlowStore
	CreateAccountWithPersonalOrg(ctx context.Context, params state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error)
	CreateOrgInvitation(ctx context.Context, inv state.OrgInvitation) (state.OrgInvitation, error)
	OrgInvitationByTokenHash(ctx context.Context, hash []byte) (state.OrgInvitation, error)
	IssueCliAuthCode(ctx context.Context, tokenHash []byte, expiresAt time.Time) error
	ClaimCliAuthCode(ctx context.Context, tokenHash []byte, accountID string) error
	ConsumeCliAuthCode(ctx context.Context, tokenHash []byte) (api.CliAuthStatus, string, error)
}

func pendingGrantsBackends() map[string]func(t *testing.T) pendingGrantsBackend {
	return map[string]func(t *testing.T) pendingGrantsBackend{
		"memory": func(*testing.T) pendingGrantsBackend { return state.NewMemStore() },
		"postgres": func(t *testing.T) pendingGrantsBackend {
			pool := pgtest.OpenMigrated(t)
			if err := db.MigrateUp(context.Background(), pool); err != nil {
				t.Fatalf("db.MigrateUp: %v", err)
			}
			return state.NewPgStore(pool)
		},
	}
}

// Recovery ends the grants an account started and nobody redeemed yet: an
// email change, invitations it sent, and approved CLI codes. Another
// account's grants are untouched (ADR 20261109120000).
func TestEndAccountPendingGrants(t *testing.T) {
	for name, open := range pendingGrantsBackends() {
		t.Run(name, func(t *testing.T) {
			s := open(t)
			ctx := t.Context()
			mk := func(email string) state.CreateAccountWithPersonalOrgResult {
				res, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: email, Plan: api.PlanFree})
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			victim, other := mk("grants-victim@example.com"), mk("grants-other@example.com")
			exp := time.Now().Add(time.Hour)
			grant := func(acct state.CreateAccountWithPersonalOrgResult, tag string) {
				if err := s.IssueEmailChange(ctx, flowToken(tag+"-email"), acct.Account.ID, tag+"-new@example.com", exp); err != nil {
					t.Fatal(err)
				}
				inviter := acct.Account.ID
				if _, err := s.CreateOrgInvitation(ctx, state.OrgInvitation{OrgID: acct.PersonalOrg.ID, Email: tag + "-invitee@example.com",
					Role: state.OrgRoleDeveloper, TokenHash: flowToken(tag + "-invite"), InvitedByAccountID: &inviter, ExpiresAt: exp}); err != nil {
					t.Fatal(err)
				}
				if err := s.IssueCliAuthCode(ctx, flowToken(tag+"-cli"), exp); err != nil {
					t.Fatal(err)
				}
				if err := s.ClaimCliAuthCode(ctx, flowToken(tag+"-cli"), acct.Account.ID); err != nil {
					t.Fatal(err)
				}
			}
			grant(victim, "victim")
			grant(other, "other")

			got, err := s.EndAccountPendingGrants(ctx, victim.Account.ID)
			if err != nil {
				t.Fatal(err)
			}
			if want := (state.PendingGrantsEnded{EmailChanges: 1, Invitations: 1, CliCodes: 1}); got != want {
				t.Fatalf("ended = %+v, want %+v", got, want)
			}
			if again, err := s.EndAccountPendingGrants(ctx, victim.Account.ID); err != nil || again.Any() {
				t.Fatalf("second call ended %+v (err %v), want nothing", again, err)
			}

			if _, err := s.ConsumeEmailChange(ctx, flowToken("victim-email")); err == nil {
				t.Error("victim's email change still confirms")
			}
			if inv, err := s.OrgInvitationByTokenHash(ctx, flowToken("victim-invite")); err == nil && inv.RevokedAt == nil {
				t.Error("victim's invitation still pending")
			}
			if status, acct, _ := s.ConsumeCliAuthCode(ctx, flowToken("victim-cli")); status == api.CliAuthStatusConsumed || acct != "" {
				t.Errorf("victim's approved CLI code still exchanges: %s %q", status, acct)
			}

			if _, err := s.ConsumeEmailChange(ctx, flowToken("other-email")); err != nil {
				t.Errorf("other account's email change: %v", err)
			}
			if inv, err := s.OrgInvitationByTokenHash(ctx, flowToken("other-invite")); err != nil || inv.RevokedAt != nil {
				t.Errorf("other account's invitation revoked (err %v)", err)
			}
			if status, acct, err := s.ConsumeCliAuthCode(ctx, flowToken("other-cli")); err != nil || status != api.CliAuthStatusConsumed || acct != other.Account.ID {
				t.Errorf("other account's CLI code = %s %q %v", status, acct, err)
			}
		})
	}
}

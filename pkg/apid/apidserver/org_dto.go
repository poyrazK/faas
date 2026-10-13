// Translator: cmd/apid/org_dto.go — the pkg/api ↔ pkg/state seam
// for the org surface (issue #190 / IAM-6 / ADR-061, PR 5).
//
// Lives in cmd/apid (not pkg/api) because:
//   - cmd/apid already imports both packages (pkg/api for the
//     wire-shape + pkg/state for the typed store rows), so the
//     translator is the only place the cycle resolves.
//   - the member-to-DTO translation reads the joined account row
//     (s.store.AccountByID) to surface the member's email — a
//     pkg/api helper would force pkg/api to import pkg/state.
//
// Mirrors pkg/api/alerts.go's AlertRuleResponseFromRow shape: the
// pkg/api side exports the wire type + converter (taking primitive
// rows); this file bridges from the state.Org / state.OrgMembership
// typed row to the primitive OrgRow / OrgMemberRow accepted by the
// pkg/api converter.

package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// orgToRow maps a state.Org to the wire-shaped api.OrgRow. Plan
// fields are strings; time.Time passes through unchanged. The
// stripe / provider columns are intentionally dropped — they're
// administrative plumbing that the customer wire-shape shouldn't
// expose (they're used by meterd in PR 7).
func orgToRow(o state.Org) api.OrgRow {
	return api.OrgRow{
		ID:        o.ID,
		Slug:      o.Slug,
		Name:      o.Name,
		Personal:  o.Personal,
		Plan:      string(o.Plan),
		Status:    string(o.Status),
		CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt,
	}
}

// memberToRow maps a state.OrgMembership to an api.OrgMemberRow. The
// email join is performed against s.store; if the join fails the
// row is rendered with an empty email (the account may have been
// deleted — ON DELETE SET NULL is the pin for that pathway, but the
// membership row itself may survive with a stale account id; the
// place-holder email is what the customer sees until PR 8's GDPR
// purge runs).
func (s *server) memberToRow(ctx context.Context, mem state.OrgMembership) api.OrgMemberRow {
	email := ""
	if acct, err := s.store.AccountByID(ctx, mem.AccountID); err == nil {
		email = acct.Email
	}
	return api.OrgMemberRow{
		AccountID: mem.AccountID,
		Email:     email,
		Role:      string(mem.Role),
		JoinedAt:  mem.JoinedAt,
	}
}

// invitationToRow maps a state.OrgInvitation + the joined org row
// to an api.OrgInvitationRow. orgSlug is the join; now is the
// clock for deriving the runtime status (the store doesn't store
// an enum — pkg/api.DeriveOrgInvitationStatus computes it at the
// boundary from the (consumed_at, revoked_at) tuple).
func invitationToRow(inv state.OrgInvitation, orgSlug string, now time.Time) api.OrgInvitationRow {
	return api.OrgInvitationRow{
		ID:        inv.ID,
		OrgID:     inv.OrgID,
		OrgSlug:   orgSlug,
		Email:     inv.Email,
		Role:      string(inv.Role),
		Status:    api.DeriveOrgInvitationStatus(inv.ConsumedAt, inv.RevokedAt, inv.ExpiresAt, now),
		ExpiresAt: inv.ExpiresAt,
		CreatedAt: inv.CreatedAt,
	}
}

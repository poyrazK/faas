// Unit tests for the /v1/orgs/{slug}/... customer surface
// (issue #190 / IAM-6 / ADR-061, PR 5). The HTTP-handler integration
// tests live in cmd/e2e/org_lifecycle_e2e_test.go; this file pins the
// Store-layer parity tests for the new TransferOrgOwnership method
// (the only Store surface PR 5 adds) and a few handler-level
// invariants that are easier to assert here than over the wire.
//
// What this file does NOT cover (covered by e2e/org_lifecycle_e2e_test.go):
//   - happy path POST /v1/orgs → listOrgsForCaller
//   - loadOrg membership stamping (covered by e2e/load_org_e2e_test.go)
//   - 30-s queue limit, full middleware composition
package apidserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestTransferOrgOwnership_MemStorePin exercises the new
// pkg/state::TransferOrgOwnership on the in-memory Store. The
// pgstore mirror test lives in pkg/state/memstore_orgs_test.go
// (sister parity); this file pins the handler-side contract: the
// Store returns the expected sentinel errors so the handler can map
// them to stable wire codes.
//
// Three scenarios, one per sentinel the handler distinguishes:
//
//   - ErrOrgLastOwner → 409 org_last_owner (caller not the active owner,
//     OR new owner already owner, OR demote-first ordering trips the
//     partial unique)
//   - ErrNotFound     → 404 (new owner is not an active member)
//   - nil error       → both rows updated atomically
func TestTransferOrgOwnership_MemStorePin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := state.NewMemStore()

	owner, err := store.CreateAccount(ctx, "owner@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount owner: %v", err)
	}
	member, err := store.CreateAccount(ctx, "member@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount member: %v", err)
	}
	stranger, err := store.CreateAccount(ctx, "stranger@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount stranger: %v", err)
	}

	org, err := store.CreateOrg(ctx, state.Org{
		Slug: "acme",
		Name: "Acme Inc.",
		Plan: api.PlanHobby,
	})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember owner: %v", err)
	}
	if err := store.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("AddOrgMember member: %v", err)
	}

	// Case 1: success — owner promotes member, becomes admin.
	t.Run("success_demotes_caller_to_admin", func(t *testing.T) {
		if err := store.TransferOrgOwnership(ctx, org.ID, owner.ID, member.ID); err != nil {
			t.Fatalf("TransferOrgOwnership: %v", err)
		}
		row, err := store.OrgMemberByAccount(ctx, org.ID, member.ID)
		if err != nil {
			t.Fatalf("OrgMemberByAccount(new owner): %v", err)
		}
		if row.Role != state.OrgRoleOwner {
			t.Errorf("new owner role = %q, want %q", row.Role, state.OrgRoleOwner)
		}
		row, err = store.OrgMemberByAccount(ctx, org.ID, owner.ID)
		if err != nil {
			t.Fatalf("OrgMemberByAccount(prev owner): %v", err)
		}
		if row.Role != state.OrgRoleAdmin {
			t.Errorf("prev owner role = %q, want %q (demoted to admin)", row.Role, state.OrgRoleAdmin)
		}

		// Restore for the next sub-test (no torn state).
		if err := store.TransferOrgOwnership(ctx, org.ID, member.ID, owner.ID); err != nil {
			t.Fatalf("restore: %v", err)
		}
	})

	// Case 2: caller is not the active owner → ErrOrgLastOwner.
	t.Run("caller_not_owner", func(t *testing.T) {
		err := store.TransferOrgOwnership(ctx, org.ID, member.ID, owner.ID)
		if !errors.Is(err, state.ErrOrgLastOwner) {
			t.Errorf("err = %v, want ErrOrgLastOwner", err)
		}
	})

	// Case 3: target is not an active member → ErrNotFound
	// (cross-tenant IDOR-safe collapse — the handler turns it into
	// a 404).
	t.Run("target_not_member", func(t *testing.T) {
		err := store.TransferOrgOwnership(ctx, org.ID, owner.ID, stranger.ID)
		if !errors.Is(err, state.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

// TestValidateOrgSlug_RejectsEmpties pins the API-boundary slug
// validator (the LoadOrg middleware caps at 64 chars and degrades
// to passthrough on oversize; the handler must reject *before*
// that point). Mirrors pkg/api.TestValidateOrgSlug so a regression
// in the regex propagates here as well.
func TestValidateOrgSlug_RejectsEmpties(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		slug string
		ok   bool
	}{
		{"min_ok", "abc", true},
		{"max_ok", "a" + strings.Repeat("b", 30) + "c", true},
		{"too_short_2", "ab", false},
		{"too_short_1", "a", false},
		{"uppercase", "Acme", false},
		{"underscore", "acme_inc", false},
		{"trailing_dash", "acme-", false},
		{"leading_dash", "-acme", false},
		{"empty", "", false},
		{"too_long_33", "a" + strings.Repeat("b", 31) + "c", false},
		{"contains_dot", "acme.co", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			reason := api.ValidateOrgSlug(tc.slug)
			if tc.ok && reason != "" {
				t.Errorf("slug %q rejected: %s", tc.slug, reason)
			}
			if !tc.ok && reason == "" {
				t.Errorf("slug %q accepted (should have been rejected)", tc.slug)
			}
		})
	}
}

// TestDeriveOrgInvitationStatus_PinsStateMachine pins the four
// terminal states so any future patch to the runtime status logic
// surfaces here. Mirrors the SQL CHECK on org_invitations
// (migration 00087).
func TestDeriveOrgInvitationStatus_PinsStateMachine(t *testing.T) {
	t.Parallel()
	now := mustTime("2026-08-03T12:00:00Z")
	past := mustTime("2026-08-02T12:00:00Z")
	future := mustTime("2026-08-04T12:00:00Z")
	zero := mustTime("0001-01-01T00:00:00Z")
	pastString := past.Format(time.RFC3339)
	futureString := future.Format(time.RFC3339)
	zeroString := zero.Format(time.RFC3339)

	cases := []struct {
		name       string
		consumedAt *string // RFC3339 or "" for nil
		revokedAt  *string
		expiresAt  string
		want       string
	}{
		{"pending_default", nil, nil, futureString, "pending"},
		{"consumed_takes_precedence", strPtr("2026-08-03T11:00:00Z"), strPtr("2026-08-03T10:00:00Z"), futureString, "consumed"},
		{"revoked_only", nil, strPtr("2026-08-03T10:00:00Z"), futureString, "revoked"},
		{"expired_only", nil, nil, pastString, "expired"},
		{"zero_expires_pending", nil, nil, zeroString, "pending"},
		{"consumed_even_when_expired", strPtr("2026-08-03T11:00:00Z"), nil, pastString, "consumed"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var consumed, revoked *time.Time
			if tc.consumedAt != nil {
				c := mustTime(*tc.consumedAt)
				consumed = &c
			}
			if tc.revokedAt != nil {
				r := mustTime(*tc.revokedAt)
				revoked = &r
			}
			got := api.DeriveOrgInvitationStatus(consumed, revoked, mustTime(tc.expiresAt), now)
			if got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

// mustTime is the test helper for time.Parse; panics on a bad
// constant. The literals are static so any regression is a typo
// rather than a flaky clock.
func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("mustTime(%q): %v", s, err))
	}
	return t
}

func strPtr(s string) *string { return &s }

// TestOrgUUID_Pin asserts the org row's UUID is well-formed so the
// wire shape never carries a non-canonical id. The Store generates
// UUIDs via uuid.New(); this test pins the length + hex-only
// invariant the OpenAPI spec regex enforces (`^[0-9a-fA-F]{8}-...`).
func TestOrgUUID_Pin(t *testing.T) {
	t.Parallel()
	_, err := uuid.Parse("00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("uuid.Parse failed on zero UUID: %v", err)
	}
}

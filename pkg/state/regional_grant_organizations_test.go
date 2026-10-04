package state

// adr: 484
import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func sharedRegionalGrantFixture(t *testing.T, s Store, creator Account, cell string) (App, Org, Account, RegionalReleaseGrant) {
	t.Helper()
	if err := s.MarkAccountEmailVerified(t.Context(), creator.ID); err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateOrg(t.Context(), Org{Slug: "grant-" + uuid.NewString()[:8], Name: "Grant Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(t.Context(), org.ID, creator.ID, OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	issuer, err := s.CreateAccount(t.Context(), "issuer-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkAccountEmailVerified(t.Context(), issuer.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(t.Context(), org.ID, issuer.ID, OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), App{ID: uuid.NewString(), AccountID: creator.ID, OrgID: org.ID,
		Slug: "shared-" + uuid.NewString(), Type: AppTypeApp, Status: AppActive, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	g := grantFixture(t, app, cell)
	g.IssuerAccountID = issuer.ID
	return app, org, issuer, g
}

func runRegionalOrganizationGrantContract(t *testing.T, s Store, cell string, creator Account) {
	grants := s.(RegionalReleaseGrantStore)
	app, org, issuer, g := sharedRegionalGrantFixture(t, s, creator, cell)
	first, err := grants.PutRegionalReleaseGrant(t.Context(), g)
	if err != nil || first.AccountID != creator.ID || first.IssuerAccountID != issuer.ID || first.OrgID != org.ID {
		t.Fatalf("shared registration: %+v %v", first, err)
	}
	if retry, err := grants.PutRegionalReleaseGrant(t.Context(), g); err != nil || !retry.matches(first) || !retry.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("shared replay: %+v %v", retry, err)
	}
	acct, key, err := grants.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash)
	if err != nil || acct.ID != issuer.ID || acct.Plan != api.PlanFree || key.AccountID != issuer.ID || key.OrgID != org.ID || key.AppID != app.ID {
		t.Fatalf("issuer principal: %+v %+v %v", acct, key, err)
	}
	intent, dep := releaseTestIntent(cell, app)
	intent.GlobalAppID, intent.Generation, intent.GrantID = g.GlobalAppID, 1, g.ID
	if _, err := s.(RegionalImageReleaseStore).CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrNotFound) {
		t.Fatal("shared grant bypassed runtime admission gate", err)
	}
	if _, err := grants.GetRegionalReleaseGrant(t.Context(), creator.ID, cell, app.ID, g.ID); err != nil {
		t.Fatal("another current administrator cannot inspect consent", err)
	}
	changed := g
	changed.IssuerAccountID = creator.ID
	if _, err := grants.PutRegionalReleaseGrant(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("issuer identity changed", err)
	}
	for _, role := range []OrgRole{OrgRoleDeveloper, OrgRoleViewer, OrgRoleBilling} {
		t.Run(string(role), func(t *testing.T) {
			member, err := s.CreateAccount(t.Context(), string(role)+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.MarkAccountEmailVerified(t.Context(), member.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.AddOrgMember(t.Context(), org.ID, member.ID, role, nil); err != nil {
				t.Fatal(err)
			}
			other := grantFixture(t, app, cell)
			other.IssuerAccountID = member.ID
			if _, err = grants.PutRegionalReleaseGrant(t.Context(), other); !errors.Is(err, ErrNotFound) {
				t.Fatal("non-administrator issued consent", err)
			}
			if _, err = grants.RevokeRegionalReleaseGrant(t.Context(), member.ID, cell, app.ID, g.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("non-administrator revoked consent", err)
			}
		})
	}
	// The app creator's membership does not authorize this issuer's credential.
	if err = s.TransferOrgOwnership(t.Context(), org.ID, creator.ID, issuer.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveOrgMember(t.Context(), org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = grants.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash); err != nil {
		t.Fatal("creator departure invalidated another member's consent", err)
	}
	// A replacement owner can retire consent after its issuer loses the role.
	replacement, err := s.CreateAccount(t.Context(), "replacement-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkAccountEmailVerified(t.Context(), replacement.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(t.Context(), org.ID, replacement.ID, OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.TransferOrgOwnership(t.Context(), org.ID, issuer.ID, replacement.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateOrgMemberRole(t.Context(), org.ID, issuer.ID, OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, _, err = grants.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatal("demoted issuer authenticated", err)
	}
	if revoked, err := grants.RevokeRegionalReleaseGrant(t.Context(), replacement.ID, cell, app.ID, g.ID); err != nil || revoked.Status != "revoked" {
		t.Fatalf("replacement administrator revocation: %+v %v", revoked, err)
	}
	if err = s.UpdateOrgMemberRole(t.Context(), org.ID, issuer.ID, OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, _, err = grants.AuthenticateRegionalReleaseGrant(t.Context(), g.Hash); !errors.Is(err, ErrAPIKeyRevoked) {
		t.Fatal("role restoration revived revoked consent", err)
	}
}

func TestRegionalOrganizationGrantMemoryContract(t *testing.T) {
	s := NewMemStore()
	creator, err := s.CreateAccount(t.Context(), "shared-grant@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRegionalOrganizationGrantContract(t, s, uuid.NewString(), creator)
}

func runRegionalOrganizationGrantBound(t *testing.T, s Store, cell string, creator Account) {
	t.Helper()
	app, _, issuer, _ := sharedRegionalGrantFixture(t, s, creator, cell)
	grants := s.(RegionalReleaseGrantStore)
	const requests = 40
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		g := grantFixture(t, app, cell)
		g.IssuerAccountID = issuer.ID
		wg.Go(func() { _, err := grants.PutRegionalReleaseGrant(t.Context(), g); errs <- err })
	}
	wg.Wait()
	close(errs)
	accepted := 0
	for err := range errs {
		if err == nil {
			accepted++
			continue
		}
		var bound *RegionalReleaseGrantLimitError
		if !errors.As(err, &bound) || bound.Limit != api.RegionalReleaseGrantsPerAppMax || bound.Observed != api.RegionalReleaseGrantsPerAppMax+1 {
			t.Fatalf("shared concurrent grant bound: %v %+v", err, bound)
		}
	}
	if accepted != api.RegionalReleaseGrantsPerAppMax {
		t.Fatalf("shared accepted %d", accepted)
	}
}

func TestRegionalOrganizationGrantMemoryBound(t *testing.T) {
	s := NewMemStore()
	creator, err := s.CreateAccount(t.Context(), "shared-bound@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRegionalOrganizationGrantBound(t, s, uuid.NewString(), creator)
}

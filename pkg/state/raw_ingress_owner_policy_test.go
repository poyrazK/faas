// adr: 20261104170000
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type rawListenerPolicyStore interface {
	internalPortListenerStore
	state.AccountAbuseHoldStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateOrg(context.Context, state.Org) (state.Org, error)
	OrgByID(context.Context, string) (state.Org, error)
	UpdateOrgPlan(context.Context, string, api.Plan) error
	AddOrgMember(context.Context, string, string, state.OrgRole, *string) error
	UpdateAccountStatus(context.Context, string, state.AccountStatus) error
	UpdateOrgStatus(context.Context, string, state.OrgStatus) error
	TCPListenerByAppAndName(context.Context, string, string) (state.TCPListener, error)
	UDPListenerByAppAndName(context.Context, string, string) (state.UDPListener, error)
}

func rawListenerOwnerPolicyCases(t *testing.T, s rawListenerPolicyStore) {
	t.Helper()
	for i, tc := range []struct {
		name                          string
		company, adopted              bool
		creator                       state.AccountStatus
		org                           state.OrgStatus
		holdCreator, holdOrg, allowed bool
	}{
		{name: "personal-active", allowed: true},
		{name: "personal-grace", creator: state.AccountPastDue, allowed: true},
		{name: "personal-suspended", creator: state.AccountSuspended},
		{name: "personal-deleted-pending", creator: state.AccountDeletedPending},
		{name: "personal-held", holdCreator: true},
		{name: "company-creator-suspended", company: true, adopted: true, creator: state.AccountSuspended, allowed: true},
		{name: "company-creator-held", company: true, adopted: true, holdCreator: true, allowed: true},
		{name: "company-suspended", company: true, adopted: true, org: state.OrgStatusSuspended},
		{name: "company-held", company: true, adopted: true, holdOrg: true},
		{name: "company-grace", company: true, adopted: true, creator: state.AccountSuspended, org: state.OrgStatusPastDue, allowed: true},
		{name: "legacy-creator-suspended", company: true, creator: state.AccountSuspended},
		{name: "legacy-company-suspended", company: true, org: state.OrgStatusSuspended},
		{name: "legacy-company-held", company: true, holdOrg: true},
		{name: "legacy-company-grace", company: true, org: state.OrgStatusPastDue, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			suffix := uuid.NewString()[:8]
			account, err := s.CreateAccount(ctx, "raw-policy-"+suffix+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			var orgID string
			if tc.company {
				plan := api.PlanPro
				if tc.adopted {
					plan = api.PlanFree // Free companies adopt resource ownership on creation.
				}
				org, err := s.CreateOrg(ctx, state.Org{Slug: "raw-policy-" + suffix, Name: "Raw policy", Plan: plan, Status: state.OrgStatusActive})
				if err != nil {
					t.Fatal(err)
				}
				orgID = org.ID
				if tc.adopted {
					if err := s.UpdateOrgPlan(ctx, orgID, api.PlanPro); err != nil {
						t.Fatal(err)
					}
				}
				if org, err := s.OrgByID(ctx, orgID); err != nil || org.ResourceOwnershipEnabled != tc.adopted {
					t.Fatalf("company adoption=%v want=%v err=%v", org.ResourceOwnershipEnabled, tc.adopted, err)
				}
				if err := s.AddOrgMember(ctx, orgID, account.ID, state.OrgRoleOwner, nil); err != nil {
					t.Fatal(err)
				}
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, OrgID: orgID, Slug: "raw-policy-" + suffix, Status: state.AppActive, RAMMB: 256,
				Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "tcp", Port: 9000}, {Name: "udp", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
			if err != nil {
				t.Fatal(err)
			}
			tcp, err := s.CreateTCPListener(ctx, state.TCPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "tcp", GuestPort: 9000, PublicPort: 40200 + i, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			udp, err := s.CreateUDPListener(ctx, state.UDPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "udp", GuestPort: 5353, PublicPort: 40200 + i, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if tc.creator != "" {
				if err := s.UpdateAccountStatus(ctx, account.ID, tc.creator); err != nil {
					t.Fatal(err)
				}
			}
			if tc.org != "" {
				if err := s.UpdateOrgStatus(ctx, orgID, tc.org); err != nil {
					t.Fatal(err)
				}
			}
			for _, owner := range []struct {
				id   string
				held bool
			}{{account.ID, tc.holdCreator}, {orgID, tc.holdOrg}} {
				if owner.held {
					if _, err := s.SetAccountAbuseHold(ctx, owner.id, state.AccountAbuseHoldOperator, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			check := func(allowed bool) {
				t.Helper()
				_, tcpErr := s.TCPListenerByPublicPort(ctx, tcp.PublicPort)
				_, udpErr := s.UDPListenerByPublicPort(ctx, udp.PublicPort)
				if allowed && (tcpErr != nil || udpErr != nil) || !allowed && (!errors.Is(tcpErr, state.ErrNotFound) || !errors.Is(udpErr, state.ErrNotFound)) {
					t.Fatalf("allowed=%v TCP=%v UDP=%v", allowed, tcpErr, udpErr)
				}
				tcps, err := s.ListEnabledTCPListeners(ctx)
				if err != nil {
					t.Fatal(err)
				}
				udps, err := s.ListEnabledUDPListeners(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var haveTCP, haveUDP bool
				for _, row := range tcps {
					haveTCP = haveTCP || row.ID == tcp.ID
				}
				for _, row := range udps {
					haveUDP = haveUDP || row.ID == udp.ID
				}
				if haveTCP != allowed || haveUDP != allowed {
					t.Fatalf("inventory TCP=%v UDP=%v want=%v", haveTCP, haveUDP, allowed)
				}
			}
			check(tc.allowed)
			// A hold never deletes the saved listener or prevents management reads.
			if row, err := s.TCPListenerByAppAndName(ctx, app.ID, "tcp"); err != nil || row.ID != tcp.ID {
				t.Fatal("TCP intent disappeared", err)
			}
			if row, err := s.UDPListenerByAppAndName(ctx, app.ID, "udp"); err != nil || row.ID != udp.ID {
				t.Fatal("UDP intent disappeared", err)
			}
			if err := s.UpdateAccountStatus(ctx, account.ID, state.AccountActive); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReleaseAccountAbuseHold(ctx, account.ID); err != nil {
				t.Fatal(err)
			}
			if orgID != "" {
				if err := s.UpdateOrgStatus(ctx, orgID, state.OrgStatusActive); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ReleaseAccountAbuseHold(ctx, orgID); err != nil {
					t.Fatal(err)
				}
			}
			check(true)
		})
	}
}

func TestMemRawListenerResourceOwnerPolicy(t *testing.T) {
	rawListenerOwnerPolicyCases(t, state.NewMemStore())
}

func TestPgRawListenerResourceOwnerPolicy(t *testing.T) {
	s, _ := pgStore(t)
	rawListenerOwnerPolicyCases(t, s)
}

package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

func TestMemApplicationStandardExceptionAuthority(t *testing.T) {
	standardExceptionAuthority(t, NewMemStore())
}
func TestPgApplicationStandardExceptionAuthority(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardExceptionAuthority(t, s)
}

func standardExceptionAuthority(t *testing.T, s standardExceptionTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	r := standardExceptionRequest(f)
	other := newStandardExceptionMember(t, s, f)
	for _, role := range []OrgRole{OrgRoleViewer, OrgRoleBilling, OrgRoleDeveloper} {
		if err := s.UpdateOrgMemberRole(ctx, f.owner.PersonalOrg.ID, other.Account.ID, role); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, other.Account.ID, f.app.ID, r); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
			t.Fatalf("%s approved exception: %v", role, err)
		}
	}
	if _, err := s.ApproveApplicationStandardException(ctx, other.PersonalOrg.ID, other.Account.ID, f.app.ID, r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign app approved: %v", err)
	}
	if _, err := s.ListApplicationStandardExceptions(ctx, other.PersonalOrg.ID, f.app.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign history disclosed: %v", err)
	}
	if err := s.UpdateOrgMemberRole(ctx, f.owner.PersonalOrg.ID, other.Account.ID, OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountStatus(ctx, other.Account.ID, AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, other.Account.ID, f.app.ID, r); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
		t.Fatalf("suspended admin approved: %v", err)
	}
	if err := s.UpdateAccountStatus(ctx, other.Account.ID, AccountActive); err != nil {
		t.Fatal(err)
	}
	x, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, other.Account.ID, f.app.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveOrgMember(ctx, f.owner.PersonalOrg.ID, other.Account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeApplicationStandardException(ctx, f.owner.PersonalOrg.ID, other.Account.ID, f.app.ID, x.ID, r.ExpectedRevision+1); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
		t.Fatalf("removed admin revoked: %v", err)
	}
	if _, err := s.RevokeApplicationStandardException(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, x.ID, r.ExpectedRevision+1); err != nil {
		t.Fatal(err)
	}
}

func TestMemApplicationStandardExceptionBlockedRepair(t *testing.T) {
	standardExceptionBlockedRepair(t, NewMemStore())
}
func TestPgApplicationStandardExceptionBlockedRepair(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardExceptionBlockedRepair(t, s)
}

func standardExceptionBlockedRepair(t *testing.T, s standardExceptionTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	r := standardExceptionRequest(f)
	r.Field = appstandards.EgressExtraPorts
	r.Value = json.RawMessage(`[7443,8443]`)
	r.ExpiresAt = time.Now().UTC().Add(2 * time.Second)
	x, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	if _, err := s.SetApplicationStandardLocalIntent(ctx, x.OrgID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{"egress_extra_ports":[7443]}`)}); err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	waitStandardExceptionExpiry(t, x)
	c, err := s.ClaimApplicationStandardEnrollment(ctx, "exception-blocked-repair")
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := s.MaterializeApplicationStandardEnrollment(ctx, c)
	if err != nil || blocked.State != "blocked" || blocked.PersistedRevision != f.enrollment.PersistedRevision || string(blocked.LocalSettings[r.Field]) != `[7443]` || ApplicationStandardEnrollmentPermitsRuntime(f.app, blocked) {
		t.Fatalf("expiry weakened local constraint: %+v %v", blocked, err)
	}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, x.OrgID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: blocked.DesiredRevision, Settings: json.RawMessage(`{"egress_extra_ports":[7443]}`)}); !errors.Is(err, ErrApplicationStandardReviewBlocked) {
		t.Fatalf("blocked weakening allowed: %v", err)
	}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, x.OrgID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: blocked.DesiredRevision, Settings: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("corrective local intent refused: %v", err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	if f.enrollment.ExceptionExpiresAt != nil || len(f.enrollment.LocalSettings) != 0 {
		t.Fatalf("corrective projection retained exception: %+v", f.enrollment)
	}
}

func waitStandardExceptionExpiry(t *testing.T, x ApplicationStandardException) {
	t.Helper()
	timer := time.NewTimer(time.Until(x.ExpiresAt))
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	case <-timer.C:
	}
}

func TestMemApplicationStandardExceptionReviewBinding(t *testing.T) {
	standardExceptionReviewBinding(t, NewMemStore())
}
func TestPgApplicationStandardExceptionReviewBinding(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardExceptionReviewBinding(t, s)
}

func standardExceptionReviewBinding(t *testing.T, s standardExceptionTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	r := ApplicationStandardReviewRequest{AssignmentID: f.assignmentID, ExpectedRevision: 1, Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1}
	before, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	xr := standardExceptionRequest(f)
	xr.ExpiresAt = time.Now().UTC().Add(time.Minute)
	x, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, xr)
	if err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	after, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, r)
	if err != nil || len(after.Blockers) != 0 || !after.ExpiresAt.Equal(x.ExpiresAt) || before.ApprovalHash == after.ApprovalHash {
		t.Fatalf("review omitted exception: %+v %v", after, err)
	}
	if _, err := s.ValidateApplicationStandardReview(ctx, before.OrgID, f.owner.Account.ID, before.ID, before.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewStale) {
		t.Fatalf("pre-exception preview stayed fresh: %v", err)
	}
	if _, err := s.RevokeApplicationStandardException(ctx, x.OrgID, f.owner.Account.ID, x.AppID, x.ID, f.enrollment.DesiredRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateApplicationStandardReview(ctx, after.OrgID, f.owner.Account.ID, after.ID, after.ApprovalHash); !errors.Is(err, ErrApplicationStandardReviewStale) {
		t.Fatalf("revoked preview stayed fresh: %v", err)
	}
}

func TestApplicationStandardExceptionIndependentConstraint(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	m.mu.Lock()
	s, err := m.standardReviewSnapshotLocked(t.Context(), f.owner.PersonalOrg.ID, f.owner.Account.ID, ApplicationStandardReviewRequest{Scope: "application", ScopeID: canonicalStandardUUID(f.app.ID), StandardID: f.version.StandardID})
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	x, err := prepareStandardException(f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, standardExceptionRequest(f))
	if err != nil {
		t.Fatal(err)
	}
	x.CreatedAt = time.Now().UTC()
	standardID, assignmentID := uuid.NewString(), uuid.NewString()
	raw := mustStandardLocalJSON(appstandards.Definition{appstandards.LogDestinations: {Mode: appstandards.Mandatory, Value: mustStandardLocalJSON([]string{f.company.ID})}})
	_, hash, err := appstandards.Parse(raw, api.ApplicationStandardResolverLimits())
	if err != nil {
		t.Fatal(err)
	}
	s.Versions = append(s.Versions, standardReviewVersion{StandardID: standardID, Version: 1, Definition: raw, DefinitionHash: hash})
	s.Assignments = append(s.Assignments, standardReviewAssignment{Assignment: appstandards.Assignment{ID: assignmentID, OrgID: s.OrgID, Scope: "organization", ScopeID: s.OrgID, StandardID: standardID, AdmissionVersion: 1}, Revision: 1, Active: true})
	s.Applications[0].Enrollment.Adoptions = append(s.Applications[0].Enrollment.Adoptions, appstandards.Adoption{AssignmentID: assignmentID, Version: 1})
	if err := validateStandardException(s, f.enrollment, x, f.enrollment.DesiredRevision, time.Now()); !errors.Is(err, ErrApplicationStandardReviewBlocked) {
		t.Fatalf("exception bypassed independent requirement: %v", err)
	}
}

func newStandardExceptionMember(t *testing.T, s standardExceptionTestStore, f standardLocalIntentFixture) CreateAccountWithPersonalOrgResult {
	t.Helper()
	other, err := s.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: "exception-member@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddOrgMember(t.Context(), f.owner.PersonalOrg.ID, other.Account.ID, OrgRoleViewer, nil); err != nil {
		t.Fatal(err)
	}
	return other
}

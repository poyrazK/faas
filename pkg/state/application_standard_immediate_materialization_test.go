package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type standardImmediateTestStore interface {
	standardAutomaticTestStore
	ApplicationStandardImmediateMaterializationStore
}

func TestMemApplicationStandardImmediateClaim(t *testing.T) {
	standardImmediateClaim(t, NewMemStore())
}

func standardImmediateClaim(t *testing.T, s standardImmediateTestStore) {
	t.Helper()
	ctx := t.Context()
	f := standardAutomaticSetup(t, s, false, false)
	first := automaticCreate(t, s, f, "immediate-oldest")
	second := automaticCreate(t, s, f, "immediate-selected")
	request := ApplicationStandardEnrollmentClaimRequest{OrgID: f.owner.PersonalOrg.ID, AppID: second.ID, DesiredRevision: 1, Owner: "interactive-owner"}
	for _, tc := range []struct {
		name string
		edit func(*ApplicationStandardEnrollmentClaimRequest)
		want error
	}{
		{"invalid owner", func(r *ApplicationStandardEnrollmentClaimRequest) { r.Owner = "" }, ErrInvalidArgument},
		{"invalid app", func(r *ApplicationStandardEnrollmentClaimRequest) { r.AppID = "not-a-uuid" }, ErrInvalidArgument},
		{"invalid revision", func(r *ApplicationStandardEnrollmentClaimRequest) { r.DesiredRevision = 0 }, ErrInvalidArgument},
		{"foreign organization", func(r *ApplicationStandardEnrollmentClaimRequest) { r.OrgID = uuid.NewString() }, ErrNotFound},
		{"changed desired revision", func(r *ApplicationStandardEnrollmentClaimRequest) { r.DesiredRevision++ }, ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request
			tc.edit(&r)
			if _, err := s.ClaimApplicationStandardEnrollmentForApp(ctx, r); !errors.Is(err, tc.want) {
				t.Fatalf("claim: got %v, want %v", err, tc.want)
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ClaimApplicationStandardEnrollmentForApp(canceled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled claim: %v", err)
	}
	request.AppID = strings.ToUpper(strings.ReplaceAll(second.ID, "-", ""))
	request.OrgID = strings.ToUpper(request.OrgID)
	start := make(chan struct{})
	results := make(chan ApplicationStandardEnrollmentClaim, 8)
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			<-start
			claim, err := s.ClaimApplicationStandardEnrollmentForApp(ctx, request)
			if err != nil {
				failures <- err
			} else {
				results <- claim
			}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	if len(results) != 1 || len(failures) != 7 {
		t.Fatalf("exclusive targeted lease: successes=%d failures=%d", len(results), len(failures))
	}
	for err := range failures {
		if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	held := <-results
	if held.AppID != canonicalStandardUUID(second.ID) || held.OrgID != canonicalStandardUUID(f.owner.PersonalOrg.ID) || held.DesiredRevision != 1 {
		t.Fatalf("claim lost canonical target: %+v", held)
	}
	other, err := s.ClaimApplicationStandardEnrollment(ctx, "background-owner")
	if err != nil || other.AppID != canonicalStandardUUID(first.ID) {
		t.Fatalf("interactive claim consumed another service: %+v %v", other, err)
	}
	if err := s.ReleaseApplicationStandardEnrollmentWorker(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseApplicationStandardEnrollmentWorker(ctx, held); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimApplicationStandardEnrollmentForApp(ctx, request)
	if err != nil || next.Generation <= held.Generation {
		t.Fatalf("retry lost generation fence: %+v %v", next, err)
	}
	if err := s.ReleaseApplicationStandardEnrollmentWorker(ctx, held); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("old release revoked replacement: %v", err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, held); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("old worker installed over replacement: %v", err)
	}
	e, err := s.MaterializeApplicationStandardEnrollment(ctx, next)
	if err != nil || e.State != "persisted" || e.DesiredRevision != 1 || e.PersistedRevision != 1 || e.ObservedRevision != 0 || len(e.MaterializedFields) != 6 {
		t.Fatalf("targeted installation: %+v %v", e, err)
	}
	if _, err := s.ClaimApplicationStandardEnrollmentForApp(ctx, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("installed service was claimed again: %v", err)
	}
	unchanged, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, first.ID)
	if err != nil || unchanged.State != "pending" || unchanged.DesiredRevision != 1 || unchanged.PersistedRevision != 0 {
		t.Fatalf("unrelated intent was installed: %+v %v", unchanged, err)
	}
}

func TestMemApplicationStandardImmediateReviewPrecedence(t *testing.T) {
	standardImmediateReviewPrecedence(t, NewMemStore())
}

func standardImmediateReviewPrecedence(t *testing.T, s standardImmediateTestStore) {
	t.Helper()
	ctx := t.Context()
	f := standardAutomaticSetup(t, s, false, true)
	app := automaticCreate(t, s, f, "immediate-review")
	r := ApplicationStandardEnrollmentClaimRequest{OrgID: app.OrgID, AppID: app.ID, DesiredRevision: 1, Owner: "interactive-before-approval"}
	held, err := s.ClaimApplicationStandardEnrollmentForApp(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: app.OrgID, ActorID: f.owner.Account.ID, Slug: f.version.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"security_policy":{"mode":"default","value":"off"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := s.ListApplicationStandardAssignments(ctx, app.OrgID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("assignment: %+v %v", assignments, err)
	}
	p, err := s.PreviewApplicationStandardAssignment(ctx, app.OrgID, f.owner.Account.ID, ApplicationStandardReviewRequest{AssignmentID: assignments[0].ID, ExpectedRevision: 1, Scope: "organization", ScopeID: app.OrgID, StandardID: v.StandardID, AdmissionVersion: 2, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("review: %+v %v", p.Blockers, err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, held); !errors.Is(err, ErrApplicationStandardOperationInProgress) {
		t.Fatalf("interactive worker overtook approved target: %v", err)
	}
	if _, err := s.ClaimApplicationStandardEnrollmentForApp(ctx, r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("queued reviewed target was claimed: %v", err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "reviewed-owner")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || o.State != "waiting" || len(o.Targets) != 1 || o.Targets[0].State != "persisted" {
		t.Fatalf("approved projection did not retain authority: %+v %v", o, err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if err != nil || e.Adoptions[0].Version != 2 || e.DesiredRevision != 2 || e.ObservedRevision != 0 {
		t.Fatalf("reviewed version: %+v %v", e, err)
	}
}

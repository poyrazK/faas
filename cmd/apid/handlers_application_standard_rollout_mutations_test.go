package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestApplicationStandardRolloutAPIReviewedRollback(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithApplicationStandardMutationsEnabled(true)
	org, _, req := standardReviewAPIFixture(t, e)
	ctx := t.Context()
	if _, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "second-preview-service", RAMMB: 128, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(e.h)
	defer ts.Close()
	client := api.NewClient(ts.URL, e.key)
	if _, err := client.PublishApplicationStandardVersion(ctx, org.Slug, "preview-baseline", api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"egress_cidrs":{"mode":"restricted","value":["8.8.8.0/24"]},"egress_extra_ports":{"mode":"mandatory","value":[8443]}}`)}); err != nil {
		t.Fatal(err)
	}
	req.AdmissionVersion = 2
	plan, err := client.PreviewApplicationStandardAssignment(ctx, org.Slug, req)
	if err != nil || len(plan.Applications) != 2 || len(plan.Blockers) != 0 {
		t.Fatalf("forward preview: %+v %v", plan, err)
	}
	op, err := client.ApproveApplicationStandardReview(ctx, org.Slug, plan.ID, api.ApproveApplicationStandardReviewRequest{ApprovalHash: plan.ApprovalHash})
	if err != nil || op.State != "queued" || len(op.Targets) != 2 {
		t.Fatalf("approval: %+v %v", op, err)
	}
	old := op.UpdatedAt
	op, err = client.PauseApplicationStandardOperation(ctx, org.Slug, op.ID, api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: old})
	if err != nil || op.State != "paused" || !op.UpdatedAt.After(old) {
		t.Fatalf("pause: %+v %v", op, err)
	}
	assertProblem(t, e.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/application-standard-operations/"+op.ID+"/resume", api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: old}, nil), http.StatusConflict, api.CodeApplicationStandardVersionStale)
	if _, err := e.store.ClaimApplicationStandardOperation(ctx, "paused-api-worker"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("paused operation claim: %v", err)
	}
	op, err = client.ResumeApplicationStandardOperation(ctx, org.Slug, op.ID, api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: op.UpdatedAt})
	if err != nil || op.State != "running" {
		t.Fatalf("resume: %+v %v", op, err)
	}
	claim, err := e.store.ClaimApplicationStandardOperation(ctx, "forward-api-worker")
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := e.store.MaterializeNextApplicationStandardTarget(ctx, claim)
	if err != nil || persisted.Targets[0].State != "persisted" || persisted.Targets[1].State != "queued" {
		t.Fatalf("partial installation: %+v %v", persisted, err)
	}
	aborted, err := client.AbortApplicationStandardOperation(ctx, org.Slug, op.ID, api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: persisted.UpdatedAt})
	if err != nil || aborted.State != "failed" || aborted.ErrorCode != "operator_aborted" || aborted.Targets[0].State != "persisted" || aborted.Targets[1].State != "skipped" {
		t.Fatalf("abort erased installed truth: %+v %v", aborted, err)
	}
	installed, err := e.store.AppByID(ctx, aborted.Targets[0].AppID)
	if err != nil || !reflect.DeepEqual(installed.EgressPorts, []int{8443}) {
		t.Fatalf("abort reversed an installed target: %+v %v", installed, err)
	}
	rollbackRequest := plan.Request
	rollbackRequest.AdmissionVersion, rollbackRequest.ExpectedRevision, rollbackRequest.BatchSize = 1, 1, 2
	rollback, err := client.PreviewApplicationStandardAssignment(ctx, org.Slug, rollbackRequest)
	if err != nil || len(rollback.Blockers) != 0 || rollback.ApprovalHash == plan.ApprovalHash {
		t.Fatalf("rollback reused authority: %+v %v", rollback, err)
	}
	recovery, err := client.ApproveApplicationStandardReview(ctx, org.Slug, rollback.ID, api.ApproveApplicationStandardReviewRequest{ApprovalHash: rollback.ApprovalHash})
	if err != nil || recovery.ID == aborted.ID {
		t.Fatalf("reviewed rollback: %+v %v", recovery, err)
	}
	claim, err = e.store.ClaimApplicationStandardOperation(ctx, "rollback-api-worker")
	if err != nil || claim.OperationID != recovery.ID {
		t.Fatalf("rollback claim: %+v %v", claim, err)
	}
	for range recovery.Targets {
		if _, err = e.store.MaterializeNextApplicationStandardTarget(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	recovery, err = client.GetApplicationStandardOperation(ctx, org.Slug, recovery.ID)
	if err != nil || recovery.State != "waiting" {
		t.Fatalf("rollback fabricated convergence: %+v %v", recovery, err)
	}
	for _, target := range recovery.Targets {
		app, err := e.store.AppByID(ctx, target.AppID)
		if err != nil || len(app.EgressPorts) != 0 || target.State != "persisted" {
			t.Fatalf("rollback projection: %+v %+v %v", target, app, err)
		}
		enrollment, err := client.GetApplicationStandardEnrollment(ctx, org.Slug, target.AppID)
		if err != nil || enrollment.ObservedRevision != 0 || len(enrollment.Adoptions) != 1 || enrollment.Adoptions[0].Version != 1 {
			t.Fatalf("rollback adoption: %+v %v", enrollment, err)
		}
	}
	retained, err := client.GetApplicationStandardOperation(ctx, org.Slug, aborted.ID)
	if err != nil || !reflect.DeepEqual(retained, aborted) {
		t.Fatalf("rollback rewrote history: %+v %v", retained, err)
	}
	assertStandardMutationResponsePrivate(t, e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/application-standard-operations/"+recovery.ID, nil, nil).Body.String())
}

func TestApplicationStandardRolloutReleaseGate(t *testing.T) {
	e := setup(t, api.PlanPro)
	org, _, req := standardReviewAPIFixture(t, e)
	plan, err := e.store.PreviewApplicationStandardAssignment(t.Context(), org.ID, e.acct.ID, state.ApplicationStandardReviewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/orgs/" + org.Slug
	for _, path := range []string{"/application-standard-reviews/" + plan.ID + "/approve", "/application-standard-operations/" + uuid.NewString() + "/pause", "/application-standard-operations/" + uuid.NewString() + "/resume", "/application-standard-operations/" + uuid.NewString() + "/abort"} {
		assertProblem(t, e.do(t, http.MethodPost, base+path, json.RawMessage(`{}`), nil), http.StatusServiceUnavailable, api.CodeApplicationStandardsPending)
	}
	assignments, err := e.store.ListApplicationStandardAssignments(t.Context(), org.ID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("gate activated assignment: %+v %v", assignments, err)
	}
	if got := e.do(t, http.MethodGet, base+"/application-standard-reviews/"+plan.ID, nil, nil); got.Code != http.StatusOK {
		t.Fatalf("gate disabled inspection: %d %s", got.Code, got.Body)
	}
}

func TestApplicationStandardRolloutAuthorityBeforeReplay(t *testing.T) {
	for _, action := range []string{"approve", "pause", "resume", "abort"} {
		t.Run(action, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			e.s.WithApplicationStandardMutationsEnabled(true)
			org, _, req := standardReviewAPIFixture(t, e)
			plan, err := e.store.PreviewApplicationStandardAssignment(t.Context(), org.ID, e.acct.ID, state.ApplicationStandardReviewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			actor, err := e.store.CreateAccount(t.Context(), "rollout-admin@example.com", api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.store.AddOrgMember(t.Context(), org.ID, actor.ID, state.OrgRoleAdmin, nil); err != nil {
				t.Fatal(err)
			}
			read := standardAPIReader(t, e, actor, []string{api.ScopeDeployWrite})
			path := "/v1/orgs/" + org.Slug + "/application-standard-reviews/" + plan.ID + "/approve"
			var body any = api.ApproveApplicationStandardReviewRequest{ApprovalHash: plan.ApprovalHash}
			status := http.StatusCreated
			if action != "approve" {
				op, err := e.store.ApproveApplicationStandardReview(t.Context(), org.ID, e.acct.ID, plan.ID, plan.ApprovalHash)
				if err != nil {
					t.Fatal(err)
				}
				if action == "resume" {
					op, err = e.store.ControlApplicationStandardOperation(t.Context(), org.ID, e.acct.ID, op.ID, op.UpdatedAt, state.ApplicationStandardOperationPause)
					if err != nil {
						t.Fatal(err)
					}
				}
				path = "/v1/orgs/" + org.Slug + "/application-standard-operations/" + op.ID + "/" + action
				body, status = api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: op.UpdatedAt}, http.StatusOK
			}
			headers := map[string]string{"Idempotency-Key": "rollout-replay"}
			first := read.do(t, http.MethodPost, path, body, headers)
			if first.Code != status {
				t.Fatalf("first: %d %s", first.Code, first.Body)
			}
			second := read.do(t, http.MethodPost, path, body, headers)
			if second.Code != status || second.Body.String() != first.Body.String() {
				t.Fatalf("replay: %d %s", second.Code, second.Body)
			}
			assertStandardMutationResponsePrivate(t, first.Body.String())
			e.s.WithApplicationStandardMutationsEnabled(false)
			assertProblem(t, read.do(t, http.MethodPost, path, body, headers), http.StatusServiceUnavailable, api.CodeApplicationStandardsPending)
			e.s.WithApplicationStandardMutationsEnabled(true)
			for _, role := range []state.OrgRole{state.OrgRoleDeveloper, state.OrgRoleViewer, state.OrgRoleBilling} {
				if err = e.store.UpdateOrgMemberRole(t.Context(), org.ID, actor.ID, role); err != nil {
					t.Fatal(err)
				}
				assertProblem(t, read.do(t, http.MethodPost, path, body, headers), http.StatusForbidden, api.CodeOrgRoleForbidden)
			}
			if err = e.store.RemoveOrgMember(t.Context(), org.ID, actor.ID); err != nil {
				t.Fatal(err)
			}
			if got := read.do(t, http.MethodPost, path, body, headers); got.Code == status {
				t.Fatal("removed member replayed a successful mutation")
			}
		})
	}
}

func TestApplicationStandardRolloutScopeAndFreshness(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.WithApplicationStandardMutationsEnabled(true)
	org, _, req := standardReviewAPIFixture(t, e)
	plan, err := e.store.PreviewApplicationStandardAssignment(t.Context(), org.ID, e.acct.ID, state.ApplicationStandardReviewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/orgs/" + org.Slug + "/application-standard-reviews/"
	body := api.ApproveApplicationStandardReviewRequest{ApprovalHash: plan.ApprovalHash}
	reader := standardAPIReader(t, e, e.acct, []string{api.ScopeAppsRead})
	assertProblem(t, reader.do(t, http.MethodPost, base+plan.ID+"/approve", body, nil), http.StatusForbidden, api.CodeForbidden)
	other := seedSharedOrgWithOwner(t, e, "rollout-foreign", "Foreign", api.PlanPro)
	assertProblem(t, e.do(t, http.MethodPost, "/v1/orgs/"+other.Slug+"/application-standard-reviews/"+plan.ID+"/approve", body, nil), http.StatusNotFound, api.CodeNotFound)
	for _, id := range []string{"bad", uuid.Nil.String()} {
		assertProblem(t, e.do(t, http.MethodPost, base+id+"/approve", body, nil), http.StatusBadRequest, api.CodeValidation)
	}
	assertProblem(t, e.do(t, http.MethodPost, base+plan.ID+"/approve", api.ApproveApplicationStandardReviewRequest{ApprovalHash: strings.Repeat("a", 64)}, nil), http.StatusConflict, api.CodeApplicationStandardVersionStale)
	if _, err = e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, OrgID: org.ID, Slug: "review-membership-changed", RAMMB: 128, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	assertProblem(t, e.do(t, http.MethodPost, base+plan.ID+"/approve", body, nil), http.StatusConflict, api.CodeApplicationStandardVersionStale)
	assignments, err := e.store.ListApplicationStandardAssignments(t.Context(), org.ID)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("stale approval wrote assignment: %+v %v", assignments, err)
	}
	fresh, err := e.store.PreviewApplicationStandardAssignment(t.Context(), org.ID, e.acct.ID, state.ApplicationStandardReviewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	op, err := e.store.ApproveApplicationStandardReview(t.Context(), org.ID, e.acct.ID, fresh.ID, fresh.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"pause", "resume", "abort"} {
		path := "/v1/orgs/" + other.Slug + "/application-standard-operations/" + op.ID + "/" + action
		assertProblem(t, e.do(t, http.MethodPost, path, api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: op.UpdatedAt}, nil), http.StatusNotFound, api.CodeNotFound)
		path = "/v1/orgs/" + org.Slug + "/application-standard-operations/" + op.ID + "/" + action
		assertProblem(t, reader.do(t, http.MethodPost, path, api.ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: op.UpdatedAt}, nil), http.StatusForbidden, api.CodeForbidden)
	}
}

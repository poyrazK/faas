package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type standardOperationControlTestStore interface {
	standardMaterializationTestStore
	ApplicationStandardOperationControlStore
}

func TestMemApplicationStandardOperationControls(t *testing.T) {
	standardOperationControlsLifecycle(t, NewMemStore())
}

func standardReadOperation(ctx context.Context, t *testing.T, s standardOperationTestStore, o ApplicationStandardOperation) ApplicationStandardOperation {
	t.Helper()
	got, err := s.GetApplicationStandardOperation(ctx, o.OrgID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func standardControlOperation(t *testing.T, s standardOperationControlTestStore, o ApplicationStandardOperation, actorID string, action ApplicationStandardOperationAction, want string) ApplicationStandardOperation {
	t.Helper()
	got, err := s.ControlApplicationStandardOperation(context.Background(), strings.ToUpper(o.OrgID), strings.ReplaceAll(actorID, "-", ""), o.ID, o.UpdatedAt, action)
	if err != nil || got.State != want || !got.UpdatedAt.After(o.UpdatedAt) {
		t.Fatalf("%s: state=%s time=%v err=%v", action, got.State, got.UpdatedAt, err)
	}
	for i := range o.Targets {
		if !standardControlValueEqual(got.Targets[i].ApprovedApp, o.Targets[i].ApprovedApp) || !standardControlValueEqual(got.Targets[i].approvalInput, o.Targets[i].approvalInput) {
			t.Fatal("operator control rewrote approved intent")
		}
	}
	return got
}

func standardControlValueEqual(a, b any) bool {
	x, xErr := standardControlJSONValue(a)
	y, yErr := standardControlJSONValue(b)
	if xErr != nil || yErr != nil {
		return false
	}
	if !reflect.DeepEqual(x, y) {
		for _, difference := range standardControlValueDifferences(x, y, "") {
			fmt.Println("control comparison:", difference)
		}
		return false
	}
	return true
}

func standardControlValueDifferences(a, b any, path string) []string {
	if reflect.DeepEqual(a, b) {
		return nil
	}
	if left, ok := a.(map[string]any); ok {
		if right, ok := b.(map[string]any); ok {
			keys := []string{}
			for k := range left {
				keys = append(keys, k)
			}
			for k := range right {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			out := []string{}
			for _, k := range slices.Compact(keys) {
				out = append(out, standardControlValueDifferences(left[k], right[k], path+"."+k)...)
			}
			return out
		}
	}
	if left, ok := a.([]any); ok {
		if right, ok := b.([]any); ok && len(left) == len(right) {
			out := []string{}
			for i := range left {
				out = append(out, standardControlValueDifferences(left[i], right[i], fmt.Sprintf("%s[%d]", path, i))...)
			}
			return out
		}
	}
	return []string{fmt.Sprintf("%s: %v != %v", path, a, b)}
}

func standardControlJSONValue(a any) (any, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber() // Preserve integer identities and revisions exactly.
	var value any
	err = decoder.Decode(&value)
	return value, err
}

func standardControlOperationEqual(a, b ApplicationStandardOperation) bool {
	if !standardControlValueEqual(a, b) {
		return false
	}
	for i := range a.Targets {
		if !standardControlValueEqual(a.Targets[i].approvalInput, b.Targets[i].approvalInput) {
			return false
		}
	}
	return true
}

func standardOperationControlsLifecycle(t *testing.T, s standardOperationControlTestStore) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	o, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "control-worker-before-pause")
	if err != nil {
		t.Fatal(err)
	}
	o = standardReadOperation(ctx, t, s, o)
	o = standardPauseResumeOperation(t, s, f, o, c)
	next, err := s.ClaimApplicationStandardOperation(ctx, "control-worker-after-resume")
	if err != nil || next.Generation <= c.Generation || next.OperationID != o.ID {
		t.Fatalf("fresh resumed authority: %+v %v", next, err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(ctx, next)
	if err != nil || o.State != "waiting" || o.Targets[0].State != "persisted" || o.Targets[1].State != "queued" {
		t.Fatalf("first wave: %+v %v", o, err)
	}
	o = standardPauseResumeOperation(t, s, f, o, next)
	if o.State != "waiting" {
		t.Fatal("resume skipped real consumer acknowledgments")
	}
	standardAbortOperation(t, s, f, o, next)
	standardOperationControlAuditCount(ctx, t, s, 5)
}

func standardPauseResumeOperation(t *testing.T, s standardOperationControlTestStore, f standardApprovalFixture, o ApplicationStandardOperation, old ApplicationStandardWorkerClaim) ApplicationStandardOperation {
	t.Helper()
	ctx := context.Background()
	before := o
	o = standardControlOperation(t, s, o, f.owner.Account.ID, ApplicationStandardOperationPause, "paused")
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("paused worker retained authority: %v", err)
	}
	if _, err := s.ClaimApplicationStandardOperation(ctx, "paused-claim"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("paused operation was claimed: %v", err)
	}
	retry, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause)
	if err != nil || !standardControlOperationEqual(retry, o) {
		t.Fatalf("already paused changed operation: %v", err)
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, before.UpdatedAt, ApplicationStandardOperationResume); !errors.Is(err, ErrApplicationStandardOperationStale) {
		t.Fatalf("stale resume accepted: %v", err)
	}
	want := "running"
	if before.State == "waiting" {
		want = "waiting"
	}
	o = standardControlOperation(t, s, o, f.owner.Account.ID, ApplicationStandardOperationResume, want)
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("old worker resurrected on resume: %v", err)
	}
	return o
}

func standardAbortOperation(t *testing.T, s standardOperationControlTestStore, f standardApprovalFixture, o ApplicationStandardOperation, old ApplicationStandardWorkerClaim) {
	t.Helper()
	ctx := context.Background()
	o = standardControlOperation(t, s, o, f.owner.Account.ID, ApplicationStandardOperationAbort, "failed")
	if o.ErrorCode != "operator_aborted" || o.Targets[0].State != "persisted" || o.Targets[1].State != "skipped" || o.Targets[1].ErrorCode != "operator_aborted" {
		t.Fatalf("partial abort lost its outcome: %+v", o)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("aborted worker retained authority: %v", err)
	}
	if _, err := s.ClaimApplicationStandardOperation(ctx, "after-abort"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("aborted operation was claimed: %v", err)
	}
	for i, target := range o.Targets {
		app, err := s.AppByID(ctx, target.AppID)
		want := api.AppSecurityPolicyOff
		if i == 0 {
			want = api.AppSecurityPolicyWarn
		}
		if err != nil || app.SecurityPolicy != want {
			t.Fatalf("abort changed installed controls: %+v %v", app, err)
		}
		e, err := s.GetApplicationStandardEnrollment(ctx, o.OrgID, target.AppID)
		if err != nil || e.ObservedRevision != 0 {
			t.Fatalf("abort fabricated observation: %+v %v", e, err)
		}
	}
	retry, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationAbort)
	if err != nil || !standardControlOperationEqual(retry, o) {
		t.Fatalf("already aborted changed operation: %v", err)
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationResume); !errors.Is(err, ErrConflict) {
		t.Fatalf("aborted operation resumed: %v", err)
	}
}

func standardOperationControlAuditCount(ctx context.Context, t *testing.T, s standardOperationTestStore, want int) {
	t.Helper()
	logs, err := s.ListAuditLog(ctx, AuditLogFilter{KindPrefix: "application_standard.operation_", IncludeAnonymous: true, Limit: 100})
	if err != nil || len(logs) != want {
		t.Fatalf("control audit count=%d want=%d err=%v", len(logs), want, err)
	}
	for _, log := range logs {
		if strings.Contains(string(log.Data), "@") || strings.Contains(string(log.Data), "approved_app") || strings.Contains(string(log.Data), "sealed") {
			t.Fatal("audit captured contact or private control material")
		}
	}
}

func TestMemApplicationStandardOperationControlAuthorization(t *testing.T) {
	standardOperationControlAuthorization(t, NewMemStore())
}

func standardOperationControlAuthorization(t *testing.T, s standardOperationControlTestStore) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	o, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "operation-control-other@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, other.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
		t.Fatalf("nonmember controlled rollout: %v", err)
	}
	if err := s.AddOrgMember(ctx, f.owner.PersonalOrg.ID, other.Account.ID, OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	for _, role := range []OrgRole{OrgRoleDeveloper, OrgRoleViewer, OrgRoleBilling} {
		if err := s.UpdateOrgMemberRole(ctx, f.owner.PersonalOrg.ID, other.Account.ID, role); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, other.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
			t.Fatalf("%s controlled rollout: %v", role, err)
		}
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, other.PersonalOrg.ID, other.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-organization control: %v", err)
	}
	standardOperationControlEligibility(t, s, f, o)
	if !standardControlOperationEqual(standardReadOperation(ctx, t, s, o), o) {
		t.Fatal("refused control changed the operation")
	}
	standardOperationControlAuditCount(ctx, t, s, 0)
	standardOperationControlAdmin(t, s, f, o, other.Account.ID)
}

func standardOperationControlAdmin(t *testing.T, s standardOperationControlTestStore, f standardApprovalFixture, o ApplicationStandardOperation, actorID string) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpdateOrgMemberRole(ctx, f.owner.PersonalOrg.ID, actorID, OrgRoleAdmin); err != nil {
		t.Fatal(err)
	}
	o = standardControlOperation(t, s, o, actorID, ApplicationStandardOperationPause, "paused")
	if err := s.RemoveOrgMember(ctx, f.owner.PersonalOrg.ID, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, actorID, o.ID, o.UpdatedAt, ApplicationStandardOperationResume); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
		t.Fatalf("removed admin resumed rollout: %v", err)
	}
}

func standardOperationControlEligibility(t *testing.T, s standardOperationControlTestStore, f standardApprovalFixture, o ApplicationStandardOperation) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpdateAccountStatus(ctx, f.owner.Account.ID, AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause); !errors.Is(err, ErrApplicationStandardReviewForbidden) {
		t.Fatalf("suspended actor controlled operation: %v", err)
	}
	if err := s.UpdateAccountStatus(ctx, f.owner.Account.ID, AccountActive); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateOrgStatus(ctx, f.owner.PersonalOrg.ID, OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause); !errors.Is(err, ErrConflict) {
		t.Fatalf("suspended organization controlled operation: %v", err)
	}
	if err := s.UpdateOrgStatus(ctx, f.owner.PersonalOrg.ID, OrgStatusActive); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ControlApplicationStandardOperation(canceled, o.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationAbort); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled control: %v", err)
	}
}

func TestMemApplicationStandardOperationReviewedRollback(t *testing.T) {
	standardOperationReviewedRollback(t, NewMemStore())
}

func standardOperationReviewedRollback(t *testing.T, s standardOperationControlTestStore) {
	t.Helper()
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	if _, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Slug: f.version.Slug, CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: json.RawMessage(`{"security_policy":{"mode":"mandatory","value":"warn"},"egress_extra_ports":{"mode":"mandatory","value":[8443]}}`)}}); err != nil {
		t.Fatal(err)
	}
	r := f.plan.Request
	r.AdmissionVersion = 2
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.plan.OrgID, f.owner.Account.ID, r)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("forward review: %+v %v", p.Blockers, err)
	}
	o, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "forward-partial-worker")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || o.Targets[0].State != "persisted" || o.Targets[1].State != "queued" {
		t.Fatalf("partial forward operation: %+v %v", o, err)
	}
	app, err := s.AppByID(ctx, o.Targets[0].AppID)
	if err != nil || len(app.EgressPorts) != 1 || app.EgressPorts[0] != 8443 {
		t.Fatalf("forward projection: %+v %v", app, err)
	}
	before := o
	o = standardControlOperation(t, s, o, f.owner.Account.ID, ApplicationStandardOperationAbort, "failed")
	if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, before.UpdatedAt, ApplicationStandardOperationAbort); !errors.Is(err, ErrApplicationStandardOperationStale) {
		t.Fatalf("stale abort replay: %v", err)
	}
	r = p.Request
	r.AdmissionVersion, r.ExpectedRevision, r.BatchSize = 1, 1, 2
	standardMaterializeReviewedRollback(t, s, f, o, r)
}

func standardMaterializeReviewedRollback(t *testing.T, s standardOperationControlTestStore, f standardApprovalFixture, previous ApplicationStandardOperation, r ApplicationStandardReviewRequest) {
	t.Helper()
	ctx := context.Background()
	p, err := s.PreviewApplicationStandardAssignment(ctx, previous.OrgID, f.owner.Account.ID, r)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("rollback review: %+v %v", p.Blockers, err)
	}
	o, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash)
	if err != nil || o.ID == previous.ID || o.ApprovalHash == previous.ApprovalHash {
		t.Fatalf("rollback reused forward authority: %+v %v", o, err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "reviewed-rollback-worker")
	if err != nil || c.OperationID != o.ID {
		t.Fatalf("rollback claim: %+v %v", c, err)
	}
	for range o.Targets {
		o, err = s.MaterializeNextApplicationStandardTarget(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
	}
	if o.State != "waiting" {
		t.Fatal("rollback was declared complete without consumer acknowledgments")
	}
	for _, target := range o.Targets {
		app, err := s.AppByID(ctx, target.AppID)
		if err != nil || app.SecurityPolicy != api.AppSecurityPolicyWarn || len(app.EgressPorts) != 0 || target.State != "persisted" {
			t.Fatalf("reviewed rollback projection: %+v %+v %v", app, target, err)
		}
		e, err := s.GetApplicationStandardEnrollment(ctx, p.OrgID, target.AppID)
		if err != nil || e.ObservedRevision != 0 || len(e.Adoptions) != 1 || e.Adoptions[0].Version != 1 {
			t.Fatalf("rollback adoption/observation: %+v %v", e, err)
		}
	}
	if got := standardReadOperation(ctx, t, s, previous); !standardControlOperationEqual(got, previous) {
		t.Fatal("rollback changed retained forward history")
	}
}

func TestApplicationStandardOperationControlValidation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	org, actor, op := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name, org string
		expected  time.Time
		action    ApplicationStandardOperationAction
	}{
		{"missing identity", "", now, ApplicationStandardOperationPause},
		{"missing timestamp", org, time.Time{}, ApplicationStandardOperationPause},
		{"unpersistable timestamp", org, now.Add(time.Nanosecond), ApplicationStandardOperationPause},
		{"unknown action", org, now, "finish"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if validStandardOperationControl(tc.org, actor, op, tc.expected, tc.action) {
				t.Fatal("invalid operator control accepted")
			}
		})
	}
	for _, terminal := range []string{"completed", "failed", "rolled_back", "superseded"} {
		for _, action := range []ApplicationStandardOperationAction{ApplicationStandardOperationPause, ApplicationStandardOperationResume, ApplicationStandardOperationAbort} {
			if _, err := prepareStandardOperationControl(ApplicationStandardOperation{State: terminal, UpdatedAt: now}, now, action, now); !errors.Is(err, ErrConflict) {
				t.Fatalf("terminal %s allowed %s: %v", terminal, action, err)
			}
		}
	}
}

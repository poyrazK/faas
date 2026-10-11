// adr: 640
package acceptance_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type milestoneLifecycleStore interface {
	operationLifecycleStore
	state.OperationMilestoneStore
	state.OperationRetentionStore
}

func testOperationMilestones(t *testing.T, s milestoneLifecycleStore) {
	t.Helper()
	ctx, acct, app, original, alice, bob := operationFixture(t, s)
	spec := original.Spec
	spec.Name = "checkout"
	spec.Path = "/checkout"
	spec.HTTPTransactionVersion = 1
	spec.Subject = &api.OperationSubjectSpec{Type: "order", IDFrom: "/order_id"}
	spec.InputSchema = []byte(`{"type":"object","required":["order_id"],"properties":{"order_id":{"type":"string"}},"additionalProperties":false}`)
	spec.Milestones = map[string]json.RawMessage{"paid": []byte(`true`), "settled": []byte(`{"type":"object","required":["total"],"properties":{"total":{"type":"integer"}},"additionalProperties":false}`)}
	spec.WorkflowSteps = []api.OperationWorkflowSpec{{Workflow: "order-lifecycle", Title: "Order lifecycle", Step: "paid", Label: "Payment authorized", Milestone: "paid", InstanceIDFrom: "/workflow_run_id", Position: 2}}
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: original.DeploymentID, Scope: original.Scope, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, def.DeploymentID, "stopped", 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: alice.ID, DefinitionID: def.ID, IdempotencyKey: "milestones", Input: []byte(`{"order_id":"shared & é/42"}`)})
	if err != nil {
		t.Fatal(err)
	}
	claim := func(id string) state.OperationExecutionAuthority {
		t.Helper()
		inv, err := s.ClaimInvocationWithCap(ctx, id, instance.ID, 60, 100)
		if err != nil {
			t.Fatal(err)
		}
		var headers map[string]string
		if err = json.Unmarshal(inv.Headers, &headers); err != nil {
			t.Fatal(err)
		}
		if headers[api.OperationMilestoneVersionHeader] != "1" {
			t.Fatal("milestone contract not negotiated")
		}
		return state.OperationExecutionAuthority{AccountID: acct.ID, AppID: app.ID, InstanceID: instance.ID, InvocationID: inv.ID, Attempt: inv.Attempts, Capability: headers[api.OperationCapabilityHeader]}
	}
	authority := claim(op.CurrentInvocationID)
	report := api.OperationMilestoneRequest{ID: uuid.NewString(), Name: "paid", Payload: []byte(`{"total":1,"workflow_run_id":"checkout-run-1"}`), OccurredAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
	if err := s.ValidateOperationMilestones(ctx, op.ID, authority, []api.OperationMilestoneRequest{report}); err != nil {
		t.Fatal(err)
	}
	opts := api.OperationMilestoneListOptions{AppID: app.ID, Scope: def.Scope, SubjectType: "order", SubjectID: "shared & é/42", Limit: 1}
	before, err := s.ListPlatformTenantOperationMilestones(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(before.Milestones) != 0 {
		t.Fatal("validation published an uncommitted fact")
	}
	first, err := s.ReportOperationMilestone(ctx, op.ID, authority, report)
	if err != nil || len(first.WorkflowSteps) != 1 || first.WorkflowSteps[0].Label != "Payment authorized" {
		t.Fatalf("milestone workflow mapping: %+v, err=%v", first.WorkflowSteps, err)
	}
	replay := report
	replay.Payload = []byte(`{ "total": 1e0, "workflow_run_id": "checkout-run-1" }`)
	duplicate, err := s.ReportOperationMilestone(ctx, op.ID, authority, replay)
	if err != nil || duplicate.ID != first.ID || duplicate.Sequence != first.Sequence || !duplicate.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("unstable acknowledgement: %+v %v", duplicate, err)
	}
	first.Payload[0] = '!'
	first.Subject.ID = "caller mutation"
	conflict := report
	conflict.Payload = []byte(`{"total":2,"workflow_run_id":"checkout-run-1"}`)
	if _, err = s.ReportOperationMilestone(ctx, op.ID, authority, conflict); !errors.Is(err, state.ErrOperationInputConflict) {
		t.Fatalf("changed fact replay: %v", err)
	}
	if err := s.ValidateOperationMilestones(ctx, op.ID, authority, []api.OperationMilestoneRequest{report, report}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("duplicate batch ID: %v", err)
	}
	invalid := report
	invalid.ID = uuid.NewString()
	invalid.Name = "settled"
	invalid.Payload = []byte(`{"total":"secret"}`)
	if _, err = s.ReportOperationMilestone(ctx, op.ID, authority, invalid); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("schema violation: %v", err)
	}
	encoded := report
	encoded.ID = uuid.NewString()
	encoded.Payload = []byte(`{"text":"\u0000","n":1e1000,"workflow_run_id":"checkout-run-1"}`)
	if _, err = s.ReportOperationMilestone(ctx, op.ID, authority, encoded); err != nil {
		t.Fatalf("bounded JSON encoding: %v", err)
	}
	page, err := s.ListPlatformTenantOperationMilestones(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(page.Milestones) != 1 || page.NextCursor == "" || page.Milestones[0].PlatformTenantID != "" {
		t.Fatalf("customer page: %+v %v", page, err)
	}
	if len(page.Milestones[0].Payload) > api.OperationMilestonePayloadMaxBytes {
		t.Fatal("database expanded a bounded JSON number")
	}
	cursor := page.NextCursor
	opts.Cursor = cursor
	next, err := s.ListPlatformTenantOperationMilestones(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(next.Milestones) != 1 || next.Milestones[0].ID != first.ID || len(next.Milestones[0].WorkflowSteps) != 1 ||
		next.Milestones[0].WorkflowSteps[0].Step != "paid" || next.Milestones[0].Subject.ID != "shared & é/42" {
		t.Fatalf("next page/mutability: %+v %v", next, err)
	}
	for _, change := range []func(*api.OperationMilestoneListOptions){
		func(o *api.OperationMilestoneListOptions) { o.Scope = "staging" }, func(o *api.OperationMilestoneListOptions) { o.AppID = uuid.NewString() },
		func(o *api.OperationMilestoneListOptions) { o.SubjectID = "other" }, func(o *api.OperationMilestoneListOptions) {
			o.OperationID = op.ID
			o.SubjectType = ""
			o.SubjectID = ""
		},
	} {
		bad := opts
		change(&bad)
		if _, err := s.ListPlatformTenantOperationMilestones(ctx, acct.ID, alice.ID, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("foreign timeline cursor: %v", err)
		}
	}
	if _, err := s.ListPlatformTenantOperationMilestones(ctx, acct.ID, bob.ID, opts); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("foreign customer cursor: %v", err)
	}
	opts.Cursor = ""
	opts.Limit = 100
	foreign, err := s.ListPlatformTenantOperationMilestones(ctx, acct.ID, bob.ID, opts)
	if err != nil || len(foreign.Milestones) != 0 {
		t.Fatalf("shared business ID crossed customers: %+v %v", foreign, err)
	}
	owner, err := s.ListAccountOperationMilestones(ctx, acct.ID, opts)
	if err != nil || len(owner.Milestones) != 2 || owner.Milestones[0].PlatformTenantID != alice.ID {
		t.Fatalf("owner timeline: %+v %v", owner, err)
	}
	opts.TenantID = bob.ID
	owner, err = s.ListAccountOperationMilestones(ctx, acct.ID, opts)
	if err != nil || len(owner.Milestones) != 0 {
		t.Fatalf("owner tenant filter: %+v %v", owner, err)
	}
	opts.TenantID = ""
	if err := s.FailInvocation(ctx, authority.InvocationID, "lost publication acknowledgement", time.Second, 10, state.WithClaimAttempt(authority.Attempt)); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverOperation(ctx, acct.ID, alice.ID, op.ID, api.OperationRecoveryRequest{RecoveryID: "receipt-outbox", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "committed receipt and milestone outbox verified"})
	if err != nil {
		t.Fatal(err)
	}
	current := claim(recovered.CurrentInvocationID)
	if _, err := s.ReportOperationMilestone(ctx, op.ID, authority, report); err == nil {
		t.Fatal("stale execution re-acknowledged milestone")
	}
	resumed, err := s.ReportOperationMilestone(ctx, op.ID, current, report)
	if err != nil || resumed.Sequence != duplicate.Sequence || !resumed.CreatedAt.Equal(duplicate.CreatedAt) {
		t.Fatalf("recovery duplicated fact: %+v %v", resumed, err)
	}
	if _, err := s.SetPlatformTenantStatus(ctx, acct.ID, alice.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportOperationMilestone(ctx, op.ID, current, report); !errors.Is(err, state.ErrPlatformTenantSuspended) {
		t.Fatalf("suspended customer publication: %v", err)
	}
	if _, err := s.SetPlatformTenantStatus(ctx, acct.ID, alice.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	for i := 2; i < api.OperationMilestonesMaxPerOperation; i++ {
		fresh := report
		fresh.ID = uuid.NewString()
		if _, err := s.ReportOperationMilestone(ctx, op.ID, current, fresh); err != nil {
			t.Fatalf("fact %d: %v", i, err)
		}
	}
	excess := report
	excess.ID = uuid.NewString()
	if _, err := s.ReportOperationMilestone(ctx, op.ID, current, excess); !errors.Is(err, state.ErrOperationQuota) {
		t.Fatalf("milestone bound: %v", err)
	}
	if err := s.ValidateOperationMilestones(ctx, op.ID, current, []api.OperationMilestoneRequest{report}); err != nil {
		t.Fatalf("dedupe exhausted quota: %v", err)
	}
	if err := s.ValidateOperationMilestones(ctx, op.ID, current, []api.OperationMilestoneRequest{excess}); !errors.Is(err, state.ErrOperationQuota) {
		t.Fatalf("validation quota: %v", err)
	}
	if err := s.CompleteKeyedInvocation(ctx, current.InvocationID, current.Attempt, []byte(`{"file":"receipt.csv"}`)); err != nil {
		t.Fatal(err)
	}
	retained, err := s.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || retained.MilestoneCount != api.OperationMilestonesMaxPerOperation {
		t.Fatalf("count drift: %s %v", fmt.Sprint(retained.MilestoneCount), err)
	}
	opts.SubjectType = ""
	opts.SubjectID = ""
	opts.OperationID = op.ID
	history, err := s.ListPlatformTenantOperationMilestones(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(history.Milestones) != api.OperationMilestonesMaxPerOperation {
		t.Fatalf("retained fact ledger: %+v %v", history, err)
	}
	if _, err := s.PruneOperationState(ctx, retained.EventExpiresAt.Add(time.Second), 500); err != nil {
		t.Fatal(err)
	}
	history, err = s.ListPlatformTenantOperationMilestones(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(history.Milestones) != api.OperationMilestonesMaxPerOperation {
		t.Fatalf("short event retention deleted business facts: %+v %v", history, err)
	}
	if _, err := s.PruneOperationState(ctx, retained.ExpiresAt.Add(time.Second), 500); err != nil {
		t.Fatal(err)
	}
	history, err = s.ListPlatformTenantOperationMilestones(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(history.Milestones) != 0 {
		t.Fatalf("expired Operation retained public facts: %+v %v", history, err)
	}
}

func TestMemOperationMilestones(t *testing.T) { testOperationMilestones(t, state.NewMemStore()) }
func TestPgOperationMilestones(t *testing.T)  { s, _ := pgStore(t); testOperationMilestones(t, s) }

func TestMilestoneLedgerClonePolicy(t *testing.T) {
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range policies {
		if policy.TableName == "customer_operation_milestones" {
			if policy.Kind != state.CloneSchemaOperational || len(policy.Columns) != 8 {
				t.Fatalf("milestone clone policy: %+v", policy)
			}
			return
		}
	}
	t.Fatal("milestone ledger missing from clone schema inventory")
}

// ADR-639: reference lookup preserves owner, environment, replay and recovery.
package acceptance_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOperationSubjects(t *testing.T, s operationLifecycleStore) {
	t.Helper()
	ctx, acct, app, legacy, alice, bob := operationFixture(t, s)
	spec := operationSpec()
	spec.Name = "fulfill-order"
	spec.Path = "/orders/fulfill"
	spec.Subject = &api.OperationSubjectSpec{Type: "order", IDFrom: "/order_id"}
	spec.InputSchema = []byte(`{"type":"object","properties":{"order_id":{},"next_order_id":{}},"additionalProperties":false}`)
	define := func(spec api.OperationDefinitionSpec) state.OperationDefinition {
		t.Helper()
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:orders", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		d, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: spec}})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	def := define(spec)
	admit := func(tenant, key, id string) state.Operation {
		t.Helper()
		input, _ := json.Marshal(map[string]string{"order_id": id})
		op, fresh, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant, IdempotencyKey: key, Input: input})
		if err != nil || !fresh || op.Subject == nil || op.Subject.ID != id {
			t.Fatalf("admission: %+v fresh=%v %v", op, fresh, err)
		}
		return op
	}
	first := admit(alice.ID, "a1", "shared & é/42")
	second := admit(alice.ID, "a2", "shared & é/42")
	bobOp := admit(bob.ID, "a1", "shared & é/42")
	admit(alice.ID, "other-id", "another")
	opts := api.OperationListOptions{AppID: app.ID, Scope: def.Scope, SubjectType: "order", SubjectID: "shared & é/42", Limit: 1}
	page, err := s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(page.Operations) != 1 || page.NextCursor == "" {
		t.Fatalf("first reference page: %+v %v", page, err)
	}
	if page.Operations[0].ID != first.ID && page.Operations[0].ID != second.ID {
		t.Fatal("foreign reference page")
	}
	originalCursor := page.NextCursor
	page.Operations[0].Subject.ID = "caller mutation"
	persisted, err := s.OperationByID(ctx, acct.ID, alice.ID, page.Operations[0].ID)
	if err != nil || persisted.Subject.ID != opts.SubjectID {
		t.Fatalf("mutable persisted reference: %+v %v", persisted.Subject, err)
	}
	opts.Cursor = originalCursor
	next, err := s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(next.Operations) != 1 || next.Operations[0].ID == page.Operations[0].ID || next.NextCursor != "" {
		t.Fatalf("next reference page: %+v %v", next, err)
	}
	for _, change := range []func(*api.OperationListOptions){
		func(o *api.OperationListOptions) { o.SubjectID = "another" }, func(o *api.OperationListOptions) { o.SubjectType = "invoice" },
		func(o *api.OperationListOptions) { o.SubjectType = ""; o.SubjectID = "" }, func(o *api.OperationListOptions) { o.Scope = "staging" },
	} {
		wrong := opts
		change(&wrong)
		if _, err := s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, wrong); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("foreign filter cursor: %v", err)
		}
	}
	if _, err := s.ListPlatformTenantOperations(ctx, acct.ID, bob.ID, opts); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("foreign customer cursor: %v", err)
	}
	opts.Cursor = ""
	opts.Limit = 100
	bobPage, err := s.ListPlatformTenantOperations(ctx, acct.ID, bob.ID, opts)
	if err != nil || len(bobPage.Operations) != 1 || bobPage.Operations[0].ID != bobOp.ID {
		t.Fatalf("same ID customer isolation: %+v %v", bobPage, err)
	}
	accountPage, err := s.ListAccountOperations(ctx, acct.ID, opts)
	if err != nil || len(accountPage.Operations) != 3 {
		t.Fatalf("operator lookup: %+v %v", accountPage, err)
	}
	opts.TenantID = alice.ID
	accountPage, err = s.ListAccountOperations(ctx, acct.ID, opts)
	if err != nil || len(accountPage.Operations) != 2 {
		t.Fatalf("operator customer filter: %+v %v", accountPage, err)
	}
	opts.TenantID = ""
	for _, change := range []func(*api.OperationListOptions){
		func(o *api.OperationListOptions) { o.SubjectID = "unknown" }, func(o *api.OperationListOptions) { o.SubjectType = "invoice" },
		func(o *api.OperationListOptions) { o.Scope = "staging" }, func(o *api.OperationListOptions) { o.AppID = uuid.NewString() }, func(o *api.OperationListOptions) { o.Name = "export" },
	} {
		empty := opts
		change(&empty)
		p, err := s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, empty)
		if err != nil || len(p.Operations) != 0 {
			t.Fatalf("lookup crossed filter: %+v %v", p, err)
		}
	}
	if p, err := s.ListPlatformTenantOperations(ctx, uuid.NewString(), alice.ID, opts); err != nil || len(p.Operations) != 0 {
		t.Fatalf("lookup crossed account: %+v %v", p, err)
	}
	for _, change := range []func(*api.OperationListOptions){
		func(o *api.OperationListOptions) { o.SubjectID = "" }, func(o *api.OperationListOptions) { o.SubjectType = "" },
		func(o *api.OperationListOptions) { o.SubjectID = strings.Repeat("é", 129) }, func(o *api.OperationListOptions) { o.SubjectID = "a\n" },
	} {
		invalid := opts
		change(&invalid)
		if _, err := s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, invalid); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid selector: %v", err)
		}
	}
	for i, raw := range []string{`{}`, `{"order_id":1}`, `{"order_id":null}`, `{"order_id":""}`, `{"order_id":"a\n"}`, fmt.Sprintf(`{"order_id":%q}`, strings.Repeat("é", 129))} {
		if _, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: fmt.Sprintf("invalid-%d", i), Input: []byte(raw)}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid reference admitted: %v", err)
		}
	}
	// A redeploy changes the selector. Replay still returns the originally pinned reference.
	spec.Subject = &api.OperationSubjectSpec{Type: "shipment", IDFrom: "/next_order_id"}
	nextDef := define(spec)
	input, _ := json.Marshal(map[string]string{"order_id": opts.SubjectID})
	replay, fresh, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: nextDef.ID, PlatformTenantID: alice.ID, IdempotencyKey: "a1", Input: input})
	if err != nil || fresh || replay.ID != first.ID || replay.Subject == nil || replay.Subject.Type != "order" || replay.Subject.ID != opts.SubjectID {
		t.Fatalf("redeploy changed admitted reference: %+v %v", replay, err)
	}
	// Recovery creates a new execution while retaining the same logical reference.
	inv, err := s.ClaimInvocation(ctx, first.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, inv.ID, "lost response", time.Second, 10, state.WithClaimAttempt(inv.Attempts)); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverOperation(ctx, acct.ID, alice.ID, first.ID, api.OperationRecoveryRequest{RecoveryID: "reference-recovery", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified business ledger; execution may safely resume"})
	if err != nil || recovered.Generation != 2 || recovered.Subject == nil || *recovered.Subject != *replay.Subject {
		t.Fatalf("recovery changed reference: %+v %v", recovered, err)
	}
	// Legacy declarations still admit and their records omit subject entirely.
	legacy = define(legacy.Spec)
	old, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: legacy.ID, PlatformTenantID: alice.ID, IdempotencyKey: "legacy", Input: []byte(`{"count":1}`)})
	if err != nil || old.Subject != nil {
		t.Fatalf("legacy operation changed: %+v %v", old, err)
	}
	all := api.OperationListOptions{AppID: app.ID, Scope: legacy.Scope, Limit: 100}
	allPage, err := s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, all)
	if err != nil || len(allPage.Operations) != 4 {
		t.Fatalf("unfiltered history or invalid admission changed: %+v %v", allPage, err)
	}
}

func TestMemOperationSubjects(t *testing.T) { testOperationSubjects(t, state.NewMemStore()) }
func TestPgOperationSubjects(t *testing.T)  { s, _ := pgStore(t); testOperationSubjects(t, s) }

func TestPgOperationSubjectStorageGuards(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, _, def, alice, _ := operationFixture(t, s)
	original, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: "subject-guards", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	// Clone a legacy record in a rolled-back transaction. CHECK validation runs
	// before the deferred execution FK, so valid inserts exercise the real guard.
	const insert = `INSERT INTO customer_operations
 (id,account_id,app_id,platform_tenant_id,definition_id,current_invocation_id,state,record,expires_at,created_at)
 SELECT $1::uuid,account_id,app_id,platform_tenant_id,definition_id,current_invocation_id,state,
 jsonb_set(jsonb_set(record,'{id}',to_jsonb($1::text)),'{subject}',$2::jsonb),expires_at,created_at
 FROM customer_operations WHERE id=$3::uuid`
	for _, tc := range []struct {
		subject string
		valid   bool
	}{
		{`{"type":"order","id":"ord-123"}`, true},
		{fmt.Sprintf(`{"type":"order","id":%q}`, strings.Repeat("é", 128)), true},
		{`null`, false}, {`{}`, false}, {`{"type":"order"}`, false}, {`{"id":"42"}`, false},
		{`{"type":"Order","id":"42"}`, false}, {`{"type":1,"id":"42"}`, false}, {`{"type":"order","id":1}`, false},
		{`{"type":"order","id":""}`, false}, {`{"type":"order","id":"a\t"}`, false}, {`{"type":"order","id":"a\u007f"}`, false},
		{`{"type":"order","id":"42","extra":"ignored"}`, false},
		{fmt.Sprintf(`{"type":"order","id":%q}`, strings.Repeat("é", 129)), false},
	} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, insert, uuid.NewString(), tc.subject, original.ID)
		_ = tx.Rollback(ctx)
		if (err == nil) != tc.valid {
			t.Fatalf("database reference validation %.80s: %v", tc.subject, err)
		}
	}
	// Even attaching a valid reference to an already admitted legacy record is forbidden.
	if _, err := pool.Exec(ctx, `UPDATE customer_operations SET record=jsonb_set(record,'{subject}','{"type":"order","id":"42"}'::jsonb) WHERE id=$1::uuid`, original.ID); err == nil {
		t.Fatal("database allowed reference rewrite")
	}
	retained, err := s.OperationByID(ctx, acct.ID, alice.ID, original.ID)
	if err != nil || retained.Subject != nil {
		t.Fatalf("legacy reference mutated: %+v %v", retained.Subject, err)
	}
}

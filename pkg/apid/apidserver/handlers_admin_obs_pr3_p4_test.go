// handlers_admin_obs_pr3_p4_test.go — Commit 6 (P4) coverage
// for /v1/admin/obs/audit-log/search. The four new branches:
//
//	?actor_email=<email>          exact match on account_email (MemStore filter)
//	?operator_only=true           sugar for kind_prefix=operator.action.
//	                             (MemStore defensive, handler mutual-
//	                              exclusivity gate returns 400 when set
//	                              together with kind_prefix)
//	?target_account_id=<uuid>     JSONB containment on data
//	                              (MemStore scans data for the key +
//	                              stringifies the uuid into a stub
//	                               JSON object — see helper below)
//	?is_operator_action echo      derived boolean on each item derived
//	                              from strings.HasPrefix(kind, "operator.action.")
//
// All four project through toObsAuditLogRows so the is_operator_action
// derivation is uniform. Pattern mirrors the PR #2 / PR #3 tests
// in handlers_admin_obs_pr3_test.go so the failure modes are
// discoverable in one place.

package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedAuditOperatorActionRow plants an audit_log row whose data
// payload mirrors what Commit 3's emitOperatorActionParkInstance /
// emitOperatorActionForceColdBoot helpers produce — i.e. data is
// JSON whose `target_account_id` field is the canonical target
// uuid. Used by the target_account_id tests below.
//
// Caller passes any accountID (a fabricated uuid or the admin's
// own uuid) so the row is not anonymous (the operator-search
// default IncludeAnonymous=false filters out AccountID=nil rows;
// fleet-level actions like operator.action.reclaim_build would
// normally have nil AccountID; tests can opt-in via the
// include_anonymous query param).
func seedAuditOperatorActionRow(t *testing.T, store *state.MemStore, kind, email, actor string, accountID *uuid.UUID, data []byte) {
	t.Helper()
	if err := store.InsertAuditLog(context.Background(), state.AuditLog{
		Kind:         kind,
		AccountID:    accountID,
		AccountEmail: email,
		Actor:        actor,
		ReceivedAt:   time.Now().UTC(),
		Data:         data,
	}); err != nil {
		t.Fatalf("seed operator audit_log: %v", err)
	}
}

func TestObsAuditLogSearch_ActorEmail_FiltersByCapturedEmail(t *testing.T) {
	e := apidserver.NewObsPR3Env(t, api.ScopesAdminOnly, apidserver.Pr3AdminEmail, apidserver.Pr3AdminEmail)
	acctID := uuid.New()
	apidserver.SeedAuditLogRow(t, (*e.Store()), "operator.action.view", &acctID, "alice@example.com", "alice@example.com", nil)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "operator.action.view", &acctID, "alice@example.com", "alice@example.com", nil)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "operator.action.view", &acctID, "bob@example.com", "bob@example.com", nil)

	rec := e.DoOperator(t, "GET", "/v1/admin/obs/audit-log/search?actor_email=alice@example.com", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search actor_email: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items: got %d, want 2 (alice's two operator rows)", len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.AccountEmail != "alice@example.com" {
			t.Errorf("account_email = %q, want alice@example.com", item.AccountEmail)
		}
	}
	if resp.ActorEmail != "alice@example.com" {
		t.Errorf("response actor_email echo: got %q, want alice@example.com", resp.ActorEmail)
	}
}

func TestObsAuditLogSearch_ActorEmail_NoMatchReturnsEmpty(t *testing.T) {
	e := apidserver.NewObsPR3Env(t, api.ScopesAdminOnly, apidserver.Pr3AdminEmail, apidserver.Pr3AdminEmail)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "operator.action.view", nil, "alice@example.com", "alice@example.com", nil)

	rec := e.DoOperator(t, "GET", "/v1/admin/obs/audit-log/search?actor_email=nobody@example.com", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search no-match actor_email: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("items: got %d, want 0", len(resp.Items))
	}
	if resp.ActorEmail != "nobody@example.com" {
		t.Errorf("response actor_email echo: got %q, want nobody@example.com", resp.ActorEmail)
	}
}

func TestObsAuditLogSearch_TargetAccountID_JSONBContainment(t *testing.T) {
	e := apidserver.NewObsPR3Env(t, api.ScopesAdminOnly, apidserver.Pr3AdminEmail, apidserver.Pr3AdminEmail)
	targetID := uuid.New()
	decoyID := uuid.New()
	acctID := uuid.New()
	seedAuditOperatorActionRow(t, (*e.Store()), "operator.action.park_instance", "ops@faas.local", "ops@faas.local", &acctID,
		[]byte(`{"target_account_id":"`+targetID.String()+`","instance_id":"abc"}`))
	seedAuditOperatorActionRow(t, (*e.Store()), "operator.action.view", "ops@faas.local", "ops@faas.local", &acctID,
		[]byte(`{"target_account_id":"`+targetID.String()+`","endpoint":"/v1/admin/apps/foo/metrics"}`))
	seedAuditOperatorActionRow(t, (*e.Store()), "operator.action.view", "ops@faas.local", "ops@faas.local", &acctID,
		[]byte(`{"target_account_id":"`+decoyID.String()+`","endpoint":"/v1/admin/apps/bar/metrics"}`))

	rec := e.DoOperator(t, "GET", "/v1/admin/obs/audit-log/search?target_account_id="+targetID.String(), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search target_account_id: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items: got %d, want 2 (target-account rows only)", len(resp.Items))
	}
	if resp.TargetAccountID != targetID.String() {
		t.Errorf("response target_account_id echo: got %q, want %q", resp.TargetAccountID, targetID)
	}
}

func TestObsAuditLogSearch_OperatorOnly_FiltersToOperatorActionFamily(t *testing.T) {
	e := apidserver.NewObsPR3Env(t, api.ScopesAdminOnly, apidserver.Pr3AdminEmail, apidserver.Pr3AdminEmail)
	acctID := uuid.New()
	apidserver.SeedAuditLogRow(t, (*e.Store()), "operator.action.view", &acctID, "ops@faas.local", "ops@faas.local", nil)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "operator.action.park_instance", &acctID, "ops@faas.local", "ops@faas.local", nil)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "audit.account.deleted", &acctID, "ops@faas.local", "", nil)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "pii.accessed", &acctID, "ops@faas.local", "ops@faas.local", nil)

	rec := e.DoOperator(t, "GET", "/v1/admin/obs/audit-log/search?operator_only=true", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search operator_only: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items: got %d, want 2 (two operator.action.* rows)", len(resp.Items))
	}
	for _, item := range resp.Items {
		if got := item.Kind[:len("operator.action.")]; got != "operator.action." {
			t.Errorf("kind %q does not have operator.action. prefix", item.Kind)
		}
	}
	if !resp.OperatorOnly {
		t.Errorf("response operator_only echo: got false, want true")
	}
	if resp.KindPrefix != "operator.action." {
		t.Errorf("response kind_prefix echo: got %q, want operator.action. (sugar expansion)", resp.KindPrefix)
	}
}

func TestObsAuditLogSearch_OperatorOnly_KindPrefix_MutuallyExclusive(t *testing.T) {
	e := apidserver.NewObsPR3Env(t, api.ScopesAdminOnly, apidserver.Pr3AdminEmail, apidserver.Pr3AdminEmail)
	rec := e.DoOperator(t, "GET", "/v1/admin/obs/audit-log/search?operator_only=true&kind_prefix=auth.", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("operator_only + kind_prefix: got %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, "conflicting_filters")
}

func TestObsAuditLogSearch_OperatorOnly_BadBool_400(t *testing.T) {
	e := apidserver.NewObsPR3Env(t, api.ScopesAdminOnly, apidserver.Pr3AdminEmail, apidserver.Pr3AdminEmail)
	rec := e.DoOperator(t, "GET", "/v1/admin/obs/audit-log/search?operator_only=notabool", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("audit-log/search bad operator_only: got %d, want 400", rec.Code)
	}
}

func TestObsAuditLogSearch_TargetAccountID_BadUUID_400(t *testing.T) {
	e := apidserver.NewObsPR3Env(t, api.ScopesAdminOnly, apidserver.Pr3AdminEmail, apidserver.Pr3AdminEmail)
	rec := e.DoOperator(t, "GET", "/v1/admin/obs/audit-log/search?target_account_id=not-a-uuid", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("audit-log/search bad target_account_id: got %d, want 400", rec.Code)
	}
}

func TestObsAuditLogSearch_RowIsOperatorActionField_DerivesTrueOnOperatorRows(t *testing.T) {
	e := apidserver.NewObsPR3Env(t, api.ScopesAdminOnly, apidserver.Pr3AdminEmail, apidserver.Pr3AdminEmail)
	acctID := uuid.New()
	apidserver.SeedAuditLogRow(t, (*e.Store()), "operator.action.view", &acctID, "ops@faas.local", "ops@faas.local", nil)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "operator.action.park_instance", &acctID, "ops@faas.local", "ops@faas.local", nil)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "audit.account.deleted", &acctID, "ops@faas.local", "", nil)
	apidserver.SeedAuditLogRow(t, (*e.Store()), "pii.accessed", &acctID, "ops@faas.local", "ops@faas.local", nil)

	rec := e.DoOperator(t, "GET", "/v1/admin/obs/audit-log/search", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit-log/search echo: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAuditLogSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 4 {
		t.Fatalf("items: got %d, want 4 (4 rows seeded)", len(resp.Items))
	}
	// Walk every row and verify the is_operator_action derivation
	// matches strings.HasPrefix(kind, "operator.action.") for both
	// directions: operator.* family → true, others → false.
	for _, item := range resp.Items {
		wantIs := item.Kind == "operator.action.view" || item.Kind == "operator.action.park_instance"
		if item.IsOperatorAction != wantIs {
			t.Errorf("is_operator_action for kind=%q: got %v, want %v", item.Kind, item.IsOperatorAction, wantIs)
		}
	}
}

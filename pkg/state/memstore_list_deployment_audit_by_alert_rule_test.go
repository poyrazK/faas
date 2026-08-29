// memstore_list_deployment_audit_by_alert_rule_test.go —
// SAFE-RELEASES-OBS PR-D (issue #976 / ADR-122) tests for the
// memstore mirror of pgstore.ListDeploymentAuditByAlertRule. The
// pgstore path is the production hot path; this test pins the
// memstore unit-test seam against which
// cmd/apid/handlers_dashboard.go::renderAlertRuleDetail is
// exercised in the suite.

package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestListDeploymentAuditByAlertRule_FiltersAndCaps pins:
//
//	(a) only rows whose AlertRuleID matches the query id are
//	    returned,
//	(b) rows are returned newest-first (matches the partial
//	    index ordering from migrations/00532),
//	(c) an explicit limit caps the result,
//	(d) rows without an AlertRuleID are excluded.
func TestListDeploymentAuditByAlertRule_FiltersAndCaps(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	depID := uuid.New()
	targetRule := uuid.New()
	otherRule := uuid.New()

	// Seed 4 rows: targetRule (old), otherRule, nil, targetRule (new).
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	rules := []*uuid.UUID{&targetRule, &otherRule, nil, &targetRule}
	for i, ridPtr := range rules {
		entry := DeploymentAudit{
			DeploymentID: depID,
			Kind:         DeployRolloutStarted,
			Actor:        "test",
			At:           base.Add(time.Duration(i) * time.Second),
			AlertRuleID:  ridPtr,
		}
		if _, err := m.AppendDeploymentAudit(ctx, entry); err != nil {
			t.Fatalf("append #%d: %v", i, err)
		}
	}

	// (a) filter: only targetRule rows come back (2 of them).
	got, err := m.ListDeploymentAuditByAlertRule(ctx, targetRule.String(), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2 (only targetRule rows)", len(got))
	}
	for _, r := range got {
		if r.AlertRuleID == nil || *r.AlertRuleID != targetRule {
			t.Errorf("row leaked: AlertRuleID=%v", r.AlertRuleID)
		}
	}

	// (b) newest-first: the LAST appended targetRule row (index 3
	// in the seed) should be at position 0.
	if got[0].At.Before(got[1].At) {
		t.Errorf("rows not newest-first: got[0].At=%v > got[1].At=%v expected", got[0].At, got[1].At)
	}

	// (c) explicit limit caps the result.
	gotLimited, err := m.ListDeploymentAuditByAlertRule(ctx, targetRule.String(), 1)
	if err != nil {
		t.Fatalf("list limit=1: %v", err)
	}
	if len(gotLimited) != 1 {
		t.Errorf("limit=1 returned %d rows; want 1", len(gotLimited))
	}

	// (d) nil-AlertRuleID rows are excluded.
	for _, r := range got {
		if r.AlertRuleID == nil {
			t.Errorf("nil-AlertRuleID row leaked into result")
		}
	}
}

// TestListDeploymentAuditByAlertRule_InvalidUUID pins the
// uuid.Parse error path: a malformed alertRuleID surfaces a
// wrapped error so the handler can render a 400.
func TestListDeploymentAuditByAlertRule_InvalidUUID(t *testing.T) {
	m := NewMemStore()
	_, err := m.ListDeploymentAuditByAlertRule(context.Background(), "not-a-uuid", 10)
	if err == nil {
		t.Fatal("expected error for malformed uuid; got nil")
	}
}

// TestAppendDeploymentAudit_PreservesAlertRuleID pins the
// round-trip: rows inserted via AppendDeploymentAudit with an
// AlertRuleID are read back with the same pointer value (memstore
// clones via pointer copy, see memstore.go around line 10039).
func TestAppendDeploymentAudit_PreservesAlertRuleID(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	ruleID := uuid.New()
	depID := uuid.New()
	_, err := m.AppendDeploymentAudit(ctx, DeploymentAudit{
		DeploymentID: depID,
		Kind:         DeployAlertRuleFired,
		Actor:        "test",
		At:           time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		AlertRuleID:  &ruleID,
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	rows, err := m.ListDeploymentAuditByAlertRule(ctx, ruleID.String(), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len=%d; want 1", len(rows))
	}
	if rows[0].AlertRuleID == nil || *rows[0].AlertRuleID != ruleID {
		t.Errorf("AlertRuleID round-trip mismatch: got=%v want=%v", rows[0].AlertRuleID, ruleID)
	}
}

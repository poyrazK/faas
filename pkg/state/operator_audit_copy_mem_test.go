package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261102071817 — an operator outcome that reaches events through the
// audit outbox (schedd's node and recovery outcomes) is copied into the audit
// log under the operator, as an emitted operator action is, and only once.
func TestMemStoreDeliveredOperatorOutcomeReachesAuditLog(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	operator, target := uuid.New(), uuid.NewString()
	data := []byte(`{"actor":"` + operator.String() + `","intent_id":"i-1","target_account_id":"` + target + `","result":"succeeded"}`)
	if _, err := s.EnqueueAuditEvent(ctx, "schedd", "operator.action.node_drain.outcome", &target, data, "operator.node.outcome:i-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := s.ClaimAuditEvent(ctx, "apid", time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	operatorAudit := func() []state.AuditLog {
		t.Helper()
		logs, err := s.ListAuditLog(ctx, state.AuditLogFilter{OperatorOnly: true, TargetAccountID: &target, Limit: 10})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}
		return logs
	}
	if delivered, err := s.DeliverAuditEvent(ctx, claimed.ID); err != nil || !delivered {
		t.Fatalf("deliver: %v %v", delivered, err)
	}
	logs := operatorAudit()
	if len(logs) != 1 || logs[0].Kind != "operator.action.node_drain.outcome" || logs[0].AccountID == nil || *logs[0].AccountID != operator {
		t.Fatalf("operator audit after delivery: %+v", logs)
	}
	if again, err := s.DeliverAuditEvent(ctx, claimed.ID); err != nil || again {
		t.Fatalf("second delivery: %v %v", again, err)
	}
	if logs := operatorAudit(); len(logs) != 1 {
		t.Fatalf("second delivery copied again: %+v", logs)
	}
}

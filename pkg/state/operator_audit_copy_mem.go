package state

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// copyOperatorActionToAuditLogLocked mirrors the events_operator_action_audit_log
// trigger (migrations/20261102071817365, ADR-20261102071817): an operator.action.*
// event also lands in the audit log, under the operator who acted, so the
// operator audit search finds it and the customer's own audit log does not.
// The caller holds m.mu.
func (m *MemStore) copyOperatorActionToAuditLogLocked(e Event) {
	if !strings.HasPrefix(e.Kind, "operator.action.") {
		return
	}
	var payload map[string]any
	_ = json.Unmarshal(e.Data, &payload)
	actor, _ := payload["actor"].(string)
	entry := AuditLog{ID: uuid.New(), Kind: e.Kind, Actor: actor, ReceivedAt: e.At, Data: e.Data}
	if entry.Actor == "" {
		entry.Actor = e.Actor
	}
	if operator, err := uuid.Parse(actor); err == nil {
		entry.AccountID = &operator
		if account, ok := m.accounts[actor]; ok {
			entry.AccountEmail = account.Email
		} else if account, ok := m.accounts[operator.String()]; ok {
			entry.AccountEmail = account.Email
		}
	}
	if _, has := payload["trace_id"]; !has && e.TraceID != nil {
		trace := *e.TraceID
		entry.TraceID = &trace
	}
	m.appendAuditLogLocked(entry)
}

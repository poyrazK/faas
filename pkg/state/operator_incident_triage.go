package state

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// OperatorIncidentTriage is the durable workflow metadata attached to one
// operator incident inbox dedupe key. Source incident rows remain owned by
// their controllers; this record only captures human triage state.
type OperatorIncidentTriage struct {
	DedupeKey string
	Status    string
	Owner     string
	Note      string
	UpdatedAt time.Time
	UpdatedBy string
}

const (
	OperatorIncidentTriageOpen         = "open"
	OperatorIncidentTriageAcknowledged = "acknowledged"
	OperatorIncidentTriageInProgress   = "in_progress"
	OperatorIncidentTriageResolved     = "resolved"
)

func validOperatorIncidentTriageStatus(status string) bool {
	switch status {
	case OperatorIncidentTriageOpen, OperatorIncidentTriageAcknowledged,
		OperatorIncidentTriageInProgress, OperatorIncidentTriageResolved:
		return true
	default:
		return false
	}
}

func validateOperatorIncidentTriage(dedupeKey, status, owner, note, updatedBy string) error {
	if strings.TrimSpace(dedupeKey) == "" || len(dedupeKey) > 255 {
		return fmt.Errorf("state: invalid operator incident dedupe key")
	}
	if !validOperatorIncidentTriageStatus(status) {
		return fmt.Errorf("state: invalid operator incident triage status %q", status)
	}
	if len(owner) > 128 || strings.IndexFunc(owner, unicode.IsControl) >= 0 || len(note) > 1024 || strings.TrimSpace(updatedBy) == "" || len(updatedBy) > 128 {
		return fmt.Errorf("state: invalid operator incident triage metadata")
	}
	return nil
}

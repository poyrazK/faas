package state

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"slices"
	"time"
)

func EventSchemaVersionMismatch(r PublishedEventRecipient, payload []byte) bool {
	if len(r.SchemaVersions) == 0 || len(r.Workflow) != 0 || r.ObjectNotification != nil {
		return false
	}
	var e eventcontract.Envelope
	if json.Unmarshal(payload, &e) != nil || e.Validate() != nil {
		return false
	}
	return sameMemUUID(r.AccountID, e.AccountID) && !slices.Contains(r.SchemaVersions, e.SchemaVersion)
}
func EventSchemaVersionFilteredProgress(previous PublishedEventRecipientProgress, attempts int, now time.Time) PublishedEventRecipientProgress {
	p := previous
	p.State, p.Attempts, p.UpdatedAt = PublishedEventRecipientFiltered, attempts, now
	p.FilterReason = "schema_version_mismatch"
	p.FailureCode, p.RetryStopReason, p.LastError, p.CapacityScope, p.DeliveryControlReason = "", "", "", "", ""
	p.Retryable, p.NextAttemptAt = false, nil
	return p
}

package tasktoolrelease

import (
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// Execution changes the separately authorized local adapter claim. The store
// supplies locked credential/app freshness, never a caller's freshness claim.
func Execution(record api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision, fresh bool, now time.Time) (api.TaskToolReleaseIntentRecord, error) {
	r := in.Execution
	if r == nil || !toolpublication.ValidID(r.OperationID) || !toolpublication.ValidID(r.BindingsSHA256) || !toolpublication.ValidID(in.ReleaseFingerprint) {
		return record, ErrInvalid
	}
	if r.Stage != "" && !slices.Contains([]string{"prepared", "deploying_worker", "deploying_web", "replacements_started", "web_promoted", "draining_previous", "previous_parked", "complete"}, r.Stage) {
		return record, ErrInvalid
	}
	record = View(record, now)
	if in.Decision == "authorize" {
		if in.IntentSHA256 != record.IntentSHA256 || in.ReleaseFingerprint != record.Intent.ReleaseFingerprint ||
			uuid.Validate(in.AuthorizerKeyID) != nil || r.Action != "authorize" || r.ClaimToken != "" {
			return record, ErrChanged
		}
		if record.Execution != nil {
			e := record.Execution
			if e.OperationID != r.OperationID || e.BindingsSHA256 != r.BindingsSHA256 || e.AuthorizerKeyID != in.AuthorizerKeyID {
				return record, ErrChanged
			}
			return record, nil // Never renew or replace an existing authority/claim.
		}
		if record.Status != "approved" || !fresh {
			return record, ErrChanged
		}
		record.Execution = &api.TaskToolReleaseExecution{OperationID: r.OperationID, BindingsSHA256: r.BindingsSHA256,
			AuthorizerKeyID: in.AuthorizerKeyID, State: "authorized", AuthorizedAt: now.UTC()}
		return record, nil
	}
	e := record.Execution
	if e == nil || e.OperationID != r.OperationID || e.BindingsSHA256 != r.BindingsSHA256 || !toolpublication.ValidID(r.ClaimToken) {
		return record, ErrChanged
	}
	// Copy the nested record so a rejected request cannot mutate shared memory.
	copy := *e
	record.Execution, e = &copy, &copy
	claim := toolpublication.Digest([]byte(r.ClaimToken))
	if r.Action == "claim" && e.State != "authorized" {
		return record, nil // Duplicate calls only inspect, never obtain a new lease.
	}
	if r.Action != "claim" && e.ClaimSHA256 != claim {
		return record, ErrChanged
	}
	if r.Action == "reconcile" && e.State == "running" {
		e.State = "reconcile_required"
		return record, nil
	}
	if record.Status != "approved" || in.ReleaseFingerprint != record.Intent.ReleaseFingerprint || !fresh {
		if record.Status == "approved" && (!fresh || in.ReleaseFingerprint != record.Intent.ReleaseFingerprint) {
			record.Status = "stale"
		}
		if e.State == "running" || e.State == "authorized" {
			e.State = "reconcile_required"
		}
		return record, nil
	}
	switch r.Action {
	case "claim":
		e.State, e.ClaimSHA256 = "running", claim
		e.Stage = "prepared"
	case "guard":
		if e.State != "running" {
			return record, ErrChanged
		}
		if r.Stage != "" {
			e.Stage = r.Stage
		}
		if r.Receipt != nil {
			if r.Stage != "" && r.Stage != r.Receipt.Stage {
				return record, ErrChanged
			}
			receipt, err := advanceReceipt(record, *r.Receipt)
			if err != nil {
				return record, err
			}
			e.Receipt, e.CompletedStages = receipt, ReceiptStages(*receipt)
		}
	case "complete":
		if e.State != "running" {
			return record, ErrChanged
		}
		if e.Receipt != nil && !receiptComplete(e.Receipt) {
			return record, ErrChanged
		}
		e.State = "completed"
		e.Stage = "complete"
	default:
		return record, ErrInvalid
	}
	return record, nil
}

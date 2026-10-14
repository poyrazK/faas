package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

var errManualObservationIdentity = errors.New("invalid manual recovery identity")

func validateManualObservationIdentity(r api.WorkflowInitialRecoveryReview) error {
	for _, id := range []string{r.ID, r.AppID, r.JobID, r.Target.RunID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return errManualObservationIdentity
		}
	}
	return nil
}

// Only deterministic data errors inside a claimed review can be deferred.
// Infrastructure, cancellation, locking and unknown errors still end the pass.
func manualObservationFailureCode(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ""
	}
	if errors.Is(err, errManualObservationIdentity) {
		return "invalid_recovery_identity"
	}
	var syntax *json.SyntaxError
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &mismatch) {
		return "invalid_observation_data"
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "23514", "22007", "22008", "22P02":
			return "invalid_observation_data"
		}
	}
	return ""
}

func manualObservationFailureFromRaw(raw []byte) *api.WorkflowInitialObservationFailure {
	var envelope struct {
		Failure *api.WorkflowInitialObservationFailure `json:"observation_failure"`
	}
	// Decode only the polling metadata; another invalid field must not prevent
	// repeated failures from accumulating bounded backoff.
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	return envelope.Failure
}

func nextManualObservationFailure(previous *api.WorkflowInitialObservationFailure, code string, now time.Time) *api.WorkflowInitialObservationFailure {
	attempts := int64(1)
	if previous != nil && previous.Attempts > 0 {
		attempts = previous.Attempts
		if attempts < api.WorkflowInitialObservationMaxAttempts {
			attempts++
		}
		if attempts > api.WorkflowInitialObservationMaxAttempts {
			attempts = api.WorkflowInitialObservationMaxAttempts
		}
	}
	delay := api.WorkflowRecoveryPolicyPollInterval
	for i := int64(1); i < attempts && delay < api.WorkflowInitialObservationMaxBackoff; i++ {
		delay *= 2
	}
	if delay > api.WorkflowInitialObservationMaxBackoff {
		delay = api.WorkflowInitialObservationMaxBackoff
	}
	now = now.UTC().Truncate(time.Microsecond)
	return &api.WorkflowInitialObservationFailure{Code: code, Attempts: attempts, FailedAt: now, RetryAt: now.Add(delay)}
}

func validateClaimedManualObservation(r api.WorkflowInitialRecoveryReview, id, app string) error {
	if !sameMemUUID(r.ID, id) || !sameMemUUID(r.AppID, app) {
		return fmt.Errorf("manual review claim mismatch: %w", errManualObservationIdentity)
	}
	return validateManualObservationIdentity(r)
}

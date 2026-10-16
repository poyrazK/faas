package api

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/appstandards"
)

// Approval names the exact saved preview; the store rechecks its inputs.
type ApproveApplicationStandardReviewRequest struct {
	ApprovalHash string `json:"approval_hash"`
}

func (r *ApproveApplicationStandardReviewRequest) UnmarshalJSON(raw []byte) error {
	type wire ApproveApplicationStandardReviewRequest
	var value wire
	if err := appstandards.DecodeStrict(raw, &value); err != nil {
		return err
	}
	*r = ApproveApplicationStandardReviewRequest(value)
	return r.Validate()
}

func (r ApproveApplicationStandardReviewRequest) Validate() error {
	decoded, err := hex.DecodeString(r.ApprovalHash)
	if err != nil || len(decoded) != 32 || r.ApprovalHash != strings.ToLower(r.ApprovalHash) {
		return fmt.Errorf("approval_hash must be the saved lowercase SHA-256 digest")
	}
	return nil
}

// UpdatedAt is an exact compare-and-swap token from a fresh operation read.
type ControlApplicationStandardOperationRequest struct {
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

func (r *ControlApplicationStandardOperationRequest) UnmarshalJSON(raw []byte) error {
	type wire ControlApplicationStandardOperationRequest
	var value wire
	if err := appstandards.DecodeStrict(raw, &value); err != nil {
		return err
	}
	*r = ControlApplicationStandardOperationRequest(value)
	return r.Validate()
}

func (r ControlApplicationStandardOperationRequest) Validate() error {
	if r.ExpectedUpdatedAt.IsZero() || !r.ExpectedUpdatedAt.Equal(r.ExpectedUpdatedAt.Truncate(time.Microsecond)) {
		return fmt.Errorf("expected_updated_at must be a nonzero timestamp with microsecond precision")
	}
	return nil
}

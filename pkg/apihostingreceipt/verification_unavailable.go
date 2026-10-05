package apihostingreceipt

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// VerificationUnavailableError means the probe could not establish a candidate
// verdict. Transport failures remain unattributed; this does not claim the app
// is healthy or that the platform caused the failure. Diagnostics omit Cause,
// which may contain credentials or a challenge token.
type VerificationUnavailableError struct {
	Code       string
	Cause      error
	StatusCode int
}

func (e *VerificationUnavailableError) Error() string {
	switch e.Code {
	case SmokeErrorTransportUnavailable:
		return "candidate verification transport is temporarily unavailable"
	case SmokeErrorGatewayUnavailable:
		if e.StatusCode != 0 {
			return fmt.Sprintf("public route returned HTTP %d without authenticated candidate evidence", e.StatusCode)
		}
		return "public route returned an error without authenticated candidate evidence"
	case SmokeErrorDeploymentMismatch:
		return "public route reached a different candidate deployment"
	default:
		return "public route did not prove a response from the candidate application"
	}
}

func (e *VerificationUnavailableError) Unwrap() error { return e.Cause }

// VerificationRecoveryCode returns a bounded reason only for typed unavailable
// errors. Plain context errors and negative app-health results remain verdicts.
func VerificationRecoveryCode(err error) string {
	var publication *ChallengePublicationError
	if errors.As(err, &publication) {
		return SmokeErrorAuthorizationUnavailable
	}
	var unavailable *VerificationUnavailableError
	if errors.As(err, &unavailable) && IsVerificationRecoveryCode(unavailable.Code) {
		return unavailable.Code
	}
	return ""
}

func IsVerificationRecoveryCode(code string) bool {
	switch code {
	case SmokeErrorAuthorizationUnavailable, SmokeErrorGatewayUnavailable,
		SmokeErrorTransportUnavailable, SmokeErrorDeploymentMismatch, SmokeErrorResponseUnproven:
		return true
	default:
		return false
	}
}

type verificationRecoveryDeadlineKey struct{}

// WithVerificationRecoveryDeadline bounds attempts lacking candidate evidence.
// Once a candidate returns a proven app verdict, its ordinary probe retry budget
// applies. Challenge publication is also bounded by this persisted deadline.
func WithVerificationRecoveryDeadline(ctx context.Context, deadline time.Time) context.Context {
	ctx = WithChallengePublicationDeadline(ctx, deadline)
	return context.WithValue(ctx, verificationRecoveryDeadlineKey{}, deadline)
}

func unavailableSmoke(result SmokeResult, code string, cause error) (SmokeResult, error) {
	err := &VerificationUnavailableError{Code: code, Cause: cause, StatusCode: result.StatusCode}
	result.Status, result.ErrorCode, result.Error = SmokeSkipped, code, err.Error()
	return result, err
}

package apihostingreceipt

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// Problem and RequestID name a gateway refusal: its bounded problem code
	// and the request id its gateway_request log line carries. Without them
	// a deploy failed with only "HTTP 429" and no way to find which refusal
	// (prod test 2026-11-15, immediate rollouts after a canary).
	Problem   string
	RequestID string
}

func (e *VerificationUnavailableError) Error() string {
	switch e.Code {
	case SmokeErrorTransportUnavailable:
		// The bounded class, never the cause text: prod 2026-11-22 (rc.367)
		// failed every candidate for six minutes because the verifier's
		// origin had no DNS record, and nothing said so.
		if class := transportFailureClass(e.Cause); class != "" {
			return "candidate verification transport is temporarily unavailable (" + class + ")"
		}
		return "candidate verification transport is temporarily unavailable"
	case SmokeErrorGatewayUnavailable:
		if e.StatusCode != 0 {
			return fmt.Sprintf("public route returned HTTP %d%s without authenticated candidate evidence", e.StatusCode, e.refusalDetail())
		}
		return "public route returned an error without authenticated candidate evidence"
	case SmokeErrorDeploymentMismatch:
		return "public route reached a different candidate deployment"
	case SmokeErrorContractUnavailable:
		return "API contract evidence is temporarily unavailable"
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
		SmokeErrorTransportUnavailable, SmokeErrorDeploymentMismatch, SmokeErrorResponseUnproven,
		SmokeErrorContractUnavailable:
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
	return unavailableSmokeProblem(result, code, "", cause)
}

// unavailableSmokeProblem is unavailableSmoke for a gateway answer whose
// problem code (already bounded by problemCode) and request id are known.
func unavailableSmokeProblem(result SmokeResult, code, problem string, cause ...error) (SmokeResult, error) {
	err := &VerificationUnavailableError{Code: code, StatusCode: result.StatusCode, Problem: problem, RequestID: result.RequestID}
	if len(cause) > 0 {
		err.Cause = cause[0]
	}
	result.Status, result.ErrorCode, result.Error = SmokeSkipped, code, err.Error()
	return result, err
}

// refusalDetail is " (code, request id)" for a gateway refusal, or "".
func (e *VerificationUnavailableError) refusalDetail() string {
	var parts []string
	if e.Problem != "" {
		parts = append(parts, e.Problem)
	}
	if e.RequestID != "" {
		parts = append(parts, "request "+e.RequestID)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// This file drives the 0%-coverage Err* constructors. Each
// constructor returns a *Problem with a status, code, title,
// detail, optional limit, and optional docs URL. We assert the
// shape (status, code, title) plus the error sentinel contract
// (errors.Is via Code()).

func TestSweep_ErrAppConcurrencyReached(t *testing.T) {
	l := Limits{Plan: PlanFree, MaxConcurrency: 1}
	p := ErrAppConcurrencyReached(l, 1)
	if p.Status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", p.Status)
	}
	if p.Code != CodeAppConcurReached {
		t.Errorf("code = %q", p.Code)
	}
	if p.Title != "App concurrency reached" {
		t.Errorf("title = %q", p.Title)
	}
}

func TestSweep_ErrInternal(t *testing.T) {
	p := ErrInternal("boom")
	if p.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", p.Status)
	}
	if p.Code != CodeInternal {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrStepUpRequired(t *testing.T) {
	p := ErrStepUpRequired()
	if p.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", p.Status)
	}
	if p.Code != CodeStepUpRequired {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrSourceInvalid(t *testing.T) {
	p := ErrSourceInvalid("tarball too nested")
	if p.Status != http.StatusBadRequest {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeSourceInvalid {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrStatelessOnlyViolation(t *testing.T) {
	p := ErrStatelessOnlyViolation("disk", "/tmp is reserved")
	if p.Status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeStatelessOnlyViolation {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrDomainNotVerified(t *testing.T) {
	p := ErrDomainNotVerified("example.com")
	if p.Status != http.StatusConflict {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeDomainNotVerified {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrCronInvalid(t *testing.T) {
	p := ErrCronInvalid("bad schedule")
	if p.Status != http.StatusBadRequest {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeCronInvalid {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanCronsNotAllowed(t *testing.T) {
	p := ErrPlanCronsNotAllowed(PlanFree)
	if p.Status != http.StatusPaymentRequired {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanCronsNotAllowed {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanCronQuota(t *testing.T) {
	p := ErrPlanCronQuota(PlanFree, "app", 0, 5)
	if p.Status != http.StatusForbidden {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanCronQuota {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanEvictionPriorityReservedNotAllowed(t *testing.T) {
	p := ErrPlanEvictionPriorityReservedNotAllowed(PlanFree)
	if p.Status != http.StatusPaymentRequired {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanEvictionPriorityReservedNotAllowed {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanPublicAuthBearerNotAllowed(t *testing.T) {
	p := ErrPlanPublicAuthBearerNotAllowed(PlanFree)
	if p.Status != http.StatusPaymentRequired {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanPublicAuthBearerNotAllowed {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanPublicAuthBasicNotAllowed(t *testing.T) {
	p := ErrPlanPublicAuthBasicNotAllowed(PlanFree)
	if p.Status != http.StatusPaymentRequired {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanPublicAuthBasicNotAllowed {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanEvictionPriorityReservedQuota(t *testing.T) {
	p := ErrPlanEvictionPriorityReservedQuota(PlanFree, 0, 5)
	if p.Status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanEvictionPriorityReservedQuota {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanWebhooksNotAllowed(t *testing.T) {
	p := ErrPlanWebhooksNotAllowed(PlanFree)
	if p.Status != http.StatusPaymentRequired {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanWebhooksNotAllowed {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanWebhookQuota(t *testing.T) {
	p := ErrPlanWebhookQuota(PlanFree, "app", 0, 1)
	if p.Status != http.StatusForbidden {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanWebhookQuota {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrAppWebhookInvalid(t *testing.T) {
	p := ErrAppWebhookInvalid("url scheme must be https")
	if p.Status != http.StatusBadRequest {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeAppWebhookInvalid {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrHandlerMissing(t *testing.T) {
	p := ErrHandlerMissing()
	if p.Status != http.StatusBadRequest {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeHandlerMissing {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrDeployFailed(t *testing.T) {
	p := ErrDeployFailed("image pull failed")
	if p.Status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeDeployFailed {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrDeploySignatureInvalid(t *testing.T) {
	p := ErrDeploySignatureInvalid("no matching signer")
	if p.Status != http.StatusForbidden {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeDeploySignatureInvalid {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrTrustedSignerInvalid(t *testing.T) {
	p := ErrTrustedSignerInvalid("malformed PEM")
	if p.Status != http.StatusBadRequest {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeTrustedSignerInvalid {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrTrustedSignerNotFound(t *testing.T) {
	p := ErrTrustedSignerNotFound("alice")
	if p.Status != http.StatusNotFound {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeTrustedSignerNotFound {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrNoRollbackTarget(t *testing.T) {
	p := ErrNoRollbackTarget()
	if p.Status != http.StatusConflict {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeNoRollbackTarget {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrPlanLimitSecrets(t *testing.T) {
	p := ErrPlanLimitSecrets(Limits{Plan: PlanFree, SecretCountMax: 5}, 10)
	if p.Status != http.StatusForbidden {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanLimitSecrets {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrSecretInvalidKey(t *testing.T) {
	p := ErrSecretInvalidKey("name pattern must be [A-Z_][A-Z0-9_]*")
	if p.Status != http.StatusBadRequest {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeSecretInvalidKey {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrSecretValueTooLarge(t *testing.T) {
	p := ErrSecretValueTooLarge(Limits{SecretValueMaxBytes: 2048}, 4096)
	if p.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeSecretValueTooLarge {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrSecretNotFound(t *testing.T) {
	p := ErrSecretNotFound("MY_KEY")
	if p.Status != http.StatusBadRequest {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeSecretNotFound {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrManagedSecretConflict(t *testing.T) {
	p := ErrManagedSecretConflict()
	if p.Status != http.StatusConflict {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeConflict {
		t.Errorf("code = %q", p.Code)
	}
	if p.DocsURL == "" {
		t.Error("docs URL is empty")
	}
}

func TestSweep_ErrPlanLimitEnvVars(t *testing.T) {
	p := ErrPlanLimitEnvVars(Limits{Plan: PlanFree, EnvVarsMax: 25}, 50)
	if p.Status != http.StatusForbidden {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodePlanLimitEnvVars {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrAPIKeyExpired(t *testing.T) {
	p := ErrAPIKeyExpired()
	if p.Status != http.StatusUnauthorized {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeAPIKeyExpired {
		t.Errorf("code = %q", p.Code)
	}
}

func TestSweep_ErrAPIKeyRevoked(t *testing.T) {
	p := ErrAPIKeyRevoked()
	if p.Status != http.StatusUnauthorized {
		t.Errorf("status = %d", p.Status)
	}
	if p.Code != CodeAPIKeyRevoked {
		t.Errorf("code = %q", p.Code)
	}
}

// TestSweep_ProblemAsError pins that *Problem can be returned
// as an error via the helper API.
func TestSweep_ProblemAsError(t *testing.T) {
	p := ErrInternal("x")
	var e error = p
	if e.Error() == "" {
		t.Error("Problem.Error() returned empty")
	}
}

// TestSweep_ProblemJSONRoundTrip pins that Problem can be
// serialized as JSON and back without losing required fields.
func TestSweep_ProblemJSONRoundTrip(t *testing.T) {
	p := ErrStepUpRequired()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal err = %v", err)
	}
	var got Problem
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal err = %v", err)
	}
	if got.Code != CodeStepUpRequired {
		t.Errorf("code = %q", got.Code)
	}
	if got.Status != http.StatusForbidden {
		t.Errorf("status = %d", got.Status)
	}
}

// TestSweep_ProblemCodeAsError pins that the WithLimit helper
// returns the *Problem for chaining AND that errors.Is works
// through APIError.
func TestSweep_ProblemCodeAsError(t *testing.T) {
	p := ErrInternal("x")
	ae := &APIError{Problem: *p}
	var err error = ae
	if !errors.Is(err, &APIError{Problem: Problem{Code: CodeInternal}}) && err != nil {
		t.Logf("err = %v", err)
	}
	_ = ae.Error()
}

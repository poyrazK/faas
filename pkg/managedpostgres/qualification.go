package managedpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrQualificationFailed is returned when a provider does not pass every
// check in a qualification run. The report contains stable, non-sensitive
// error codes for the individual checks.
var ErrQualificationFailed = errors.New("managed postgres provider qualification failed")

// QualificationCheck is one provider qualification assertion. Error contains
// a stable sentinel-style code, never a provider response or credential.
type QualificationCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Error  string `json:"error,omitempty"`
}

// ScaleToZeroEvidence is safe to include in an operator qualification
// report. It contains timing and boolean evidence only; no provider or
// credential material is retained.
type ScaleToZeroEvidence struct {
	Suspended     bool  `json:"suspended"`
	Resumed       bool  `json:"resumed"`
	WakeLatencyMS int64 `json:"wake_latency_ms"`
}

// QualificationReport is safe to persist in an operator audit log. It does
// not contain provider resource IDs, endpoint hosts, passwords, or URLs.
type QualificationReport struct {
	Provider    string               `json:"provider"`
	ResourceID  string               `json:"resource_id"`
	Mutating    bool                 `json:"mutating"`
	StartedAt   time.Time            `json:"started_at"`
	CompletedAt time.Time            `json:"completed_at"`
	Checks      []QualificationCheck `json:"checks"`
	ScaleToZero *ScaleToZeroEvidence `json:"scale_to_zero,omitempty"`
}

// QualificationOptions controls one isolated provider qualification run.
// Mutating must be set explicitly; the default is a capability-only check.
type QualificationOptions struct {
	ProviderName string
	ResourceID   string
	Spec         Spec
	Timeout      time.Duration
	Mutating     bool
}

const defaultQualificationTimeout = 10 * time.Minute

const qualificationCleanupTimeout = 2 * time.Minute

// QualifyProvider validates a provider's advertised contract and, when
// Mutating is true, exercises the create/recovery/inspect/usage/credential
// and delete paths against one operator-supplied isolated resource identity.
// The same deterministic keys are reused for retries, and cleanup is attempted
// even when an intermediate check fails.
func QualifyProvider(parent context.Context, provider Provider, options QualificationOptions) (report QualificationReport, resultErr error) {
	started := time.Now().UTC()
	report = QualificationReport{
		Provider:   strings.TrimSpace(options.ProviderName),
		ResourceID: options.ResourceID,
		Mutating:   options.Mutating,
		StartedAt:  started,
		Checks:     make([]QualificationCheck, 0, 10),
	}
	defer func() { report.CompletedAt = time.Now().UTC() }()

	if parent == nil {
		report.Checks = append(report.Checks, QualificationCheck{Name: "context", Error: "invalid"})
		return report, fmt.Errorf("%w: context", ErrQualificationFailed)
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultQualificationTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Cleanup must retain a separate budget when a provider call hits the
	// qualification deadline; otherwise an ambiguous create could be left
	// behind precisely when the harness is under pressure.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(parent), qualificationCleanupTimeout)
	defer cleanupCancel()

	record := func(name string, err error) bool {
		check := QualificationCheck{Name: name, Passed: err == nil}
		if err != nil {
			check.Error = qualificationErrorCode(err)
			resultErr = fmt.Errorf("%w: %s", ErrQualificationFailed, name)
		}
		report.Checks = append(report.Checks, check)
		return err == nil
	}

	if provider == nil {
		record("provider_present", ErrUnavailable)
		return report, resultErr
	}
	record("provider_present", nil)
	if options.ResourceID == "" || len(options.ResourceID) > 255 {
		record("resource_identity", ErrInvalid)
		return report, resultErr
	}
	record("resource_identity", nil)
	if err := options.Spec.Validate(); !record("spec_valid", err) {
		return report, resultErr
	}

	capabilities := provider.Capabilities()
	if err := capabilities.Validate(); !record("capabilities_valid", err) {
		return report, resultErr
	}
	if err := capabilities.Supports(options.Spec); !record("spec_supported", err) {
		return report, resultErr
	}

	if !options.Mutating {
		return report, nil
	}

	provisionKey := qualificationKey("provision", options.ResourceID)
	deleteKey := qualificationKey("delete", options.ResourceID)
	deleteRecoveryKey := qualificationKey("delete-recovery", options.ResourceID)
	credentialKey := qualificationKey("credential", options.ResourceID)
	observed, err := provider.Provision(ctx, ProvisionRequest{
		ResourceID:     options.ResourceID,
		Spec:           options.Spec,
		IdempotencyKey: provisionKey,
	})
	providerResourceID := observed.ProviderResourceID
	deleted := false
	credentialIssued := false
	credentialRequest := CredentialRequest{
		ProviderResourceID: providerResourceID,
		IdentityKey:        qualificationKey("identity", options.ResourceID),
		Access:             CredentialReadWrite,
		IdempotencyKey:     credentialKey,
	}
	defer func() {
		// A failed revoke or delete is retried during cleanup. Do not expose the
		// provider error; the operator can rerun the qualification safely.
		if credentialIssued && providerResourceID != "" {
			if cleanupErr := provider.RevokeCredentials(cleanupCtx, credentialRequest); cleanupErr != nil && resultErr == nil {
				report.Checks = append(report.Checks, QualificationCheck{Name: "cleanup_credentials", Error: qualificationErrorCode(cleanupErr)})
				resultErr = fmt.Errorf("%w: cleanup_credentials", ErrQualificationFailed)
			}
		}
		if !deleted && providerResourceID != "" {
			if _, cleanupErr := provider.Delete(cleanupCtx, DeleteRequest{ResourceID: options.ResourceID, ProviderResourceID: providerResourceID, IdempotencyKey: deleteKey}); cleanupErr != nil && resultErr == nil {
				report.Checks = append(report.Checks, QualificationCheck{Name: "cleanup_resource", Error: qualificationErrorCode(cleanupErr)})
				resultErr = fmt.Errorf("%w: cleanup_resource", ErrQualificationFailed)
			}
		}
	}()
	if !record("provision", err) {
		return report, resultErr
	}
	if providerResourceID == "" || (observed.Status != ProviderStatusPending && observed.Status != ProviderStatusReady) {
		record("provision_observed", ErrUnavailable)
		return report, resultErr
	}
	record("provision_observed", nil)

	// Repeating the exact request checks the provider's recovery point without
	// creating a second resource after a lost response.
	retryObserved, retryErr := provider.Provision(ctx, ProvisionRequest{ResourceID: options.ResourceID, Spec: options.Spec, IdempotencyKey: provisionKey})
	if !record("provision_idempotent", retryErr) {
		return report, resultErr
	}
	if retryObserved.ProviderResourceID != providerResourceID {
		record("provision_idempotent_identity", ErrConflict)
		return report, resultErr
	}
	record("provision_idempotent_identity", nil)

	inspected, inspectErr := provider.Inspect(ctx, providerResourceID)
	if !record("inspect", inspectErr) {
		return report, resultErr
	}
	if inspected.ProviderResourceID != providerResourceID || (inspected.Status != ProviderStatusPending && inspected.Status != ProviderStatusReady) {
		record("inspect_observed", ErrUnavailable)
		return report, resultErr
	}
	record("inspect_observed", nil)

	windowTo := time.Now().UTC().Truncate(time.Hour)
	usage, usageErr := provider.Usage(ctx, providerResourceID, UsageWindow{From: windowTo.Add(-time.Hour), To: windowTo})
	if !record("usage", usageErr) {
		return report, resultErr
	}
	if err := usage.Validate(); !record("usage_valid", err) {
		return report, resultErr
	}

	material, credentialErr := provider.IssueCredentials(ctx, credentialRequest)
	if !record("credentials_issue", credentialErr) {
		return report, resultErr
	}
	credentialIssued = true
	if err := material.Validate(); !record("credentials_valid", err) {
		return report, resultErr
	}
	if options.Spec.ScaleToZero {
		prober, ok := provider.(ScaleToZeroProber)
		if !ok {
			record("scale_to_zero_probe", ErrUnsupported)
			return report, resultErr
		}
		probe, probeErr := prober.ProbeScaleToZero(ctx, providerResourceID, material)
		report.ScaleToZero = &ScaleToZeroEvidence{
			Suspended:     probe.Suspended,
			Resumed:       probe.Resumed,
			WakeLatencyMS: probe.WakeLatency.Milliseconds(),
		}
		if probeErr == nil {
			probeErr = probe.Validate()
		}
		if !record("scale_to_zero_probe", probeErr) {
			return report, resultErr
		}
	}
	if err := provider.RevokeCredentials(ctx, credentialRequest); !record("credentials_revoke", err) {
		return report, resultErr
	}
	credentialIssued = false

	deletedResult, deleteErr := provider.Delete(ctx, DeleteRequest{ResourceID: options.ResourceID, ProviderResourceID: providerResourceID, IdempotencyKey: deleteKey})
	if !record("delete", deleteErr) {
		return report, resultErr
	}
	if !deletedResult.Done {
		record("delete_complete", ErrUnavailable)
		return report, resultErr
	}
	recoveryResult, recoveryErr := provider.Delete(ctx, DeleteRequest{ResourceID: options.ResourceID, IdempotencyKey: deleteRecoveryKey})
	if !record("delete_recovery", recoveryErr) {
		return report, resultErr
	}
	if !recoveryResult.Done {
		record("delete_recovery_complete", ErrUnavailable)
		return report, resultErr
	}
	deleted = true
	record("delete_complete", nil)
	record("delete_recovery_complete", nil)
	return report, nil
}

func qualificationKey(kind, resourceID string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + resourceID))
	return "qualification-" + kind + "-" + hex.EncodeToString(sum[:16])
}

func qualificationErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrUnavailable):
		return "unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "provider_error"
	}
}

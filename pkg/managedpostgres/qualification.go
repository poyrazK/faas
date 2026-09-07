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

// LifecycleQualificationReport contains the non-sensitive evidence from a
// control-plane lifecycle smoke. It deliberately reports only stable check
// codes: logical database, binding, provider resource, credential material,
// and connection URLs never cross this boundary.
type LifecycleQualificationReport struct {
	Checks []QualificationCheck `json:"checks"`
}

// LifecycleQualificationOptions identifies one disposable control-plane
// lifecycle. The caller must provide a unique account/name pair in an
// isolated staging environment; the helper always attempts binding and
// database cleanup before returning.
type LifecycleQualificationOptions struct {
	AccountID      string
	DatabaseName   string
	AppID          string
	Scope          string
	EnvironmentKey string
	Access         CredentialAccess
	Spec           Spec
	Timeout        time.Duration
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

// QualifyLifecycle exercises the provider-neutral control-plane saga with a
// disposable database and app binding. It is intended for the isolated
// staging qualification command, not customer traffic: the service and
// binding service supplied by the caller decide the exact provider adapter and
// credential sink. The operation is recoverable and cleanup runs even after an
// intermediate check fails.
func QualifyLifecycle(parent context.Context, service *Service, bindings *BindingService, options LifecycleQualificationOptions) (report LifecycleQualificationReport, resultErr error) {
	report.Checks = make([]QualificationCheck, 0, 11)
	if parent == nil {
		recordQualificationFailure(&report, "context", ErrInvalid, &resultErr)
		return report, resultErr
	}
	if service == nil {
		recordQualificationFailure(&report, "service_present", ErrUnavailable, &resultErr)
		return report, resultErr
	}
	report.Checks = append(report.Checks, QualificationCheck{Name: "service_present", Passed: true})
	if bindings == nil {
		recordQualificationFailure(&report, "binding_service_present", ErrUnavailable, &resultErr)
		return report, resultErr
	}
	report.Checks = append(report.Checks, QualificationCheck{Name: "binding_service_present", Passed: true})
	if options.AccountID == "" || !ValidName(options.DatabaseName) || options.AppID == "" || options.Scope == "" || options.EnvironmentKey == "" {
		recordQualificationFailure(&report, "lifecycle_identity", ErrInvalid, &resultErr)
		return report, resultErr
	}
	if options.Access != CredentialReadWrite && options.Access != CredentialReadOnly {
		recordQualificationFailure(&report, "lifecycle_access", ErrInvalid, &resultErr)
		return report, resultErr
	}
	if err := options.Spec.Validate(); err != nil {
		recordQualificationFailure(&report, "lifecycle_spec", err, &resultErr)
		return report, resultErr
	}
	report.Checks = append(report.Checks, QualificationCheck{Name: "lifecycle_identity", Passed: true})
	report.Checks = append(report.Checks, QualificationCheck{Name: "lifecycle_access", Passed: true})
	report.Checks = append(report.Checks, QualificationCheck{Name: "lifecycle_spec", Passed: true})

	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultQualificationTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	var database Database
	var binding Binding
	var bindingDeleted, databaseDeleted bool
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(parent), qualificationCleanupTimeout)
		defer cleanupCancel()
		if database.ID == "" {
			if candidates, listErr := service.List(cleanupCtx, options.AccountID); listErr == nil {
				for _, candidate := range candidates {
					if candidate.Name == options.DatabaseName {
						database = candidate
						break
					}
				}
			}
		}
		if binding.ID == "" && database.ID != "" {
			if candidates, listErr := bindings.List(cleanupCtx, options.AccountID, database.ID); listErr == nil && len(candidates) == 1 {
				binding = candidates[0]
			}
		}
		if binding.ID != "" && !bindingDeleted {
			if cleaned, cleanupErr := bindings.Delete(cleanupCtx, options.AccountID, binding.ID); cleanupErr == nil {
				binding = cleaned
				bindingDeleted = cleaned.State == BindingStateDeleted
			} else if resultErr == nil {
				recordQualificationFailure(&report, "cleanup_binding", cleanupErr, &resultErr)
			}
		}
		if database.ID != "" && !databaseDeleted {
			if cleaned, cleanupErr := qualifyLifecycleDelete(cleanupCtx, service, options.AccountID, database.ID, database); cleanupErr == nil {
				database = cleaned
				databaseDeleted = cleaned.State == StateDeleted
			} else if resultErr == nil {
				recordQualificationFailure(&report, "cleanup_database", cleanupErr, &resultErr)
			}
		}
	}()

	created, err := service.Create(ctx, CreateRequest{AccountID: options.AccountID, Name: options.DatabaseName, Spec: options.Spec})
	database = created
	if database.ID == "" {
		// Service.Create may have durably reserved the row before an upstream
		// error is returned. Discover that deterministic name so cleanup can
		// still remove a provider resource after a lost response.
		if candidates, listErr := service.List(ctx, options.AccountID); listErr == nil {
			for _, candidate := range candidates {
				if candidate.Name == options.DatabaseName {
					database = candidate
					break
				}
			}
		}
	}
	if err != nil {
		recordQualificationFailure(&report, "database_create", err, &resultErr)
		return report, resultErr
	}
	recordQualificationCheck(&report, "database_create", nil, &resultErr)
	ready, err := qualifyLifecycleReady(ctx, service, options.AccountID, database)
	database = ready
	if err != nil {
		recordQualificationFailure(&report, "database_ready", err, &resultErr)
		return report, resultErr
	}
	if database.State != StateReady || database.ProviderResourceID == "" {
		recordQualificationFailure(&report, "database_ready", ErrConflict, &resultErr)
		return report, resultErr
	}
	recordQualificationCheck(&report, "database_ready", nil, &resultErr)

	createdBinding, err := bindings.Create(ctx, CreateBindingRequest{
		AccountID: options.AccountID, DatabaseID: database.ID, AppID: options.AppID,
		Scope: options.Scope, EnvironmentKey: options.EnvironmentKey, Access: options.Access,
	})
	binding = createdBinding
	if binding.ID == "" {
		// BindingService intentionally returns a zero value with an error after
		// a durable reservation failure. Discover the reserved row by database
		// so the qualification cleanup can still revoke any issued credential
		// and remove the owned secret.
		if candidates, listErr := bindings.List(ctx, options.AccountID, database.ID); listErr == nil && len(candidates) == 1 {
			binding = candidates[0]
		}
	}
	if err != nil {
		recordQualificationFailure(&report, "binding_create", err, &resultErr)
		return report, resultErr
	}
	if binding.State != BindingStateReady || binding.ProviderIdentityID == "" || binding.CredentialRef == "" {
		recordQualificationFailure(&report, "binding_ready", ErrConflict, &resultErr)
		return report, resultErr
	}
	recordQualificationCheck(&report, "binding_create", nil, &resultErr)
	report.Checks = append(report.Checks, QualificationCheck{Name: "binding_ready", Passed: true})

	deletedBinding, err := bindings.Delete(ctx, options.AccountID, binding.ID)
	if deletedBinding.ID != "" {
		binding = deletedBinding
	}
	bindingDeleted = err == nil && deletedBinding.State == BindingStateDeleted
	if err != nil {
		recordQualificationFailure(&report, "binding_delete", err, &resultErr)
		return report, resultErr
	}
	if !bindingDeleted {
		recordQualificationFailure(&report, "binding_delete", ErrConflict, &resultErr)
		return report, resultErr
	}
	recordQualificationCheck(&report, "binding_delete", nil, &resultErr)

	deletedDatabase, err := qualifyLifecycleDelete(ctx, service, options.AccountID, database.ID, database)
	if deletedDatabase.ID != "" {
		database = deletedDatabase
	}
	databaseDeleted = err == nil && deletedDatabase.State == StateDeleted
	if err != nil {
		recordQualificationFailure(&report, "database_delete", err, &resultErr)
		return report, resultErr
	}
	if !databaseDeleted {
		recordQualificationFailure(&report, "database_delete", ErrConflict, &resultErr)
		return report, resultErr
	}
	recordQualificationCheck(&report, "database_delete", nil, &resultErr)
	return report, resultErr
}

func qualifyLifecycleReady(ctx context.Context, service *Service, accountID string, database Database) (Database, error) {
	if database.State == StateReady {
		return database, nil
	}
	for {
		if err := waitQualificationPoll(ctx, service.pollInterval); err != nil {
			return database, err
		}
		next, err := service.Reconcile(ctx, accountID, database.ID)
		if err != nil {
			return database, err
		}
		if next.State == StateReady {
			return next, nil
		}
		database = next
	}
}

func qualifyLifecycleDelete(ctx context.Context, service *Service, accountID, databaseID string, database Database) (Database, error) {
	for {
		next, err := service.Delete(ctx, accountID, databaseID)
		if err != nil {
			return database, err
		}
		database = next
		if database.State == StateDeleted {
			return database, nil
		}
		if err := waitQualificationPoll(ctx, service.pollInterval); err != nil {
			return database, err
		}
	}
}

func waitQualificationPoll(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func recordQualificationCheck(report *LifecycleQualificationReport, name string, err error, resultErr *error) bool {
	check := QualificationCheck{Name: name, Passed: err == nil}
	if err != nil {
		check.Error = qualificationErrorCode(err)
		if *resultErr == nil {
			*resultErr = fmt.Errorf("%w: %s", ErrQualificationFailed, name)
		}
	}
	report.Checks = append(report.Checks, check)
	return err == nil
}

func recordQualificationFailure(report *LifecycleQualificationReport, name string, err error, resultErr *error) {
	_ = recordQualificationCheck(report, name, err, resultErr)
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

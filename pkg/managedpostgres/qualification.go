package managedpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
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

// QualificationArtifactVersion is bumped whenever the approval document
// shape or validation semantics change incompatibly.
const QualificationArtifactVersion = 1

const qualificationArtifactVersion = QualificationArtifactVersion

var requiredProviderQualificationChecks = [...]string{
	"provider_present", "resource_identity", "spec_valid", "capabilities_valid", "spec_supported",
	"provision", "provision_observed", "provision_idempotent", "provision_idempotent_identity",
	"inspect", "inspect_observed", "usage", "usage_valid", "credentials_issue", "credentials_valid",
	"scale_to_zero_probe", "credentials_revoke", "delete", "delete_recovery", "delete_complete", "delete_recovery_complete",
}

var requiredLifecycleQualificationChecks = [...]string{
	"service_present", "binding_service_present", "lifecycle_identity", "lifecycle_access", "lifecycle_spec",
	"database_create", "database_ready", "binding_create", "binding_ready", "binding_delete", "database_delete",
}

// QualificationApproval is the non-secret approval material an operator may
// use to enable the staging provisioning gate. It is intentionally bound to a
// report digest and to the exact backend placement fingerprint, so changing a
// provider configuration cannot silently reuse an old qualification run.
type QualificationApproval struct {
	Version            int       `json:"version"`
	Provider           string    `json:"provider"`
	BackendID          string    `json:"backend_id"`
	BackendFingerprint string    `json:"backend_fingerprint"`
	QualifiedAt        time.Time `json:"qualified_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	ReportSHA256       string    `json:"report_sha256"`
	LifecycleValidated bool      `json:"lifecycle_validated"`
	CanaryAccounts     []string  `json:"canary_accounts,omitempty"`
}

// QualificationReadiness is a stable, machine-readable result for operator
// tooling. Reasons contain codes only; provider responses and credentials are
// never included.
type QualificationReadiness struct {
	Ready   bool     `json:"ready"`
	Reasons []string `json:"reasons,omitempty"`
}

// QualificationArtifact is the complete JSON document emitted by the
// qualification command. It contains only provider-neutral specs, stable
// check codes, and non-secret backend identity material.
type QualificationArtifact struct {
	Version            int                           `json:"version"`
	BackendID          string                        `json:"backend_id"`
	BackendFingerprint string                        `json:"backend_fingerprint"`
	Spec               Spec                          `json:"spec"`
	Report             QualificationReport           `json:"report"`
	Lifecycle          *LifecycleQualificationReport `json:"lifecycle,omitempty"`
	Approval           *QualificationApproval        `json:"approval,omitempty"`
	ApprovalEnv        map[string]string             `json:"approval_env,omitempty"`
	Readiness          QualificationReadiness        `json:"readiness"`
}

// LoadQualificationArtifact reads one operator-owned qualification artifact
// without contacting a provider. The size, JSON shape, and trailing-data
// checks are shared by the CLI verifier and the apid provisioning gate so a
// deployment cannot validate a different document from the one it consumes.
func LoadQualificationArtifact(path string) (QualificationArtifact, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return QualificationArtifact{}, ErrInvalid
	}
	file, err := os.Open(path) //nolint:forbidigo // operator-owned deployment artifact
	if err != nil {
		return QualificationArtifact{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return QualificationArtifact{}, ErrInvalid
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var artifact QualificationArtifact
	if err := decoder.Decode(&artifact); err != nil {
		return QualificationArtifact{}, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return QualificationArtifact{}, ErrInvalid
	}
	return artifact, nil
}

// BuildQualificationApproval creates an approval envelope from a successful
// provider qualification. A missing lifecycle report is allowed here so the
// command can still emit provider evidence, but readiness remains blocked
// until the control-plane lifecycle smoke is present and passing.
func BuildQualificationApproval(report QualificationReport, lifecycle *LifecycleQualificationReport, backendID, backendFingerprint string, canaryAccounts []string, now time.Time, ttl time.Duration) (QualificationApproval, error) {
	if err := ValidateQualificationReport(report); err != nil {
		return QualificationApproval{}, err
	}
	if strings.TrimSpace(backendID) == "" || backendID != strings.TrimSpace(backendID) || len(backendID) > 255 {
		return QualificationApproval{}, ErrInvalid
	}
	if strings.TrimSpace(backendFingerprint) == "" || backendFingerprint != strings.TrimSpace(backendFingerprint) || len(backendFingerprint) > 255 {
		return QualificationApproval{}, ErrInvalid
	}
	if lifecycle != nil {
		if err := ValidateLifecycleQualificationReport(*lifecycle); err != nil {
			return QualificationApproval{}, err
		}
	}
	accounts := append([]string(nil), canaryAccounts...)
	if err := validateCanaryAccounts(accounts); err != nil {
		return QualificationApproval{}, err
	}
	sort.Strings(accounts)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if ttl > 90*24*time.Hour {
		return QualificationApproval{}, ErrInvalid
	}
	qualifiedAt := report.CompletedAt.UTC()
	if qualifiedAt.IsZero() {
		qualifiedAt = now
	}
	if report.StartedAt.After(qualifiedAt) {
		return QualificationApproval{}, ErrInvalid
	}
	expiresAt := now.Add(ttl)
	if !expiresAt.After(qualifiedAt) {
		return QualificationApproval{}, ErrInvalid
	}
	return QualificationApproval{
		Version:            qualificationArtifactVersion,
		Provider:           report.Provider,
		BackendID:          backendID,
		BackendFingerprint: backendFingerprint,
		QualifiedAt:        qualifiedAt,
		ExpiresAt:          expiresAt,
		ReportSHA256:       qualificationReportSHA256(report),
		LifecycleValidated: lifecycle != nil,
		CanaryAccounts:     accounts,
	}, nil
}

// ValidateQualificationReport checks that a report is complete and contains
// no failed assertions. It is deliberately independent of any provider SDK.
func ValidateQualificationReport(report QualificationReport) error {
	if strings.TrimSpace(report.Provider) == "" || report.Provider != strings.TrimSpace(report.Provider) || report.ResourceID == "" || len(report.ResourceID) > 255 || !report.Mutating || report.StartedAt.IsZero() || report.CompletedAt.IsZero() || report.CompletedAt.Before(report.StartedAt) {
		return ErrInvalid
	}
	if !hasQualificationChecks(report.Checks, requiredProviderQualificationChecks[:]) {
		return ErrInvalid
	}
	if report.ScaleToZero == nil || !report.ScaleToZero.Suspended || !report.ScaleToZero.Resumed || report.ScaleToZero.WakeLatencyMS < 0 {
		return ErrUnavailable
	}
	return nil
}

// ValidateLifecycleQualificationReport checks that every lifecycle assertion
// passed and that the report is not an empty placeholder.
func ValidateLifecycleQualificationReport(report LifecycleQualificationReport) error {
	if !hasQualificationChecks(report.Checks, requiredLifecycleQualificationChecks[:]) {
		return ErrInvalid
	}
	return nil
}

func hasQualificationChecks(checks []QualificationCheck, required []string) bool {
	if len(checks) != len(required) {
		return false
	}
	seen := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		if strings.TrimSpace(check.Name) == "" || !check.Passed || (check.Passed && check.Error != "") {
			return false
		}
		seen[check.Name] = struct{}{}
	}
	if len(seen) != len(required) {
		return false
	}
	for _, name := range required {
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}

// EvaluateQualificationArtifact verifies an artifact against the currently
// configured backend and canary allowlist. It never performs provider calls.
// A non-ready result is expected while lifecycle qualification or operator
// gate configuration is incomplete.
func EvaluateQualificationArtifact(artifact QualificationArtifact, expectedBackendID, expectedBackendFingerprint string, expectedCanaryAccounts []string, now time.Time) QualificationReadiness {
	var reasons []string
	add := func(reason string) {
		for _, existing := range reasons {
			if existing == reason {
				return
			}
		}
		reasons = append(reasons, reason)
	}
	if artifact.Version != qualificationArtifactVersion {
		add("artifact_version_invalid")
	}
	if artifact.Approval == nil {
		add("approval_missing")
		return QualificationReadiness{Ready: false, Reasons: reasons}
	}
	approval := *artifact.Approval
	if approval.Version != qualificationArtifactVersion {
		add("approval_version_invalid")
	}
	if artifact.BackendID == "" || artifact.BackendID != approval.BackendID || artifact.BackendID != expectedBackendID {
		add("backend_mismatch")
	}
	if artifact.BackendFingerprint == "" || artifact.BackendFingerprint != approval.BackendFingerprint || artifact.BackendFingerprint != expectedBackendFingerprint {
		add("backend_fingerprint_mismatch")
	}
	if approval.Provider == "" || approval.Provider != artifact.Report.Provider {
		add("provider_mismatch")
	}
	if err := ValidateQualificationReport(artifact.Report); err != nil {
		switch {
		case errors.Is(err, ErrQualificationFailed):
			add("provider_checks_failed")
		case errors.Is(err, ErrUnavailable):
			add("scale_to_zero_evidence_missing")
		default:
			add("report_invalid")
		}
	}
	if approval.ReportSHA256 == "" || approval.ReportSHA256 != qualificationReportSHA256(artifact.Report) {
		add("report_digest_mismatch")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if approval.QualifiedAt.IsZero() || approval.ExpiresAt.IsZero() || !approval.ExpiresAt.After(approval.QualifiedAt) {
		add("approval_window_invalid")
	} else if !approval.ExpiresAt.After(now.UTC()) {
		add("approval_expired")
	}
	if !approval.QualifiedAt.Equal(artifact.Report.CompletedAt) || approval.ExpiresAt.Sub(approval.QualifiedAt) > 90*24*time.Hour {
		add("approval_window_invalid")
	}
	if !approval.LifecycleValidated {
		add("lifecycle_not_qualified")
	} else if artifact.Lifecycle == nil {
		add("lifecycle_report_missing")
	} else if err := ValidateLifecycleQualificationReport(*artifact.Lifecycle); err != nil {
		add("lifecycle_checks_failed")
	}
	if err := validateCanaryAccounts(approval.CanaryAccounts); err != nil {
		add("canary_accounts_invalid")
	} else if !sameCanaryAccounts(approval.CanaryAccounts, expectedCanaryAccounts) {
		add("canary_accounts_mismatch")
	}
	if len(artifact.ApprovalEnv) > 0 {
		if artifact.ApprovalEnv[QualificationEnv] != "true" || artifact.ApprovalEnv[QualificationBackendEnv] != approval.BackendID || artifact.ApprovalEnv[QualificationFingerprintEnv] != approval.BackendFingerprint || artifact.ApprovalEnv[QualificationUntilEnv] != approval.ExpiresAt.UTC().Format(time.RFC3339) {
			add("approval_env_mismatch")
		}
		if len(approval.CanaryAccounts) > 0 && artifact.ApprovalEnv[CanaryAccountsEnv] != strings.Join(approval.CanaryAccounts, ",") {
			add("approval_env_mismatch")
		}
	}
	return QualificationReadiness{Ready: len(reasons) == 0, Reasons: reasons}
}

// VerifyQualificationArtifact checks the artifact against the registry's
// single default backend and current staging canary allowlist. It is a pure
// configuration check; provider APIs are never contacted.
func (r *Registry) VerifyQualificationArtifact(artifact QualificationArtifact, expectedCanaryAccounts []string, now time.Time) QualificationReadiness {
	if r == nil {
		return QualificationReadiness{Reasons: []string{"registry_unavailable"}}
	}
	regions := r.Regions()
	if len(regions) != 1 {
		return QualificationReadiness{Reasons: []string{"default_backend_count_invalid"}}
	}
	backend, err := r.Default(regions[0])
	if err != nil {
		return QualificationReadiness{Reasons: []string{"default_backend_unavailable"}}
	}
	readiness := EvaluateQualificationArtifact(artifact, backend.ID, backend.Fingerprint, expectedCanaryAccounts, now)
	if artifact.Spec.Region != backend.Region {
		readiness.Reasons = append(readiness.Reasons, "spec_region_mismatch")
		readiness.Ready = false
	}
	if err := backend.Capabilities.Supports(artifact.Spec); err != nil {
		readiness.Reasons = append(readiness.Reasons, "spec_unsupported")
		readiness.Ready = false
	}
	return readiness
}

func qualificationReportSHA256(report QualificationReport) string {
	encoded, _ := json.Marshal(report)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func validateCanaryAccounts(accounts []string) error {
	if len(accounts) > 100 {
		return ErrInvalid
	}
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		if strings.TrimSpace(account) == "" || account != strings.TrimSpace(account) || len(account) > 255 {
			return ErrInvalid
		}
		if _, ok := seen[account]; ok {
			return ErrInvalid
		}
		seen[account] = struct{}{}
	}
	return nil
}

func sameCanaryAccounts(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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

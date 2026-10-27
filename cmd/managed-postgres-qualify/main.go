// Command managed-postgres-qualify runs an isolated provider qualification.
// It is intentionally a separate binary so a live run cannot be triggered by
// the apid process or by customer traffic.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

type qualificationOutput struct {
	Version              int                                           `json:"version"`
	SourceRevision       string                                        `json:"source_revision,omitempty"`
	SourceClean          bool                                          `json:"source_clean,omitempty"`
	SourceEvidenceSHA256 string                                        `json:"source_evidence_sha256,omitempty"`
	BackendID            string                                        `json:"backend_id"`
	BackendFingerprint   string                                        `json:"backend_fingerprint"`
	Spec                 managedpostgres.Spec                          `json:"spec"`
	Report               managedpostgres.QualificationReport           `json:"report"`
	Lifecycle            *managedpostgres.LifecycleQualificationReport `json:"lifecycle,omitempty"`
	Approval             *managedpostgres.QualificationApproval        `json:"approval,omitempty"`
	ApprovalEnv          map[string]string                             `json:"approval_env,omitempty"`
	Readiness            managedpostgres.QualificationReadiness        `json:"readiness"`
}

// configurationPreflightOutput is deliberately separate from the live
// qualification artifact. It contains only configuration and capability
// evidence; --check-config never calls a provider API and never emits a
// qualification approval.
type configurationPreflightOutput struct {
	BackendID          string                                 `json:"backend_id,omitempty"`
	BackendFingerprint string                                 `json:"backend_fingerprint,omitempty"`
	Spec               *managedpostgres.Spec                  `json:"spec,omitempty"`
	Checks             []managedpostgres.QualificationCheck   `json:"checks"`
	Warnings           []string                               `json:"warnings,omitempty"`
	Readiness          managedpostgres.QualificationReadiness `json:"readiness"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--check-config" {
		if len(os.Args) != 2 {
			_, _ = fmt.Fprintln(os.Stderr, "usage: managed-postgres-qualify --check-config")
			os.Exit(2)
		}
		os.Exit(runConfigurationPreflight(os.Getenv, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "--compute-cap" {
		if len(os.Args) != 2 {
			_, _ = fmt.Fprintln(os.Stderr, "usage: managed-postgres-qualify --compute-cap")
			os.Exit(2)
		}
		os.Exit(runComputeCap(os.Getenv, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "--verify" {
		os.Exit(runVerificationArguments(os.Args[2:], os.Getenv, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: managed-postgres-qualify [--check-config | --compute-cap | --verify [--approval PATH] [--rollout --guest-report PATH --runtime-manifest PATH]]")
		os.Exit(2)
	}
	os.Exit(run(os.Getenv, os.Stdout, os.Stderr))
}

func runConfigurationPreflight(getenv func(string) string, output, errorOutput io.Writer) int {
	result := configurationPreflightOutput{}
	failed := false
	addCheck := func(name string, passed bool, code string) {
		check := managedpostgres.QualificationCheck{Name: name, Passed: passed}
		if !passed {
			check.Error = code
			failed = true
		}
		result.Checks = append(result.Checks, check)
	}

	configPath := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_CONFIG"))
	addCheck("config_path", configPath != "", "configuration_path_missing")
	addCheck("staging_environment", strings.EqualFold(strings.TrimSpace(getenv(managedpostgres.EnvironmentEnv)), managedpostgres.QualificationStagingEnvironment), "environment_not_staging")

	if configPath == "" {
		return writeConfigurationPreflight(result, failed, errorOutput, output)
	}
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	addCheck("configuration_valid", err == nil && registry != nil, "configuration_invalid")
	if err != nil || registry == nil {
		return writeConfigurationPreflight(result, failed, errorOutput, output)
	}

	regions := registry.Regions()
	addCheck("single_default_backend", len(regions) == 1, "default_backend_count_invalid")
	addCheck("provisioning_disabled", !registry.ProvisioningEnabled, "provisioning_must_remain_disabled")

	backend, backendErr := registry.Default(registry.DefaultRegion)
	addCheck("default_backend", backendErr == nil, "default_backend_unavailable")
	if backendErr != nil {
		return writeConfigurationPreflight(result, failed, errorOutput, output)
	}
	result.BackendID = backend.ID
	result.BackendFingerprint = backend.Fingerprint
	_, fingerprintErr := hex.DecodeString(backend.Fingerprint)
	addCheck("placement_fingerprint", len(backend.Fingerprint) == sha256.Size*2 && fingerprintErr == nil, "placement_fingerprint_invalid")

	spec, specErr := qualificationSpec(backend, backend.Region)
	addCheck("qualification_spec", specErr == nil, "qualification_spec_unsupported")
	if specErr == nil {
		result.Spec = &spec
	}
	addCheck("plan_entitlements", preflightPlanEntitlements(backend), "plan_entitlement_unsupported")
	scaleLimits, scalePlanOK := api.ManagedPostgresLimitsFor(api.PlanScale)
	addCheck("global_database_ceiling", scalePlanOK && registry.MaxDatabasesPerAccount <= scaleLimits.DatabasesMax, "global_database_ceiling_invalid")

	_, canaryErr := managedpostgres.ParseStagingCanaryAccounts(getenv(managedpostgres.CanaryAccountsEnv))
	addCheck("canary_accounts", canaryErr == nil, "canary_accounts_invalid")

	resourceID := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID"))
	addCheck("qualification_resource_id", resourceID != "" && len(resourceID) <= 255, "qualification_resource_id_invalid")
	if timeoutValue := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_TIMEOUT")); timeoutValue != "" {
		timeout, timeoutErr := time.ParseDuration(timeoutValue)
		addCheck("qualification_timeout", timeoutErr == nil && timeout > 0, "qualification_timeout_invalid")
	} else {
		addCheck("qualification_timeout", true, "qualification_timeout_invalid")
	}
	_, approvalTTLErr := parseQualificationApprovalTTL(getenv)
	addCheck("approval_ttl", approvalTTLErr == nil, "approval_ttl_invalid")

	if !registry.UsagePolicy().Enabled {
		result.Warnings = append(result.Warnings, "usage_policy_disabled")
	}
	if !backend.Capabilities.RestoreUsageIsolated {
		result.Warnings = append(result.Warnings, "restore_usage_not_isolated")
	}
	if !backend.Capabilities.RestoreUsageIsolated && !backend.Capabilities.RestoreUsageIncludedInSource {
		result.Warnings = append(result.Warnings, "restore_usage_unaccounted")
	}
	return writeConfigurationPreflight(result, failed, errorOutput, output)
}

func writeConfigurationPreflight(result configurationPreflightOutput, failed bool, errorOutput, output io.Writer) int {
	result.Readiness = managedpostgres.QualificationReadiness{Ready: !failed}
	if failed {
		for _, check := range result.Checks {
			if !check.Passed && check.Error != "" {
				result.Readiness.Reasons = append(result.Readiness.Reasons, check.Error)
			}
		}
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintln(errorOutput, "cannot write managed postgres configuration preflight")
		return 1
	}
	if failed {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres configuration preflight failed")
		return 1
	}
	return 0
}

func preflightPlanEntitlements(backend managedpostgres.Backend) bool {
	plans := []struct {
		plan  api.Plan
		class managedpostgres.ServiceClass
	}{
		{plan: api.PlanHobby, class: managedpostgres.ClassDevelopment},
		{plan: api.PlanPro, class: managedpostgres.ClassBurstable},
		{plan: api.PlanScale, class: managedpostgres.ClassProduction},
	}
	major := 0
	for _, candidate := range backend.Capabilities.PostgresMajors {
		if candidate > major {
			major = candidate
		}
	}
	if major == 0 {
		return false
	}
	for _, item := range plans {
		limits, ok := api.ManagedPostgresLimitsFor(item.plan)
		if !ok || limits.DatabasesMax <= 0 {
			return false
		}
		spec := managedpostgres.Spec{
			Region:               backend.Region,
			PostgresMajor:        major,
			Class:                item.class,
			Availability:         managedpostgres.AvailabilitySingleZone,
			ScaleToZero:          true,
			StorageLimitBytes:    limits.StorageLimitBytes,
			RestoreWindowSeconds: limits.RestoreWindowSeconds,
		}
		if backend.Capabilities.Supports(spec) != nil {
			return false
		}
	}
	return true
}

// liveQualification is the shared setup of a live run.
type liveQualification struct {
	registry   *managedpostgres.Registry
	backend    managedpostgres.Backend
	resourceID string
	spec       managedpostgres.Spec
	timeout    time.Duration
}

func loadLiveQualification(getenv func(string) string, errorOutput io.Writer) (liveQualification, bool) {
	if !isLiveQualificationEnabled(getenv) {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification requires FAAS_ENVIRONMENT=staging and FAAS_MANAGED_POSTGRES_QUALIFY_LIVE=true")
		return liveQualification{}, false
	}
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification configuration is unavailable")
		return liveQualification{}, false
	}
	resourceID := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID"))
	if resourceID == "" || len(resourceID) > 255 {
		_, _ = fmt.Fprintln(errorOutput, "FAAS_MANAGED_POSTGRES_QUALIFY_RESOURCE_ID is required and must be at most 255 characters")
		return liveQualification{}, false
	}
	region := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_REGION"))
	if region == "" {
		region = registry.DefaultRegion
	}
	backend, err := registry.Default(region)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, "qualification region has no configured backend")
		return liveQualification{}, false
	}
	spec, err := qualificationSpec(backend, region)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, "configured backend cannot produce a qualification spec")
		return liveQualification{}, false
	}
	timeout := 20 * time.Minute
	if value := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_TIMEOUT")); value != "" {
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			_, _ = fmt.Fprintln(errorOutput, "FAAS_MANAGED_POSTGRES_QUALIFY_TIMEOUT must be a positive duration")
			return liveQualification{}, false
		}
	}
	return liveQualification{registry: registry, backend: backend, resourceID: resourceID, spec: spec, timeout: timeout}, true
}

// runComputeCap qualifies compute caps (ADR-20261025094915 decision 10) on a
// disposable resource and writes the artifact that
// FAAS_MANAGED_POSTGRES_CAP_ENFORCEMENT=on requires. It is separate from the
// provisioning approval, which an older apid decodes strictly.
func runComputeCap(getenv func(string) string, output, errorOutput io.Writer) int {
	live, ok := loadLiveQualification(getenv, errorOutput)
	if !ok {
		return 2
	}
	report, qualificationErr := managedpostgres.QualifyProvider(context.Background(), live.backend.Provider, managedpostgres.QualificationOptions{
		ProviderName: live.backend.Driver,
		ResourceID:   live.resourceID,
		Spec:         live.spec,
		Timeout:      live.timeout,
		Mutating:     true,
		ComputeCap:   true,
	})
	artifact := managedpostgres.ComputeCapQualification{
		Version: managedpostgres.ComputeCapQualificationVersion, BackendID: live.backend.ID,
		BackendFingerprint: live.backend.Fingerprint, CompletedAt: report.CompletedAt, Checks: report.Checks,
	}
	if report.ComputeCap != nil {
		artifact.Evidence = *report.ComputeCap
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(artifact); err != nil {
		return 1
	}
	if qualificationErr != nil || live.registry.VerifyComputeCapQualification(artifact) != nil {
		_, _ = fmt.Fprintln(errorOutput, "compute cap qualification failed; see the checks and evidence")
		return 1
	}
	return 0
}

func run(getenv func(string) string, output, errorOutput io.Writer) int {
	live, ok := loadLiveQualification(getenv, errorOutput)
	if !ok {
		return 2
	}
	registry, backend, resourceID, spec, timeout := live.registry, live.backend, live.resourceID, live.spec, live.timeout
	approvalTTL, err := parseQualificationApprovalTTL(getenv)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, managedpostgres.QualificationApprovalTTLEnv+" must be positive and no longer than 90 days")
		return 2
	}
	continueAfterUsageFailure, err := parseContinueAfterUsageFailure(getenv)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, "FAAS_MANAGED_POSTGRES_QUALIFY_CONTINUE_AFTER_USAGE_FAILURE must be a boolean")
		return 2
	}
	canaryAccounts, canaryErr := managedpostgres.ParseStagingCanaryAccounts(getenv(managedpostgres.CanaryAccountsEnv))
	if canaryErr != nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification canary accounts are invalid")
		return 2
	}
	var durable durableQualificationConfig
	if durableQualificationEnabled(getenv) {
		preflight, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		durable, err = loadDurableQualification(preflight, getenv, spec, timeout)
		cancel()
		if err != nil {
			_, _ = fmt.Fprintln(errorOutput, "durable qualification requires a marked disposable SQL catalog, owned app/account, and private key files")
			return 2
		}
	}
	report, qualificationErr := managedpostgres.QualifyProvider(context.Background(), backend.Provider, managedpostgres.QualificationOptions{
		ProviderName:              backend.Driver,
		ResourceID:                resourceID,
		Spec:                      spec,
		Timeout:                   timeout,
		Mutating:                  true,
		ContinueAfterUsageFailure: continueAfterUsageFailure,
	})
	result := qualificationOutput{BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Spec: spec, Report: report}
	if qualificationErr == nil && durableQualificationEnabled(getenv) {
		lifecycle, lifecycleErr := managedpostgres.QualifyDurableLifecycle(context.Background(), durable.factory(getenv), durable.options)
		result.Lifecycle = &lifecycle
		qualificationErr = lifecycleErr
	} else if qualificationErr == nil && isLifecycleQualificationEnabled(getenv) {
		store := managedpostgres.NewMemoryStore()
		sink := &qualificationCredentialSink{refs: make(map[string]struct{})}
		lifecycleService, serviceErr := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{
			PollInterval:        2 * time.Second,
			ProvisioningEnabled: func() bool { return true },
		})
		if serviceErr == nil {
			bindingService, bindingErr := managedpostgres.NewBindingService(registry, store, store, sink, managedpostgres.BindingServiceOptions{
				ProviderTimeout:     30 * time.Second,
				ProvisioningEnabled: func() bool { return true },
			})
			if bindingErr == nil {
				lifecycle, lifecycleErr := managedpostgres.QualifyLifecycle(context.Background(), lifecycleService, bindingService, managedpostgres.LifecycleQualificationOptions{
					AccountID:      "qualification-account",
					DatabaseName:   qualificationDatabaseName(resourceID),
					AppID:          "qualification-app",
					Scope:          "default",
					EnvironmentKey: "DATABASE_URL",
					Access:         managedpostgres.CredentialReadWrite,
					Spec:           spec,
					Timeout:        timeout,
				})
				result.Lifecycle = &lifecycle
				if lifecycleErr != nil {
					qualificationErr = lifecycleErr
				}
			} else {
				qualificationErr = fmt.Errorf("%w: lifecycle_binding_service", managedpostgres.ErrQualificationFailed)
			}
		} else {
			qualificationErr = fmt.Errorf("%w: lifecycle_service", managedpostgres.ErrQualificationFailed)
		}
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	result.Version = managedpostgres.QualificationArtifactVersion
	if qualificationErr == nil {
		approval, approvalErr := managedpostgres.BuildQualificationApproval(
			result.Report, result.Lifecycle, result.BackendID, result.BackendFingerprint,
			canaryAccounts, time.Now().UTC(), approvalTTL,
		)
		if approvalErr == nil {
			result.Approval = &approval
		} else {
			qualificationErr = fmt.Errorf("%w: approval", managedpostgres.ErrQualificationFailed)
		}
	}
	artifact := managedpostgres.QualificationArtifact{
		Version:            result.Version,
		BackendID:          result.BackendID,
		BackendFingerprint: result.BackendFingerprint,
		Spec:               result.Spec,
		Report:             result.Report,
		Lifecycle:          result.Lifecycle,
		Approval:           result.Approval,
		ApprovalEnv:        result.ApprovalEnv,
	}
	artifact.SourceRevision, artifact.SourceClean = qualificationExecutableSource()
	artifact.SourceEvidenceSHA256 = managedpostgres.QualificationSourceDigest(artifact)
	result.SourceRevision, result.SourceClean, result.SourceEvidenceSHA256 = artifact.SourceRevision, artifact.SourceClean, artifact.SourceEvidenceSHA256
	result.Readiness = managedpostgres.EvaluateQualificationArtifact(artifact, result.BackendID, result.BackendFingerprint, canaryAccounts, time.Now().UTC())
	if result.Readiness.Ready && result.Approval != nil {
		result.ApprovalEnv = approvalEnvironment(*result.Approval)
		artifact.ApprovalEnv = result.ApprovalEnv
		result.Readiness = managedpostgres.EvaluateQualificationArtifact(artifact, result.BackendID, result.BackendFingerprint, canaryAccounts, time.Now().UTC())
	}
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintln(errorOutput, "cannot write qualification report")
		return 1
	}
	if qualificationErr != nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres provider qualification failed")
		return 1
	}
	return 0
}

func parseContinueAfterUsageFailure(getenv func(string) string) (bool, error) {
	value := strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_CONTINUE_AFTER_USAGE_FAILURE"))
	if value == "" {
		return false, nil
	}
	return strconv.ParseBool(value)
}

const (
	qualificationApprovalTTLDefault = 24 * time.Hour
	qualificationApprovalTTLMax     = 90 * 24 * time.Hour
)

func qualificationApprovalTTL(getenv func(string) string) time.Duration {
	ttl, err := parseQualificationApprovalTTL(getenv)
	if err != nil {
		return qualificationApprovalTTLDefault
	}
	return ttl
}

func parseQualificationApprovalTTL(getenv func(string) string) (time.Duration, error) {
	if getenv == nil {
		return qualificationApprovalTTLDefault, nil
	}
	value := strings.TrimSpace(getenv(managedpostgres.QualificationApprovalTTLEnv))
	if value == "" {
		return qualificationApprovalTTLDefault, nil
	}
	ttl, err := time.ParseDuration(value)
	if err != nil || ttl <= 0 || ttl > qualificationApprovalTTLMax {
		return 0, errors.New("invalid approval ttl")
	}
	return ttl, nil
}

func approvalEnvironment(approval managedpostgres.QualificationApproval) map[string]string {
	values := map[string]string{
		managedpostgres.QualificationEnv:            "true",
		managedpostgres.QualificationVersionEnv:     strconv.Itoa(approval.Version),
		managedpostgres.QualificationBackendEnv:     approval.BackendID,
		managedpostgres.QualificationFingerprintEnv: approval.BackendFingerprint,
		managedpostgres.QualificationUntilEnv:       approval.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if len(approval.CanaryAccounts) > 0 {
		values[managedpostgres.CanaryAccountsEnv] = strings.Join(approval.CanaryAccounts, ",")
	}
	return values
}

func runVerify(getenv func(string) string, path string, output, errorOutput io.Writer) int {
	if getenv == nil || !strings.EqualFold(strings.TrimSpace(getenv(managedpostgres.EnvironmentEnv)), managedpostgres.QualificationStagingEnvironment) {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification verification requires FAAS_ENVIRONMENT=staging")
		return 2
	}
	if strings.TrimSpace(path) == "" {
		_, _ = fmt.Fprintln(errorOutput, "an approval artifact path is required via --approval or "+managedpostgres.QualificationApprovalPathEnv)
		return 2
	}
	artifact, err := readQualificationArtifact(path)
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification approval artifact is unavailable")
		return 2
	}
	registry, err := managedpostgres.Load(getenv, map[string]managedpostgres.Factory{"neon": neon.New})
	if err != nil || registry == nil {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification configuration is unavailable")
		return 2
	}
	canaryAccounts, err := managedpostgres.ParseStagingCanaryAccounts(getenv(managedpostgres.CanaryAccountsEnv))
	if err != nil {
		_, _ = fmt.Fprintln(errorOutput, "FAAS_MANAGED_POSTGRES_CANARY_ACCOUNTS is malformed")
		return 2
	}
	readiness := registry.VerifyQualificationArtifact(artifact, canaryAccounts, time.Now().UTC())
	result := struct {
		ArtifactVersion int                                    `json:"artifact_version"`
		Readiness       managedpostgres.QualificationReadiness `json:"readiness"`
	}{ArtifactVersion: artifact.Version, Readiness: readiness}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintln(errorOutput, "cannot write qualification verification")
		return 1
	}
	if !readiness.Ready {
		_, _ = fmt.Fprintln(errorOutput, "managed postgres qualification approval is not ready")
		return 1
	}
	return 0
}

func readQualificationArtifact(path string) (managedpostgres.QualificationArtifact, error) {
	return managedpostgres.LoadQualificationArtifact(path)
}

func isLiveQualificationEnabled(getenv func(string) string) bool {
	if getenv == nil || !strings.EqualFold(strings.TrimSpace(getenv(managedpostgres.EnvironmentEnv)), managedpostgres.QualificationStagingEnvironment) {
		return false
	}
	approved, err := strconv.ParseBool(strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_LIVE")))
	return err == nil && approved
}

func isLifecycleQualificationEnabled(getenv func(string) string) bool {
	if !isLiveQualificationEnabled(getenv) {
		return false
	}
	approved, err := strconv.ParseBool(strings.TrimSpace(getenv("FAAS_MANAGED_POSTGRES_QUALIFY_LIFECYCLE")))
	return err == nil && approved
}

func qualificationDatabaseName(resourceID string) string {
	sum := sha256.Sum256([]byte(resourceID))
	return "qualification-" + hex.EncodeToString(sum[:6])
}

// qualificationCredentialSink validates the provider material and records
// only opaque ownership references. It intentionally does not retain a
// password or connection URL; the production app-secret sink is exercised by
// the apid integration tests.
type qualificationCredentialSink struct {
	refs map[string]struct{}
}

func (s *qualificationCredentialSink) Put(_ context.Context, binding managedpostgres.Binding, material managedpostgres.CredentialMaterial) (string, error) {
	if err := material.Validate(); err != nil {
		return "", err
	}
	ref := "qualification-secret-" + binding.ID
	s.refs[ref] = struct{}{}
	return ref, nil
}

func (s *qualificationCredentialSink) Delete(_ context.Context, binding managedpostgres.Binding) error {
	delete(s.refs, "qualification-secret-"+binding.ID)
	return nil
}

func qualificationSpec(backend managedpostgres.Backend, region string) (managedpostgres.Spec, error) {
	capabilities := backend.Capabilities
	if err := capabilities.Validate(); err != nil {
		return managedpostgres.Spec{}, err
	}
	if !capabilities.ScaleToZero {
		return managedpostgres.Spec{}, managedpostgres.ErrUnsupported
	}
	major := capabilities.PostgresMajors[0]
	for _, candidate := range capabilities.PostgresMajors[1:] {
		if candidate > major {
			major = candidate
		}
	}
	class := managedpostgres.ClassDevelopment
	if !containsServiceClass(capabilities.ServiceClasses, class) {
		if len(capabilities.ServiceClasses) == 0 {
			return managedpostgres.Spec{}, errors.New("no service class")
		}
		class = capabilities.ServiceClasses[0]
	}
	availability := managedpostgres.AvailabilitySingleZone
	if !containsAvailability(capabilities.Availability, availability) {
		if len(capabilities.Availability) == 0 {
			return managedpostgres.Spec{}, errors.New("no availability")
		}
		availability = capabilities.Availability[0]
	}
	storage := int64(1 << 30)
	if capabilities.MaxStorageBytes > 0 && capabilities.MaxStorageBytes < storage {
		storage = capabilities.MaxStorageBytes
	}
	if storage <= 0 {
		return managedpostgres.Spec{}, errors.New("no storage capacity")
	}
	restoreWindow := int64(0)
	if capabilities.PointInTimeRestore {
		restoreWindow = int64(24 * time.Hour / time.Second)
		if capabilities.MaxRestoreWindowSeconds > 0 && capabilities.MaxRestoreWindowSeconds < restoreWindow {
			restoreWindow = capabilities.MaxRestoreWindowSeconds
		}
	}
	spec := managedpostgres.Spec{
		Region:               region,
		PostgresMajor:        major,
		Class:                class,
		Availability:         availability,
		ScaleToZero:          capabilities.ScaleToZero,
		StorageLimitBytes:    storage,
		RestoreWindowSeconds: restoreWindow,
	}
	if err := capabilities.Supports(spec); err != nil {
		return managedpostgres.Spec{}, err
	}
	return spec, nil
}

func containsServiceClass(values []managedpostgres.ServiceClass, wanted managedpostgres.ServiceClass) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsAvailability(values []managedpostgres.Availability, wanted managedpostgres.Availability) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

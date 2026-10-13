package managedpostgres

import (
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
)

const GuestQualificationPathEnv = "FAAS_MANAGED_POSTGRES_GUEST_QUALIFICATION_PATH"
const RuntimeManifestPathEnv = "FAAS_MANAGED_POSTGRES_RUNTIME_MANIFEST_PATH"
const RequireGuestQualificationEnv = "FAAS_MANAGED_POSTGRES_REQUIRE_GUEST_QUALIFICATION"

// RolloutReadiness combines the existing provider/durable contract with the
// independently executed guest suite. Evaluating it never enables provisioning.
type RolloutReadiness struct {
	Version           int                          `json:"version"`
	Ready             bool                         `json:"ready"`
	ProviderLifecycle QualificationReadiness       `json:"provider_lifecycle"`
	Guest             guestqualification.Readiness `json:"guest"`
	Reasons           []string                     `json:"reasons,omitempty"`
}

func (r *Registry) VerifyRolloutQualification(artifact QualificationArtifact, guest *guestqualification.Report, expected *guestqualification.RuntimeManifest, canaryAccounts []string, now time.Time) RolloutReadiness {
	result := RolloutReadiness{Version: 1, ProviderLifecycle: r.VerifyQualificationArtifact(artifact, canaryAccounts, now), Guest: guestqualification.Evaluate(guest, expected, artifact.Spec.PostgresMajor, now)}
	expectedRevision := ""
	if expected != nil && expected.Valid() {
		expectedRevision = expected.SourceRevision
	}
	if reasons := qualificationSourceReasons(artifact, expectedRevision, now); len(reasons) != 0 {
		result.ProviderLifecycle.Ready = false
		result.ProviderLifecycle.Reasons = append(result.ProviderLifecycle.Reasons, reasons...)
	}
	for _, reason := range result.ProviderLifecycle.Reasons {
		result.Reasons = append(result.Reasons, "provider_"+reason)
	}
	result.Reasons = append(result.Reasons, result.Guest.Reasons...)
	result.Ready = result.ProviderLifecycle.Ready && result.Guest.Ready
	return result
}

// Supplying either guest input opts in even if the boolean is false, so a
// partially configured canary cannot silently fall back to provider evidence.
func loadGuestQualification(getenv func(string) string) (*guestqualification.Report, *guestqualification.RuntimeManifest, bool, error) {
	if getenv == nil {
		return nil, nil, false, nil
	}
	guestPath := strings.TrimSpace(getenv(GuestQualificationPathEnv))
	runtimePath := strings.TrimSpace(getenv(RuntimeManifestPathEnv))
	required, err := GuestQualificationRequired(getenv)
	if err != nil {
		return nil, nil, true, err
	}
	if !required {
		return nil, nil, false, nil
	}
	if guestPath == "" || runtimePath == "" {
		return nil, nil, true, ErrUnavailable
	}
	guest, err := guestqualification.Load[guestqualification.Report](guestPath)
	if err != nil {
		return nil, nil, true, ErrInvalid
	}
	manifest, err := guestqualification.Load[guestqualification.RuntimeManifest](runtimePath)
	if err != nil {
		return nil, nil, true, ErrInvalid
	}
	return &guest, &manifest, true, nil
}

// GuestQualificationRequired is shared by daemon and CLI policy evaluation.
func GuestQualificationRequired(getenv func(string) string) (bool, error) {
	if getenv == nil {
		return false, nil
	}
	required := strings.TrimSpace(getenv(GuestQualificationPathEnv)) != "" || strings.TrimSpace(getenv(RuntimeManifestPathEnv)) != ""
	if value := strings.TrimSpace(getenv(RequireGuestQualificationEnv)); value != "" {
		flag, err := strconv.ParseBool(value)
		if err != nil {
			return true, ErrInvalid
		}
		required = required || flag
	}
	return required, nil
}

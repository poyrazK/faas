package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Compute cap qualification (ADR-20261025094915 decision 10). Before any
// environment enforces caps, a staging run on a disposable database must show
// the provider behaves as the enforcer assumes. The evidence is its own
// artifact, not part of the provisioning approval: an older apid decodes that
// approval strictly and would reject an unknown field.

// CapQualificationPathEnv names the compute cap qualification artifact that
// FAAS_MANAGED_POSTGRES_CAP_ENFORCEMENT=on requires.
const CapQualificationPathEnv = "FAAS_MANAGED_POSTGRES_CAP_QUALIFICATION_PATH"

// ComputeCapQualificationVersion is the artifact's schema version. Version 2
// checks what enforcement relies on since ADR-20261025094915 amendment
// 2026-11-05: the consumption report and the hold, not the provider's quota.
// A version 1 artifact no longer decodes.
const ComputeCapQualificationVersion = 2

// ComputeCapEvidence is what the staging probe observed.
type ComputeCapEvidence struct {
	// PeriodStartsAtUTCMonth: the provider's consumption period starts within
	// an hour of the UTC month start, where the ledger's month starts.
	PeriodStartsAtUTCMonth bool `json:"period_starts_at_utc_month"`
	// ReportCurrent: the consumption report grew while the compute was
	// active, read within minutes, the open hour included.
	ReportCurrent bool `json:"report_current"`
	// ReportInComputeUnits: it grew by the compute's size times the time it
	// was active, so the sweep and the ledger share a unit.
	// ComputeUnitRatioPermille is that ratio in thousandths.
	ReportInComputeUnits     bool  `json:"report_in_compute_units"`
	ComputeUnitRatioPermille int64 `json:"compute_unit_ratio_permille"`
	// SuspendedWhileHeld, RefusedWhileHeld, ResumedAfterRelease: the hold
	// the sweep places suspends the compute and refuses a connection, and
	// releasing it lets the next connection wake it.
	SuspendedWhileHeld  bool `json:"suspended_while_held"`
	RefusedWhileHeld    bool `json:"refused_while_held"`
	ResumedAfterRelease bool `json:"resumed_after_release"`
	// StorageLimitPreserved: the backstop quota write left the storage
	// limit as it was.
	StorageLimitPreserved bool `json:"storage_limit_preserved"`
}

func (e ComputeCapEvidence) Validate() error {
	if !e.PeriodStartsAtUTCMonth || !e.ReportCurrent || !e.ReportInComputeUnits || !e.SuspendedWhileHeld ||
		!e.RefusedWhileHeld || !e.ResumedAfterRelease || !e.StorageLimitPreserved {
		return ErrUnavailable
	}
	return nil
}

// ComputeCapProber runs the staging probe. It changes only disposable
// qualification resources.
type ComputeCapProber interface {
	ProbeComputeCap(ctx context.Context, providerResourceID string, material CredentialMaterial) (ComputeCapEvidence, error)
}

// ComputeUnitRatioAcceptable judges a report-to-allocation ratio, in
// thousandths: the consumption report grew by allocated compute units times
// active seconds, between the compute's smallest and largest size, with a
// quarter of slack for the report's delay.
func ComputeUnitRatioAcceptable(ratioPermille int64, minimumCU, maximumCU float64) bool {
	if minimumCU <= 0 || maximumCU < minimumCU {
		return false
	}
	return ratioPermille >= 800 && float64(ratioPermille) <= 1000*maximumCU/minimumCU*1.25
}

// ComputeCapQualification is the artifact `managed-postgres-qualify
// --compute-cap` writes.
type ComputeCapQualification struct {
	Version            int                  `json:"version"`
	BackendID          string               `json:"backend_id"`
	BackendFingerprint string               `json:"backend_fingerprint"`
	CompletedAt        time.Time            `json:"completed_at"`
	Evidence           ComputeCapEvidence   `json:"evidence"`
	Checks             []QualificationCheck `json:"checks"`
}

// LoadComputeCapQualification reads one operator-owned artifact strictly.
func LoadComputeCapQualification(path string) (ComputeCapQualification, error) {
	return loadCapQualification[ComputeCapQualification](path)
}

// loadCapQualification decodes one operator-owned artifact strictly: unknown
// fields and trailing data are refused.
func loadCapQualification[T any](path string) (T, error) {
	var artifact T
	path = strings.TrimSpace(path)
	if path == "" {
		return artifact, ErrInvalid
	}
	file, err := os.Open(path) //nolint:forbidigo // operator-owned deployment artifact
	if err != nil {
		return artifact, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		var zero T
		return zero, ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		var zero T
		return zero, ErrInvalid
	}
	return artifact, nil
}

// VerifyComputeCapQualification accepts an artifact made against a backend
// this registry configures, with complete evidence and every check passed.
func (r *Registry) VerifyComputeCapQualification(artifact ComputeCapQualification) error {
	if artifact.Version != ComputeCapQualificationVersion || artifact.CompletedAt.IsZero() {
		return fmt.Errorf("compute cap qualification version or completion missing: %w", ErrInvalid)
	}
	backend, err := r.Resolve(artifact.BackendID, artifact.BackendFingerprint)
	if err != nil {
		return fmt.Errorf("compute cap qualification names a backend this configuration does not have: %w", ErrInvalid)
	}
	if _, ok := backend.Provider.(ComputeCapProvider); !ok {
		return fmt.Errorf("backend %s cannot hold compute quotas: %w", backend.ID, ErrUnsupported)
	}
	if _, ok := backend.Provider.(ComputeHoldProvider); !ok {
		return fmt.Errorf("backend %s cannot hold computes: %w", backend.ID, ErrUnsupported)
	}
	if len(artifact.Checks) == 0 {
		return fmt.Errorf("compute cap qualification has no checks: %w", ErrInvalid)
	}
	for _, check := range artifact.Checks {
		if !check.Passed {
			return fmt.Errorf("compute cap qualification check %s failed: %w", check.Name, ErrUnavailable)
		}
	}
	if err := artifact.Evidence.Validate(); err != nil {
		return fmt.Errorf("compute cap qualification evidence incomplete: %w", err)
	}
	return nil
}

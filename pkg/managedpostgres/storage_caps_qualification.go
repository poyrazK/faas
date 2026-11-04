package managedpostgres

import (
	"context"
	"fmt"
	"time"
)

// Storage pool qualification (ADR-20261025094915 decisions 9 and 10). Before
// any environment enforces the storage pool, a staging run on a disposable
// database must show that a branch at its storage limit refuses writes that
// would grow it while it stays readable and its compute keeps running: the
// provider's documentation disagrees on whether the compute is suspended. Like
// the compute cap evidence, it is its own artifact.

// StorageCapQualificationPathEnv names the storage pool qualification artifact
// that FAAS_MANAGED_POSTGRES_STORAGE_CAP_ENFORCEMENT=on requires.
const StorageCapQualificationPathEnv = "FAAS_MANAGED_POSTGRES_STORAGE_CAP_QUALIFICATION_PATH"

// StorageCapQualificationVersion is the artifact's schema version.
const StorageCapQualificationVersion = 1

// StorageCapEvidence is what the staging probe observed.
type StorageCapEvidence struct {
	// SizeReported: the provider reported the branch's size growing after
	// the probe wrote data.
	SizeReported bool `json:"size_reported"`
	// WritesRefusedAtLimit: with the limit at the branch's size, a write that
	// grows it failed.
	WritesRefusedAtLimit bool `json:"writes_refused_at_limit"`
	// ReadsAllowedAtLimit: reads kept working at the limit.
	ReadsAllowedAtLimit bool `json:"reads_allowed_at_limit"`
	// ComputeKeptRunning: the compute was never suspended at the limit.
	ComputeKeptRunning bool `json:"compute_kept_running"`
	// WritesAllowedAfterRaise: raising the limit let writes through again.
	WritesAllowedAfterRaise bool `json:"writes_allowed_after_raise"`
	// ComputeQuotaPreserved: the storage limit writes left the compute quota
	// as it was.
	ComputeQuotaPreserved bool `json:"compute_quota_preserved"`
}

func (e StorageCapEvidence) Validate() error {
	if !e.SizeReported || !e.WritesRefusedAtLimit || !e.ReadsAllowedAtLimit || !e.ComputeKeptRunning ||
		!e.WritesAllowedAfterRaise || !e.ComputeQuotaPreserved {
		return ErrUnavailable
	}
	return nil
}

// StorageCapProber runs the staging probe. It changes only disposable
// qualification resources, and leaves their storage limit as it found it.
type StorageCapProber interface {
	ProbeStorageCap(ctx context.Context, providerResourceID string, material CredentialMaterial) (StorageCapEvidence, error)
}

// StorageCapQualification is the artifact `managed-postgres-qualify
// --storage-cap` writes.
type StorageCapQualification struct {
	Version            int                  `json:"version"`
	BackendID          string               `json:"backend_id"`
	BackendFingerprint string               `json:"backend_fingerprint"`
	CompletedAt        time.Time            `json:"completed_at"`
	Evidence           StorageCapEvidence   `json:"evidence"`
	Checks             []QualificationCheck `json:"checks"`
}

// LoadStorageCapQualification reads one operator-owned artifact strictly.
func LoadStorageCapQualification(path string) (StorageCapQualification, error) {
	return loadCapQualification[StorageCapQualification](path)
}

// VerifyStorageCapQualification accepts an artifact made against a backend
// this registry configures, with complete evidence and every check passed.
func (r *Registry) VerifyStorageCapQualification(artifact StorageCapQualification) error {
	if artifact.Version != StorageCapQualificationVersion || artifact.CompletedAt.IsZero() {
		return fmt.Errorf("storage cap qualification version or completion missing: %w", ErrInvalid)
	}
	backend, err := r.Resolve(artifact.BackendID, artifact.BackendFingerprint)
	if err != nil {
		return fmt.Errorf("storage cap qualification names a backend this configuration does not have: %w", ErrInvalid)
	}
	if _, ok := backend.Provider.(StorageCapProvider); !ok {
		return fmt.Errorf("backend %s cannot hold storage limits: %w", backend.ID, ErrUnsupported)
	}
	if len(artifact.Checks) == 0 {
		return fmt.Errorf("storage cap qualification has no checks: %w", ErrInvalid)
	}
	for _, check := range artifact.Checks {
		if !check.Passed {
			return fmt.Errorf("storage cap qualification check %s failed: %w", check.Name, ErrUnavailable)
		}
	}
	if err := artifact.Evidence.Validate(); err != nil {
		return fmt.Errorf("storage cap qualification evidence incomplete: %w", err)
	}
	return nil
}

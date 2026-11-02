package managedpostgres

import "context"

type ResizeEvidence struct {
	IdentityPreserved     bool `json:"identity_preserved"`
	DataPreserved         bool `json:"data_preserved"`
	CredentialsPreserved  bool `json:"credentials_preserved"`
	ReplayStable          bool `json:"replay_stable"`
	OriginalClassRestored bool `json:"original_class_restored"`
}

func (e ResizeEvidence) Validate() error {
	if !e.IdentityPreserved || !e.DataPreserved || !e.CredentialsPreserved || !e.ReplayStable || !e.OriginalClassRestored {
		return ErrUnavailable
	}
	return nil
}

// ComputeResizeProber is called only on disposable qualification resources.
type ComputeResizeProber interface {
	ProbeComputeResize(context.Context, string, Spec) (ResizeEvidence, error)
}

// StorageResizeEvidence proves a storage-limit growth keeps the database
// whole and lands the new limit. Storage never shrinks, so unlike a class
// change it cannot be undone on the qualification resource.
type StorageResizeEvidence struct {
	IdentityPreserved    bool `json:"identity_preserved"`
	DataPreserved        bool `json:"data_preserved"`
	CredentialsPreserved bool `json:"credentials_preserved"`
	ReplayStable         bool `json:"replay_stable"`
	LimitApplied         bool `json:"limit_applied"`
}

func (e StorageResizeEvidence) Validate() error {
	if !e.IdentityPreserved || !e.DataPreserved || !e.CredentialsPreserved || !e.ReplayStable || !e.LimitApplied {
		return ErrUnavailable
	}
	return nil
}

// StorageResizeProber is called only on disposable qualification resources,
// after every probe that expects the qualification spec.
type StorageResizeProber interface {
	ProbeStorageResize(context.Context, string, Spec) (StorageResizeEvidence, error)
}

// MajorUpgradeEvidence proves an in-place major upgrade, to TargetMajor,
// keeps the database whole and lands the new major. Like storage growth it
// cannot be undone on the qualification resource.
type MajorUpgradeEvidence struct {
	TargetMajor          int  `json:"target_major"`
	IdentityPreserved    bool `json:"identity_preserved"`
	DataPreserved        bool `json:"data_preserved"`
	CredentialsPreserved bool `json:"credentials_preserved"`
	ReplayStable         bool `json:"replay_stable"`
	MajorApplied         bool `json:"major_applied"`
}

func (e MajorUpgradeEvidence) Validate() error {
	if e.TargetMajor <= 0 || !e.IdentityPreserved || !e.DataPreserved || !e.CredentialsPreserved || !e.ReplayStable || !e.MajorApplied {
		return ErrUnavailable
	}
	return nil
}

// MajorUpgradeProber is called only on disposable qualification resources,
// after every probe that expects the qualification spec. It upgrades the
// resource to the next major its backend offers above the spec's.
type MajorUpgradeProber interface {
	ProbeMajorUpgrade(context.Context, string, Spec) (MajorUpgradeEvidence, error)
}

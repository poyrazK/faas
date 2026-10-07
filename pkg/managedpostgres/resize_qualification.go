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

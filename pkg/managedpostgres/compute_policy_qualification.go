package managedpostgres

import "context"

type ComputePolicyEvidence struct {
	IdentityPreserved      bool `json:"identity_preserved"`
	DataPreserved          bool `json:"data_preserved"`
	CredentialsPreserved   bool `json:"credentials_preserved"`
	ReplayStable           bool `json:"replay_stable"`
	OriginalPolicyRestored bool `json:"original_policy_restored"`
	AlwaysOnObserved       bool `json:"always_on_observed"`
	Suspended              bool `json:"suspended"`
	Resumed                bool `json:"resumed"`
}

func (e ComputePolicyEvidence) Validate() error {
	if !e.IdentityPreserved || !e.DataPreserved || !e.CredentialsPreserved || !e.ReplayStable ||
		!e.OriginalPolicyRestored || !e.AlwaysOnObserved || !e.Suspended || !e.Resumed {
		return ErrUnavailable
	}
	return nil
}

// ComputePolicyProber changes only disposable qualification resources.
type ComputePolicyProber interface {
	ProbeComputePolicy(context.Context, string, Spec) (ComputePolicyEvidence, error)
}

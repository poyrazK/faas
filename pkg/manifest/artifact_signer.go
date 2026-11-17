package manifest

import "fmt"

// DefaultArtifactSignerPort is signd's port on the control plane's private
// address (ADR-20261109170000).
const DefaultArtifactSignerPort = 8097

// ArtifactSigner turns on the control plane's artifact signer, signd
// (ADR-20261109170000). Absent leaves every compute node signing its own
// layers with sign.key.
type ArtifactSigner struct {
	// Enabled starts signd on the control plane and switches every compute
	// node's imaged to sign app layers through it.
	Enabled bool `yaml:"enabled,omitempty"`
	// Port is signd's port on the control plane's private address; zero
	// means DefaultArtifactSignerPort.
	Port int `yaml:"port,omitempty"`
	// RetireComputeKey removes sign.key from the compute nodes. Set it only
	// after `gregalectl artifact resign` reports nothing left to convert
	// (docs/runbooks/artifact-signer-rollout.md).
	RetireComputeKey bool `yaml:"retire_compute_key,omitempty"`
}

// ListenPort is signd's port.
func (a ArtifactSigner) ListenPort() int {
	if a.Port == 0 {
		return DefaultArtifactSignerPort
	}
	return a.Port
}

func (a ArtifactSigner) validate() Errors {
	var errs Errors
	if a.Port != 0 && (a.Port < 1024 || a.Port > 65535) {
		errs = append(errs, Error{"artifact_signer.port", fmt.Sprintf("%d must be between 1024 and 65535", a.Port)})
	}
	if a.RetireComputeKey && !a.Enabled {
		errs = append(errs, Error{"artifact_signer.retire_compute_key", "needs artifact_signer.enabled: compute nodes without sign.key sign through signd"})
	}
	if !a.Enabled && a.Port != 0 {
		errs = append(errs, Error{"artifact_signer.port", "is set but artifact_signer.enabled is not"})
	}
	return errs
}

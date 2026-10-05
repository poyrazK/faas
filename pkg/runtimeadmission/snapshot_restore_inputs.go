package runtimeadmission

// adr: 593. Historical capture lineage never replaces fresh restore authority.

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CheckRestoreInputs checks lineage against a fresh boot binding. The issuing
// store must independently fence the catalog's immutable, node-independent
// inputs against current intent. CapturedInputHash includes the target node;
// comparing it with the historical parent's hash would forbid cross-node use.
func (c SnapshotCapture) CheckRestoreInputs(binding Binding, sources []ArtifactSource, memoryKey, vmstateKey string, memoryBytes int64, now time.Time) error {
	if err := binding.Validate(now); err != nil {
		return err
	}
	if binding.ProtocolVersion != ArtifactProtocolVersion || c.Check(now) != nil {
		return ErrInvalid
	}
	parent := c.Parent.Binding
	if c.Parent.Paused || binding.Token == parent.Token || binding.InstanceID == parent.InstanceID {
		return ErrReplay
	}
	if binding.AppID != parent.AppID || binding.AccountID != parent.AccountID || binding.DeploymentID != parent.DeploymentID || binding.DesiredRevision != parent.DesiredRevision || binding.EffectiveHash != parent.EffectiveHash || binding.EgressRevision != parent.EgressRevision {
		return ErrStale
	}
	hash, err := HashArtifactSources(sources)
	if err != nil || hash != binding.ArtifactSourcesHash || hash != parent.ArtifactSourcesHash {
		return ErrStale
	}
	if c.Memory.StorageKey != memoryKey || c.VMState.StorageKey != vmstateKey || c.Memory.Bytes != memoryBytes {
		return ErrInvalid
	}
	if time.Unix(0, binding.IssuedAtUnixNano).Add(api.ApplicationStandardRuntimeAdmissionClockSkew).Before(time.Unix(0, c.CapturedAtUnixNano)) {
		return ErrStale
	}
	return nil
}

package runtimeadmission

// adr: 595 This private native request check does not enable paused publication.

import (
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// CheckSnapshotResumeRequest checks a fresh request against complete historical
// paused-load identity. Receipt.Check and Promotion.Validate continue refusing
// protocol-2 paused history until the durable promotion proof is implemented.
func (p Promotion) CheckSnapshotResumeRequest(now time.Time) error {
	if err := p.Binding.Validate(now); err != nil {
		return err
	}
	r := p.Parent
	if r.Binding.ProtocolVersion != ArtifactProtocolVersion || !r.Paused || r.Method != vmmdpb.WakeMethod_WAKE_RESTORE || r.CompletedAtUnixNano <= 0 || r.CompletedAtUnixNano > now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew).UnixNano() {
		return ErrInvalid
	}
	if err := r.checkRuntimeIdentity(r.Binding, time.Unix(0, r.CompletedAtUnixNano)); err != nil {
		return err
	}
	if err := r.SnapshotConsumption.Check(r.Binding, r.ArtifactConsumption, true); err != nil {
		return err
	}
	return p.checkFreshPromotionBinding()
}

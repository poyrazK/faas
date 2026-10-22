package fcvm

import (
	"context"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// Only the native manager produces this receipt, after both physical updates.
// Its startup identity is immutable. A request for another process never writes.
func (m *Manager) UpdateAdmittedAppEgressPolicy(ctx context.Context, expected runtimeadmission.Identity, p runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
	p = p.Clone()
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		return runtimeadmission.EgressReceipt{}, err
	}
	if identity != expected {
		return runtimeadmission.EgressReceipt{}, runtimeadmission.ErrStale
	}
	if identity.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion {
		return runtimeadmission.EgressReceipt{}, runtimeadmission.ErrUnavailable
	}
	hash, err := p.Hash()
	if err != nil {
		return runtimeadmission.EgressReceipt{}, err
	}
	ports := make([]uint16, len(p.Ports))
	for n, port := range p.Ports {
		ports[n] = uint16(port)
	}
	if err := m.UpdateAppEgressPolicy(ctx, p.AppID, p.Revision, p.Allowlist, ports); err != nil {
		return runtimeadmission.EgressReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtimeadmission.EgressReceipt{}, err
	}
	return runtimeadmission.EgressReceipt{Identity: identity, AppID: p.AppID, Revision: p.Revision, PolicyHash: hash}, nil
}

package vmmdgrpc

// adr: 620
import (
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func (s *Server) regionalRuntimeHostEpoch() *vmmdpb.RegionalRuntimeHostEpoch {
	provider, ok := s.vmm.(interface {
		RegionalRuntimeHostEpoch() (runtimequota.HostEpoch, error)
	})
	if !ok {
		return nil
	}
	epoch, err := provider.RegionalRuntimeHostEpoch()
	if err != nil || epoch.Validate() != nil {
		return nil
	}
	return &vmmdpb.RegionalRuntimeHostEpoch{NodeId: epoch.NodeID, HostEpochId: epoch.HostEpochID}
}

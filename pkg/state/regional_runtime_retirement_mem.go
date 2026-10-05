package state

// adr: 631
import (
	"context"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func (s *MemStore) BeginRegionalRuntimeRetirement(ctx context.Context, p runtimequota.CapacityPermit, fence string) (RegionalRuntimeCapacityReceipt, error) {
	if p.Validate() != nil || !validRegionalReleaseIDs(fence, fence, fence) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	key := capacityKey(p.Grant.Binding.RegionalAppID, p.ID)
	old, ok := s.regionalRuntimeCapacity[key]
	if !ok {
		return RegionalRuntimeCapacityReceipt{}, ErrNotFound
	}
	r, err := beginRegionalRetirement(old, p, fence)
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	s.regionalRuntimeCapacity[key] = r
	s.closeSeatsForPhysicalLocked(r)
	return cloneRegionalCapacity(r), nil
}

func (s *MemStore) completeRegionalRuntimeRetirement(ctx context.Context, stop runtimequota.CapacityStop, signer runtimequota.Signer, cells runtimequota.Trust) (RegionalRuntimeCapacityReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	key := capacityKey(stop.Permit.Grant.Binding.RegionalAppID, stop.Permit.ID)
	old, ok := s.regionalRuntimeCapacity[key]
	if !ok {
		return RegionalRuntimeCapacityReceipt{}, ErrNotFound
	}
	r, err := completeRegionalRetirement(old, stop, signer, cells)
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	s.regionalRuntimeCapacity[key] = cloneRegionalCapacity(r)
	s.closeSeatsForPhysicalLocked(r)
	return cloneRegionalCapacity(r), nil
}

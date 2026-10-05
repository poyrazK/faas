package state

// adr: 634
import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func (s *MemStore) BeginRegionalRuntimeLaunchFailure(ctx context.Context, source runtimequota.LaunchSource, fence string) (RegionalRuntimeCapacityReceipt, error) {
	if source.Validate() != nil || !validRegionalReleaseIDs(fence, fence, fence) {
		return RegionalRuntimeCapacityReceipt{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	p := source.Permit
	key := capacityKey(p.Grant.Binding.RegionalAppID, p.ID)
	old, ok := s.regionalRuntimeCapacity[key]
	if !ok || s.regionalRuntimeLaunchSources[key].Source != source || old.Permit != p || old.IssuedAt == nil {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	if ins, ok := s.instances[source.InstanceKey]; ok && !RegionalRuntimeAttemptMatches(source, ins) {
		return RegionalRuntimeCapacityReceipt{}, ErrConflict
	}
	// Resume issuance uses this same lock. Even a completed resume proves this
	// physical runtime has outlived the initial launch whose failure was caught.
	for _, resume := range s.regionalRuntimeServingResumes {
		if resume.Intent.Seat.Permit == p {
			return RegionalRuntimeCapacityReceipt{}, ErrConflict
		}
	}
	r, err := beginRegionalRetirement(old, p, fence)
	if err != nil {
		return RegionalRuntimeCapacityReceipt{}, err
	}
	s.regionalRuntimeCapacity[key] = r
	s.closeSeatsForPhysicalLocked(r)
	return cloneRegionalCapacity(r), nil
}

func (s *MemStore) FailRegionalRuntimeAttempt(ctx context.Context, source runtimequota.LaunchSource, expected string) error {
	next := RegionalRuntimeFailureState(State(expected))
	if source.Validate() != nil || next == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	key := capacityKey(source.Permit.Grant.Binding.RegionalAppID, source.Permit.ID)
	if err := checkRegionalAttemptFailure(source, s.regionalRuntimeLaunchSources[key], s.regionalRuntimeCapacity[key]); err != nil {
		return err
	}
	old, ok := s.instances[source.InstanceKey]
	if !ok || old.State != expected || !RegionalRuntimeAttemptMatches(source, old) {
		return ErrConflict
	}
	if err := validateWorkerInstanceMutation(old, string(next), old.Mode); err != nil {
		return err
	}
	ins := old
	ins.State = string(next)
	if ins.TerminalAt == nil {
		at := time.Now().UTC()
		ins.TerminalAt = &at
	}
	if err := s.checkServiceCapacityInstanceLocked(ins); err != nil {
		return err
	}
	if err := s.guardInstanceRuntimeTransitionLocked(old, ins); err != nil {
		return err
	}
	s.instances[source.InstanceKey] = ins
	if s.hostPorts != nil {
		s.hostPorts.Release(old.NodeID, source.InstanceKey)
	}
	return nil
}

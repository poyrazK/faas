package dbsched

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

var recoveryRegion = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func ValidRecoveryRegion(region string) bool { return recoveryRegion.MatchString(region) }

func (s *Scheduler) CheckRecoveryTarget(ctx context.Context, source, region string, target time.Time, major int, latest bool) (dbarchive.RestoreCheck, error) {
	unverified := dbarchive.RestoreCheck{Outcome: dbarchive.RestoreUnverified, Reason: "recovery_archive_unavailable"}
	if !dbarchive.ValidDatabaseID(source) || !ValidRecoveryRegion(region) || target.IsZero() || !latest && target.After(s.now()) {
		return unverified, ErrInvalid
	}
	if latest && target.After(s.now()) {
		target = s.now()
	}
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return unverified, err
	}
	for _, node := range nodes {
		if node.RecoveryRegion != region || node.Lifecycle != NodeActive || !node.offers(major) {
			continue
		}
		check, err := s.probeRecoveryArchive(ctx, node, source, target, major, latest)
		if err == nil && check.Reason != "recovery_archive_unconfigured" {
			return check, nil
		}
	}
	return unverified, nil
}

func (s *Scheduler) probeRecoveryArchive(ctx context.Context, node Node, source string, target time.Time, major int, latest bool) (dbarchive.RestoreCheck, error) {
	checker, ok := s.vmm.(interface {
		CheckRecoveryArchive(context.Context, Node, string, time.Time, int, bool) (dbarchive.RestoreCheck, error)
	})
	if !ok {
		return dbarchive.RestoreCheck{}, ErrNodeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return checker.CheckRecoveryArchive(ctx, node, source, target, major, latest)
}

func (s *Scheduler) restoreFromRecovery(ctx context.Context, req RestoreRequest) (VM, error) {
	if req.Target.After(s.now()) {
		return VM{}, ErrInvalid
	}
	match := func(vm VM) (VM, error) {
		if !vm.RestoreFromRecovery || vm.RestoreSource != req.Source ||
			!vm.RestoreTarget.Equal(req.Target) || vm.RecoveryRegion != req.RecoveryRegion {
			return vm, ErrConflict
		}
		s.resumeRestore(ctx, vm)
		return vm, nil
	}
	if vm, err := s.store.Get(ctx, req.DatabaseID); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return VM{}, err
		}
		return match(vm)
	}
	check, err := s.CheckRecoveryTarget(ctx, req.Source, req.RecoveryRegion, req.Target, req.Major, false)
	if err != nil {
		return VM{}, err
	}
	if check.Outcome != dbarchive.RestoreCovered {
		return VM{}, fmt.Errorf("%w: recovery archive cannot reach the requested point", ErrConflict)
	}
	var timeline uint32
	if check.Timeline > 1 {
		timeline = check.Timeline
	}
	vm, err := s.store.Place(ctx, VM{
		DatabaseID: req.DatabaseID, RecoveryRegion: req.RecoveryRegion, RestoreFromRecovery: true,
		Major: req.Major, VcpuCount: req.VcpuCount, MemMiB: req.MemMiB, VolumeBytes: req.VolumeBytes,
		AlwaysOn: req.AlwaysOn, RestoreWindowSeconds: req.RestoreWindowSeconds, Replicated: req.Replicated,
		RestoreSource: req.Source, RestoreTarget: req.Target, RestoreTimeline: timeline,
	})
	if err != nil {
		return vm, err
	}
	return match(vm)
}

package dbsched

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	nodes, err := s.recoveryNodes(ctx, region, major)
	if err != nil {
		return unverified, err
	}
	fallback := unverified
	for _, node := range nodes {
		check, err := s.probeRecoveryArchive(ctx, node, source, target, major, latest, 0)
		if err != nil || check.Reason == "recovery_archive_unconfigured" {
			continue
		}
		if check.Outcome == dbarchive.RestoreCovered {
			return check, nil
		}
		if fallback == unverified || fallback.Outcome == dbarchive.RestoreUnverified && check.Outcome != dbarchive.RestoreUnverified {
			fallback = check
		}
	}
	return fallback, ctx.Err()
}

func (s *Scheduler) recoveryNodes(ctx context.Context, region string, major int) ([]Node, error) {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	eligible := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		if node.RecoveryRegion == region && node.Lifecycle == NodeActive && node.offers(major) {
			eligible = append(eligible, node)
		}
	}
	return eligible, nil
}

func (s *Scheduler) probeRecoveryArchive(ctx context.Context, node Node, source string, target time.Time, major int, latest bool, timeline uint32) (dbarchive.RestoreCheck, error) {
	checker, ok := s.vmm.(interface {
		CheckRecoveryArchive(context.Context, Node, string, time.Time, int, bool, uint32) (dbarchive.RestoreCheck, error)
	})
	if !ok {
		return dbarchive.RestoreCheck{}, ErrNodeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return checker.CheckRecoveryArchive(ctx, node, source, target, major, latest, timeline)
}

// A busy or unreachable archive provides no evidence about the reserved
// timeline. Keep the reservation while the restore's context allows retries.
func (s *Scheduler) checkReservedRecovery(ctx context.Context, node Node, vm VM) (dbarchive.RestoreCheck, error) {
	for delay := 250 * time.Millisecond; ; delay = min(delay*2, 5*time.Second) {
		probe, err := s.probeRecoveryArchive(ctx, node, vm.RestoreSource, vm.RestoreTarget, vm.Major, false, vm.RestoreTimeline)
		retry := false
		if err == nil {
			retry = probe.Outcome == dbarchive.RestoreUnverified &&
				(probe.Reason == "archive_busy" || probe.Reason == "archive_unavailable")
		} else {
			switch status.Code(err) {
			case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
				retry = true
			default:
				retry = errors.Is(err, context.DeadlineExceeded)
			}
		}
		if !retry {
			return probe, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return probe, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Scheduler) restoreFromRecovery(ctx context.Context, req RestoreRequest) (VM, error) {
	if req.Target.After(s.now()) {
		return VM{}, ErrInvalid
	}
	match := func(vm VM) (VM, error) {
		if !vm.RestoreFromRecovery || vm.RestoreSource != req.Source ||
			!vm.RestoreTarget.Equal(req.Target) || vm.RecoveryRegion != req.RecoveryRegion ||
			(req.RecoveryTimeline != 0 && vm.RestoreTimeline != req.RecoveryTimeline) {
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
	if !dbarchive.ValidDatabaseID(req.Source) {
		return VM{}, ErrInvalid
	}
	nodes, err := s.recoveryNodes(ctx, req.RecoveryRegion, req.Major)
	if err != nil {
		return VM{}, err
	}
	covered := false
	for _, node := range nodes {
		check, err := s.probeRecoveryArchive(ctx, node, req.Source, req.Target, req.Major, false, req.RecoveryTimeline)
		if err != nil || check.Outcome != dbarchive.RestoreCovered || check.Timeline == 0 ||
			(req.RecoveryTimeline != 0 && check.Timeline != req.RecoveryTimeline) {
			continue
		}
		covered = true
		vm, err := s.store.Place(ctx, VM{
			DatabaseID: req.DatabaseID, NodeID: node.ID, RecoveryRegion: req.RecoveryRegion, RestoreFromRecovery: true,
			Major: req.Major, VcpuCount: req.VcpuCount, MemMiB: req.MemMiB, VolumeBytes: req.VolumeBytes,
			AlwaysOn: req.AlwaysOn, RestoreWindowSeconds: req.RestoreWindowSeconds, Replicated: req.Replicated,
			RestoreSource: req.Source, RestoreTarget: req.Target, RestoreTimeline: check.Timeline,
		})
		if errors.Is(err, ErrNoCapacity) {
			continue
		}
		if err != nil {
			return vm, err
		}
		return match(vm)
	}
	if err := ctx.Err(); err != nil {
		return VM{}, err
	}
	if covered {
		return VM{}, ErrNoCapacity
	}
	return VM{}, fmt.Errorf("%w: recovery archive cannot reach the requested point", ErrConflict)
}

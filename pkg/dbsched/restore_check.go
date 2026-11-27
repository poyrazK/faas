package dbsched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbarchive"
)

// CheckRestoreTarget selects the same archive path as a new restore. It
// leaves placement, the guest and the archive untouched.
func (s *Scheduler) CheckRestoreTarget(ctx context.Context, id string, target time.Time) (dbarchive.RestoreCheck, error) {
	unknown := dbarchive.RestoreCheck{Outcome: dbarchive.RestoreUnverified, Reason: "archive_unavailable"}
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return unknown, err
	}
	now := s.now()
	if target.IsZero() || !target.Before(now) || target.Before(vm.CreatedAt) ||
		vm.RestoreWindowSeconds <= 0 || now.Sub(target) > time.Duration(vm.RestoreWindowSeconds)*time.Second {
		return unknown, fmt.Errorf("%w: target is outside the source's restore window", ErrInvalid)
	}
	if !vm.Bootstrapped || vm.State == StateUpgrading {
		unknown.Reason = "source_not_ready"
		return unknown, nil
	}
	checker, ok := s.vmm.(interface {
		CheckArchiveRestore(context.Context, Node, string, time.Time, int) (dbarchive.RestoreCheck, error)
	})
	if !ok {
		return unknown, nil
	}
	pinned, err := s.restoreNode(ctx, vm)
	if err != nil {
		return unknown, nil //nolint:nilerr // A placement problem leaves archive coverage unverified.
	}
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return unknown, err
	}
	for _, node := range nodes {
		if node.RecoveryRegion != vm.RecoveryRegion || node.Lifecycle != NodeActive || (pinned != "" && pinned != node.ID) {
			continue
		}
		probe, cancel := context.WithTimeout(ctx, 15*time.Second)
		result, err := checker.CheckArchiveRestore(probe, node, vm.DatabaseID, target, vm.MajorAt(target))
		cancel()
		if err == nil {
			return result, nil
		}
		s.log.Warn("dbsched: archive restore check unavailable", "database_id", id, "err", err)
		break
	}
	return unknown, nil
}

func (g *GRPCVMM) CheckLatestArchiveRestore(ctx context.Context, node Node, id string, target time.Time, major int) (dbarchive.RestoreCheck, error) {
	return g.checkArchive(ctx, node, id, target, major, false, true, 0)
}

func (g *GRPCVMM) CheckArchiveRestore(ctx context.Context, node Node, id string, target time.Time, major int) (dbarchive.RestoreCheck, error) {
	return g.checkArchive(ctx, node, id, target, major, false, false, 0)
}

func (g *GRPCVMM) CheckRecoveryArchive(ctx context.Context, node Node, id string, target time.Time, major int, latest bool, timeline uint32) (dbarchive.RestoreCheck, error) {
	return g.checkArchive(ctx, node, id, target, major, true, latest, timeline)
}

func (g *GRPCVMM) checkArchive(ctx context.Context, node Node, id string, target time.Time, major int, recovery, latest bool, timeline uint32) (dbarchive.RestoreCheck, error) {
	c, err := g.client(ctx, node)
	if err != nil {
		return dbarchive.RestoreCheck{}, err
	}
	resp, err := c.DatabaseArchiveRestoreCheck(ctx, &vmmdpb.DatabaseArchiveRestoreCheckRequest{
		DatabaseId: id, PointInTime: timestamppb.New(target), PostgresMajor: int32(major),
		FromRecoveryArchive: recovery, Latest: latest, RecoveryTimeline: timeline,
	})
	if err != nil {
		return dbarchive.RestoreCheck{}, err
	}
	if resp.GetFromRecoveryArchive() != recovery {
		return dbarchive.RestoreCheck{}, errors.New("dbsched: archive check answered for the wrong storage")
	}
	check := dbarchive.RestoreCheck{Outcome: resp.GetOutcome(), Reason: resp.GetReason(), MissingWAL: resp.GetMissingWalSegment(),
		Timeline: resp.GetRecoveryTimeline(), MissingHistory: resp.GetMissingTimelineHistory()}
	if resp.GetBackupCompletedAt() != nil {
		at := resp.GetBackupCompletedAt().AsTime()
		check.BackupCompleted = &at
	}
	if resp.GetWalStopAt() != nil {
		at := resp.GetWalStopAt().AsTime()
		check.WALStopTime = &at
	}
	if resp.GetRecoveryPoint() != nil {
		at := resp.GetRecoveryPoint().AsTime()
		check.RecoveryPoint = &at
	}
	return check, nil
}

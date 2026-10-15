package dbsched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

// Snapshots (ADR-1027 environment clones). A snapshot is a hold on the
// source's archive at an instant (dbarchive holds), taken once the source
// has archived WAL past it: nothing is copied. Restoring it is a
// point-in-time restore to that instant (restore.go), which the hold keeps
// possible after the source's restore window has passed it, until the
// snapshot is deleted. Deleting the source deletes its archive and so its
// snapshots.

// Snapshot is one held instant of a database.
type Snapshot struct {
	DatabaseID, Name       string
	PointInTime, CreatedAt time.Time
}

// ArchiveHold is a hold as a node's dbbackupd reports it.
type ArchiveHold struct {
	PointInTime, CreatedAt time.Time
}

// minArchiveRetention is the shortest retention dbbackupd keeps any
// archive for (its min_retention floor).
const minArchiveRetention = time.Hour

// snapshotMargin keeps new snapshots this far from the end of the
// source's retention, so a prune pass running while the hold is placed
// cannot remove what it pins.
const snapshotMargin = 10 * time.Minute

// CaptureSnapshot holds database id's archive at point under name and
// makes the archive reach point. Capturing the same name and point again
// returns the snapshot unchanged; another point under the name is
// ErrConflict.
func (s *Scheduler) CaptureSnapshot(ctx context.Context, id, name string, point time.Time) (Snapshot, error) {
	if err := validSnapshotRef(id, name); err != nil {
		return Snapshot{}, err
	}
	if point.IsZero() {
		return Snapshot{}, fmt.Errorf("%w: a snapshot needs a point in time", ErrInvalid)
	}
	point = point.UTC().Truncate(time.Microsecond)
	source, err := s.store.Get(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	hold, err := s.getHold(ctx, source, name)
	switch {
	case err == nil && !hold.PointInTime.Equal(point):
		return Snapshot{}, fmt.Errorf("%w: snapshot %s holds another point", ErrConflict, name)
	case errors.Is(err, ErrNotFound):
		// A retry of a placed hold skips this: its point may have aged.
		if err := s.snapshottable(source, point); err != nil {
			return Snapshot{}, err
		}
		if hold, err = s.putHold(ctx, source, name, point); err != nil {
			return Snapshot{}, err
		}
	case err != nil:
		return Snapshot{}, err
	}
	// Recovery to the point needs a commit past it in the archive. A
	// failure leaves the hold for the retry, or for DeleteSnapshot.
	lost, err := s.sourceNodeLost(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	if lost {
		return Snapshot{}, fmt.Errorf("%w: the database's node is lost", ErrNodeUnavailable)
	}
	if err := s.flushArchive(ctx, id, point); err != nil {
		return Snapshot{}, fmt.Errorf("flush archive: %w", err)
	}
	s.log.Info("dbsched: snapshot captured", "database_id", id, "snapshot", name, "point_in_time", point)
	return Snapshot{DatabaseID: id, Name: name, PointInTime: hold.PointInTime, CreatedAt: hold.CreatedAt}, nil
}

// GetSnapshot returns a snapshot, or ErrNotFound.
func (s *Scheduler) GetSnapshot(ctx context.Context, id, name string) (Snapshot, error) {
	if err := validSnapshotRef(id, name); err != nil {
		return Snapshot{}, err
	}
	source, err := s.store.Get(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	hold, err := s.getHold(ctx, source, name)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{DatabaseID: id, Name: name, PointInTime: hold.PointInTime, CreatedAt: hold.CreatedAt}, nil
}

// DeleteSnapshot releases a snapshot's hold; a missing snapshot, or one
// whose database is gone, is success.
func (s *Scheduler) DeleteSnapshot(ctx context.Context, id, name string) error {
	if err := validSnapshotRef(id, name); err != nil {
		return err
	}
	source, err := s.store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.onArchive(ctx, source, func(n Node) error { return s.vmm.DeleteHold(ctx, n, id, name) })
}

func validSnapshotRef(id, name string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%w: invalid database id", ErrInvalid)
	}
	if !dbarchive.ValidHoldName(name) {
		return fmt.Errorf("%w: invalid snapshot name", ErrInvalid)
	}
	return nil
}

// snapshottable checks a new snapshot's point against its database.
func (s *Scheduler) snapshottable(source VM, point time.Time) error {
	retention := max(time.Duration(source.RestoreWindowSeconds)*time.Second, minArchiveRetention)
	switch {
	case source.State == StateDeleting || source.State == StateFailed || source.State == StateProvisioning:
		return fmt.Errorf("%w: database %s is %s", ErrConflict, source.DatabaseID, source.State)
	case !source.Bootstrapped || source.LastBackupAt.IsZero():
		return fmt.Errorf("%w: database %s has no base backup", ErrInvalid, source.DatabaseID)
	case point.Before(source.CreatedAt):
		return fmt.Errorf("%w: point in time precedes the database", ErrInvalid)
	case point.After(s.now()):
		return fmt.Errorf("%w: point in time is in the future", ErrInvalid)
	case point.Before(s.now().Add(-retention + snapshotMargin)):
		return fmt.Errorf("%w: point in time is too close to the end of the database's archive retention", ErrInvalid)
	}
	return nil
}

func (s *Scheduler) putHold(ctx context.Context, source VM, name string, point time.Time) (ArchiveHold, error) {
	var hold ArchiveHold
	err := s.onArchive(ctx, source, func(n Node) error {
		var err error
		hold, err = s.vmm.PutHold(ctx, n, source.DatabaseID, name, point)
		return err
	})
	return hold, err
}

func (s *Scheduler) getHold(ctx context.Context, source VM, name string) (ArchiveHold, error) {
	var hold ArchiveHold
	err := s.onArchive(ctx, source, func(n Node) error {
		var err error
		hold, err = s.vmm.GetHold(ctx, n, source.DatabaseID, name)
		return err
	})
	return hold, err
}

// onArchive runs op against a dbbackupd that reaches the database's
// archive: its node's, or with a shared archive any live node's, trying
// the next when one does not answer. ErrNotFound and ErrConflict are
// answers and end the search.
func (s *Scheduler) onArchive(ctx context.Context, vm VM, op func(Node) error) error {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return err
	}
	var candidates []Node
	for _, n := range nodes {
		if n.ID == vm.NodeID && n.Lifecycle != NodeUnavailable {
			candidates = append([]Node{n}, candidates...)
		} else if s.cfg.SharedArchive && n.Lifecycle != NodeUnavailable {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return fmt.Errorf("%w: no node reaches database %s's archive", ErrNodeUnavailable, vm.DatabaseID)
	}
	var errs []error
	for _, n := range candidates {
		err := op(n)
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || ctx.Err() != nil {
			return err
		}
		errs = append(errs, fmt.Errorf("node %s: %w", n.ID, err))
	}
	return errors.Join(errs...)
}

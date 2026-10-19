package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ImagePreparationPhaseSnapshot includes active and recoverable checkpoints.
// UnknownAge counts legacy rows without phase history; OldestPhaseStartedAt
// covers only known timestamps and is zero when every pending age is unknown.
type ImagePreparationPhaseSnapshot struct {
	Pending, UnknownAge  int64
	OldestPhaseStartedAt time.Time
}

type ImagePreparationProgressSnapshot struct {
	Preparing, LayerPublished, ScanComplete ImagePreparationPhaseSnapshot
}

// ImagePreparationProgressObservationStore reads all eligible checkpoints in
// one consistent snapshot, without the recovery batch limit or activation lock.
// Empty owners retain recovery compatibility; never sum overlapping node scopes.
type ImagePreparationProgressObservationStore interface {
	ImagePreparationProgressSnapshot(context.Context, string) (ImagePreparationProgressSnapshot, error)
}

var (
	_ ImagePreparationProgressObservationStore = (*PgStore)(nil)
	_ ImagePreparationProgressObservationStore = (*MemStore)(nil)
)

func (s *ImagePreparationProgressSnapshot) phase(phase ImagePreparationPhase) *ImagePreparationPhaseSnapshot {
	switch phase {
	case ImagePreparing:
		return &s.Preparing
	case ImageLayerPublished:
		return &s.LayerPublished
	case ImageScanComplete:
		return &s.ScanComplete
	default:
		return nil
	}
}

func (s *PgStore) ImagePreparationProgressSnapshot(ctx context.Context, node string) (ImagePreparationProgressSnapshot, error) {
	rows, err := sqlc.New().OperatorImagePreparationProgressSnapshot(ctx, s.pool, strings.TrimSpace(node))
	if err != nil {
		return ImagePreparationProgressSnapshot{}, fmt.Errorf("state: observe image preparation progress: %w", err)
	}
	var out ImagePreparationProgressSnapshot
	for _, row := range rows {
		p := out.phase(ImagePreparationPhase(row.Phase))
		if p == nil || row.InvalidAge != 0 || (row.OldestPhaseStartedAt.Valid && row.OldestPhaseStartedAt.InfinityModifier != pgtype.Finite) {
			return ImagePreparationProgressSnapshot{}, fmt.Errorf("state: invalid image preparation phase timing")
		}
		p.Pending, p.UnknownAge = row.Pending, row.UnknownAge
		if row.OldestPhaseStartedAt.Valid {
			p.OldestPhaseStartedAt = row.OldestPhaseStartedAt.Time
		}
	}
	return out, nil
}

func (m *MemStore) ImagePreparationProgressSnapshot(ctx context.Context, node string) (ImagePreparationProgressSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ImagePreparationProgressSnapshot{}, err
	}
	node = strings.TrimSpace(node)
	var out ImagePreparationProgressSnapshot
	for id, p := range m.imagePreparations {
		d, exists := m.deployments[id]
		if !exists || d.Status.IsTerminal() || d.Status == DeployLive || p.Phase == ImageHandedOff ||
			(node != "" && p.NodeName != "" && p.NodeName != node) {
			continue
		}
		phase := out.phase(p.Phase)
		if phase == nil {
			return ImagePreparationProgressSnapshot{}, fmt.Errorf("state: invalid image preparation phase")
		}
		phase.Pending++
		if p.PhaseStartedAt.IsZero() {
			phase.UnknownAge++
		} else if phase.OldestPhaseStartedAt.IsZero() || p.PhaseStartedAt.Before(phase.OldestPhaseStartedAt) {
			phase.OldestPhaseStartedAt = p.PhaseStartedAt
		}
	}
	return out, nil
}

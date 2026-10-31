package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// VMMDEpoch is a compute node's vmmd ownership epoch (vmmd restart adoption
// ADR §3). Each vmmd start claims the next epoch; Incarnation is the
// claimant and Predecessor the incarnation it replaced, whose guests it may
// adopt. Both are "" before the first claim.
type VMMDEpoch struct {
	Epoch       uint64
	Incarnation string
	Predecessor string
}

// ComputeNodeVMMDEpochStore reads and claims the epoch. vmmd is its only
// writer.
type ComputeNodeVMMDEpochStore interface {
	ComputeNodeVMMDEpoch(ctx context.Context, nodeID string) (VMMDEpoch, error)
	// ClaimComputeNodeVMMDEpoch claims expected+1 for incarnation and returns
	// it. ErrConflict means the row is no longer at expected: another claim
	// won, or an operator re-registered the node.
	ClaimComputeNodeVMMDEpoch(ctx context.Context, nodeID string, expected uint64, incarnation string) (uint64, error)
}

// AdoptionReportInstanceStore lists the rows schedd settles from a vmmd
// adoption report: the node's RUNNING and FROZEN instances. The projection
// fills ID, AppID, DeploymentID, State, NodeID, WakeID, StartedAt and Mode.
type AdoptionReportInstanceStore interface {
	ListAdoptionReportInstancesOnNode(ctx context.Context, nodeID string, limit int) ([]Instance, error)
}

// VMMDRestartNoticeStore is the planned-restart notice (vmmd restart
// adoption ADR §3). vmmd sets it as it stops for a restart and clears it
// once its successor serves; schedd reads the active ones.
type VMMDRestartNoticeStore interface {
	SetComputeNodeVMMDRestartNotice(ctx context.Context, nodeID string, seconds int) error
	ClearComputeNodeVMMDRestartNotice(ctx context.Context, nodeID string) error
	// ListComputeNodeVMMDRestartNotices returns each node with an unexpired
	// notice and when it expires.
	ListComputeNodeVMMDRestartNotices(ctx context.Context) (map[string]time.Time, error)
}

var (
	_ VMMDRestartNoticeStore      = (*PgStore)(nil)
	_ VMMDRestartNoticeStore      = (*MemStore)(nil)
	_ ComputeNodeVMMDEpochStore   = (*PgStore)(nil)
	_ ComputeNodeVMMDEpochStore   = (*MemStore)(nil)
	_ AdoptionReportInstanceStore = (*PgStore)(nil)
	_ AdoptionReportInstanceStore = (*MemStore)(nil)
)

func adoptionReportState(s string) bool {
	return s == string(StateRunning) || s == string(StateFrozen)
}

func validVMMDEpochArgs(nodeID, incarnation string) bool {
	n, errN := uuid.Parse(nodeID)
	i, errI := uuid.Parse(incarnation)
	return errN == nil && errI == nil && n.String() == nodeID && i.String() == incarnation && i != uuid.Nil
}

func (s *PgStore) ComputeNodeVMMDEpoch(ctx context.Context, nodeID string) (VMMDEpoch, error) {
	if _, err := uuid.Parse(nodeID); err != nil {
		return VMMDEpoch{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ComputeNodeVMMDEpoch(ctx, s.pool, mustPgUUID(nodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return VMMDEpoch{}, ErrNotFound
	}
	if err != nil {
		return VMMDEpoch{}, fmt.Errorf("read vmmd epoch: %w", mapErr(err))
	}
	if row.VmmdEpoch < 0 {
		return VMMDEpoch{}, fmt.Errorf("read vmmd epoch: negative epoch %d", row.VmmdEpoch)
	}
	return VMMDEpoch{Epoch: uint64(row.VmmdEpoch), Incarnation: pgUUIDString(row.VmmdEpochIncarnation), Predecessor: pgUUIDString(row.VmmdPredecessorIncarnation)}, nil
}

func (s *PgStore) ClaimComputeNodeVMMDEpoch(ctx context.Context, nodeID string, expected uint64, incarnation string) (uint64, error) {
	if !validVMMDEpochArgs(nodeID, incarnation) || expected >= 1<<62 {
		return 0, ErrInvalidArgument
	}
	next, err := sqlc.New().ClaimComputeNodeVMMDEpoch(ctx, s.pool, sqlc.ClaimComputeNodeVMMDEpochParams{
		NodeID: mustPgUUID(nodeID), Incarnation: mustPgUUID(incarnation), ExpectedEpoch: int64(expected),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, readErr := s.ComputeNodeVMMDEpoch(ctx, nodeID); errors.Is(readErr, ErrNotFound) {
			return 0, ErrNotFound
		}
		return 0, ErrConflict
	}
	if err != nil {
		return 0, fmt.Errorf("claim vmmd epoch: %w", mapErr(err))
	}
	return uint64(next), nil
}

func (m *MemStore) ComputeNodeVMMDEpoch(ctx context.Context, nodeID string) (VMMDEpoch, error) {
	if err := ctx.Err(); err != nil {
		return VMMDEpoch{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[nodeID]; !ok {
		return VMMDEpoch{}, ErrNotFound
	}
	return m.computeNodeVMMDEpochs[nodeID], nil
}

func (m *MemStore) ClaimComputeNodeVMMDEpoch(ctx context.Context, nodeID string, expected uint64, incarnation string) (uint64, error) {
	if !validVMMDEpochArgs(nodeID, incarnation) || expected >= 1<<62 {
		return 0, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[nodeID]; !ok {
		return 0, ErrNotFound
	}
	cur := m.computeNodeVMMDEpochs[nodeID]
	if cur.Epoch != expected {
		return 0, ErrConflict
	}
	if m.computeNodeVMMDEpochs == nil {
		m.computeNodeVMMDEpochs = map[string]VMMDEpoch{}
	}
	next := VMMDEpoch{Epoch: expected + 1, Incarnation: incarnation, Predecessor: cur.Incarnation}
	m.computeNodeVMMDEpochs[nodeID] = next
	return next.Epoch, nil
}

func (s *PgStore) ListAdoptionReportInstancesOnNode(ctx context.Context, nodeID string, limit int) ([]Instance, error) {
	if _, err := uuid.Parse(nodeID); err != nil || limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListAdoptionReportInstancesOnNode(ctx, s.pool, sqlc.ListAdoptionReportInstancesOnNodeParams{
		NodeID: mustPgUUID(nodeID), RowLimit: int32(min(limit, 1<<20)),
	})
	if err != nil {
		return nil, fmt.Errorf("list adoption report instances: %w", mapErr(err))
	}
	out := make([]Instance, 0, len(rows))
	for _, r := range rows {
		out = append(out, Instance{
			ID: pgUUIDString(r.ID), AppID: r.AppID, DeploymentID: r.DeploymentID, State: r.State,
			NodeID: nodeID, WakeID: pgUUIDString(r.WakeID), StartedAt: timeFromPgtype(r.StartedAt), Mode: r.Mode,
		})
	}
	return out, nil
}

func (m *MemStore) ListAdoptionReportInstancesOnNode(ctx context.Context, nodeID string, limit int) ([]Instance, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		if ins.NodeID == nodeID && adoptionReportState(ins.State) {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func validNoticeArgs(nodeID string, seconds int) bool {
	_, err := uuid.Parse(nodeID)
	return err == nil && seconds > 0 && seconds <= 3600
}

func (s *PgStore) SetComputeNodeVMMDRestartNotice(ctx context.Context, nodeID string, seconds int) error {
	if !validNoticeArgs(nodeID, seconds) {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().SetComputeNodeVMMDRestartNotice(ctx, s.pool, sqlc.SetComputeNodeVMMDRestartNoticeParams{NodeID: mustPgUUID(nodeID), Seconds: int32(seconds)})
	if err != nil {
		return fmt.Errorf("set vmmd restart notice: %w", mapErr(err))
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ClearComputeNodeVMMDRestartNotice(ctx context.Context, nodeID string) error {
	if _, err := uuid.Parse(nodeID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := sqlc.New().ClearComputeNodeVMMDRestartNotice(ctx, s.pool, mustPgUUID(nodeID)); err != nil {
		return fmt.Errorf("clear vmmd restart notice: %w", mapErr(err))
	}
	return nil
}

// ListComputeNodeVMMDRestartNotices is shared with EdgeStore: the edge
// gateways hold forwards through a planned restart (pkg/gateway).
func (s *pgCore) ListComputeNodeVMMDRestartNotices(ctx context.Context) (map[string]time.Time, error) {
	rows, err := sqlc.New().ListComputeNodeVMMDRestartNotices(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("list vmmd restart notices: %w", mapErr(err))
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		out[pgUUIDString(r.ID)] = timeFromPgtype(r.VmmdRestartNoticeUntil)
	}
	return out, nil
}

func (m *MemStore) SetComputeNodeVMMDRestartNotice(ctx context.Context, nodeID string, seconds int) error {
	if !validNoticeArgs(nodeID, seconds) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[nodeID]; !ok {
		return ErrNotFound
	}
	if m.computeNodeVMMDNotices == nil {
		m.computeNodeVMMDNotices = map[string]time.Time{}
	}
	m.computeNodeVMMDNotices[nodeID] = time.Now().Add(time.Duration(seconds) * time.Second)
	return nil
}

func (m *MemStore) ClearComputeNodeVMMDRestartNotice(ctx context.Context, nodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.computeNodeVMMDNotices, nodeID)
	return nil
}

func (m *MemStore) ListComputeNodeVMMDRestartNotices(ctx context.Context) (map[string]time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := map[string]time.Time{}
	for id, until := range m.computeNodeVMMDNotices {
		if until.After(now) {
			out[id] = until
		}
	}
	return out, nil
}

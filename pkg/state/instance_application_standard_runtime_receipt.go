package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) GetInstanceApplicationStandardRuntimeReceipt(ctx context.Context, id string) (runtimeadmission.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return runtimeadmission.Receipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return runtimeadmission.Receipt{}, ErrNotFound
	}
	capture, ok := m.instanceApplicationStandardAdmissions[id]
	if !ok || ins.State != string(StateRunning) && ins.State != string(StateSnapshotting) && ins.State != string(StateMigrating) || m.guardNativeRuntimeReceiptLocked(ins, capture) != nil {
		return runtimeadmission.Receipt{}, ErrApplicationStandardRuntimeStale
	}
	boot, ok := m.instanceApplicationStandardBoots[m.instanceApplicationStandardBootTokens[id]]
	if !ok || boot.Receipt == nil {
		return runtimeadmission.Receipt{}, ErrApplicationStandardRuntimeStale
	}
	r := boot.Receipt.Clone()
	if token := m.instanceApplicationStandardPromotionTokens[id]; token != "" {
		p := m.instanceApplicationStandardPromotions[token]
		if p.Receipt == nil {
			return runtimeadmission.Receipt{}, ErrApplicationStandardRuntimeStale
		}
		r = p.Receipt.Clone()
	}
	if r.Check(r.Binding, time.Unix(0, r.CompletedAtUnixNano)) != nil {
		return runtimeadmission.Receipt{}, ErrApplicationStandardRuntimeStale
	}
	return r, nil
}

func (s *PgStore) GetInstanceApplicationStandardRuntimeReceipt(ctx context.Context, id string) (runtimeadmission.Receipt, error) {
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil || u.String() != id {
		return runtimeadmission.Receipt{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetCurrentApplicationStandardRuntimeReceipt(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return runtimeadmission.Receipt{}, mapErr(err)
	}
	var r runtimeadmission.Receipt
	if !row.Valid || json.Unmarshal(row.Receipt, &r) != nil || r.Check(r.Binding, time.Unix(0, r.CompletedAtUnixNano)) != nil {
		return runtimeadmission.Receipt{}, ErrApplicationStandardRuntimeStale
	}
	return r, nil
}

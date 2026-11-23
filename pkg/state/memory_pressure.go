package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Event kinds the operator memory pressure report reads (ADR-20261123190507).
const (
	// MemoryPressureKindEviction is schedd's RAM pressure eviction of a
	// running instance; its reason names the trigger.
	MemoryPressureKindEviction = "memory_eviction"
	// MemoryPressureKindFrozenPark is a frozen instance parked; the report
	// reads only those with reason memory_pressure.
	MemoryPressureKindFrozenPark = "frozen_park"
	// MemoryPressureKindOOMKill is a workload OOM kill (ADR-121).
	MemoryPressureKindOOMKill = "instances.workload_oom_failed"
	// MemoryPressureParkReason is the frozen_park reason schedd records when
	// it parks a frozen instance under memory pressure.
	MemoryPressureParkReason = "memory_pressure"
)

// MemoryPressureReader is an optional read capability for the operator
// memory pressure report; Store stays unchanged.
type MemoryPressureReader interface {
	// OperatorMemoryPressureEvents returns memory pressure evictions, frozen
	// parks under memory pressure and OOM kills at or after since, newest
	// first, at most limit.
	OperatorMemoryPressureEvents(ctx context.Context, since time.Time, limit int) ([]MemoryPressureEvent, error)
}

// MemoryPressureEvent is one eviction, frozen park or OOM kill with its
// instance's node and RAM and its app's account and plan. Fields of an
// instance or app row that is gone are empty. PeakMB, PlanMB and
// DeploymentFailed are set on OOM kills only.
type MemoryPressureEvent struct {
	At                              time.Time
	Kind, Reason, InstanceID        string
	AppID, AppSlug, AccountID, Plan string
	NodeID, NodeName                string
	RAMMB, PeakMB, PlanMB           int
	DeploymentFailed                bool
}

var (
	_ MemoryPressureReader = (*PgStore)(nil)
	_ MemoryPressureReader = (*MemStore)(nil)
)

func (s *PgStore) OperatorMemoryPressureEvents(ctx context.Context, since time.Time, limit int) ([]MemoryPressureEvent, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().OperatorMemoryPressureEvents(ctx, s.pool, sqlc.OperatorMemoryPressureEventsParams{
		Since: pgtype.Timestamptz{Time: since, Valid: true}, RowLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("state: operator memory pressure events: %w", err)
	}
	out := make([]MemoryPressureEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, MemoryPressureEvent{At: r.At.Time, Kind: r.Kind, Reason: r.Reason, InstanceID: r.InstanceID,
			AppID: r.AppID, AppSlug: r.AppSlug, AccountID: r.AccountID, Plan: r.Plan, NodeID: r.NodeID, NodeName: r.NodeName,
			RAMMB: int(r.RamMb), PeakMB: int(r.PeakMb), PlanMB: int(r.PlanMb), DeploymentFailed: r.DeploymentFailed})
	}
	return out, nil
}

// memoryPressureData is the part of an event's data the report reads.
type memoryPressureData struct {
	Reason           string `json:"reason"`
	InstanceID       string `json:"instance_id"`
	AppID            string `json:"app_id"`
	PeakMB           int    `json:"peak_mb"`
	PlanMB           int    `json:"plan_mb"`
	DeploymentFailed bool   `json:"deployment_failed"`
}

func (m *MemStore) OperatorMemoryPressureEvents(_ context.Context, since time.Time, limit int) ([]MemoryPressureEvent, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Events name a subject by its uuid form; memory ids need not be uuids.
	bySubject := map[uuid.UUID]string{}
	for id := range m.instances {
		if u := parseSubjectID(id); u != nil {
			bySubject[*u] = id
		}
	}
	type ranked struct {
		ev MemoryPressureEvent
		id int64
	}
	var out []ranked
	for _, e := range m.events {
		if e.At.Before(since) || (e.Kind != MemoryPressureKindEviction && e.Kind != MemoryPressureKindFrozenPark && e.Kind != MemoryPressureKindOOMKill) {
			continue
		}
		var d memoryPressureData
		_ = json.Unmarshal(e.Data, &d)
		if e.Kind == MemoryPressureKindFrozenPark && d.Reason != MemoryPressureParkReason {
			continue
		}
		ev := MemoryPressureEvent{At: e.At, Kind: e.Kind, Reason: d.Reason, InstanceID: d.InstanceID, AppID: d.AppID,
			PeakMB: d.PeakMB, PlanMB: d.PlanMB, DeploymentFailed: d.DeploymentFailed}
		if e.Subject != nil {
			ev.InstanceID = e.Subject.String()
			if id, ok := bySubject[*e.Subject]; ok {
				ev.InstanceID = id
			}
		}
		m.joinMemoryPressureLocked(&ev)
		out = append(out, ranked{ev, e.ID})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ev.At.Equal(out[j].ev.At) {
			return out[i].ev.At.After(out[j].ev.At)
		}
		return out[i].id > out[j].id
	})
	if len(out) > limit {
		out = out[:limit]
	}
	evs := make([]MemoryPressureEvent, len(out))
	for i, r := range out {
		evs[i] = r.ev
	}
	return evs, nil
}

// joinMemoryPressureLocked fills an event's instance, node, app and account
// fields from the rows that still exist. Callers hold m.mu.
func (m *MemStore) joinMemoryPressureLocked(ev *MemoryPressureEvent) {
	if ins, ok := m.instances[ev.InstanceID]; ok {
		ev.RAMMB, ev.NodeID = ins.RAMMB, ins.NodeID
		if ins.AppID != "" {
			ev.AppID = ins.AppID
		}
		if n, ok := m.computeNodes[ins.NodeID]; ok {
			ev.NodeName = n.Name
		}
	}
	a, ok := m.apps[ev.AppID]
	if !ok {
		ev.AppID = ""
		return
	}
	ev.AppSlug, ev.AccountID = a.Slug, a.AccountID
	if acct, ok := m.accounts[a.AccountID]; ok {
		ev.Plan = string(acct.Plan)
	}
}

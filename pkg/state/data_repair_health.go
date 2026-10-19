package state

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type DataRepairHealthStore interface {
	DataRepairHealth(context.Context, time.Time, string, int) (api.DataRepairHealth, error)
}

func ValidateDataRepairHealthCursor(key string) bool {
	if key == "" {
		return true
	}
	if !strings.HasPrefix(key, "t:") && !strings.HasPrefix(key, "o:") {
		return false
	}
	id, err := uuid.Parse(key[2:])
	return err == nil && id != uuid.Nil && id.String() == key[2:]
}

func (s *PgStore) DataRepairHealth(ctx context.Context, now time.Time, after string, limit int) (api.DataRepairHealth, error) {
	if !ValidateDataRepairHealthCursor(after) || limit < 0 || limit > api.ObsAdminPaginationMax || now.IsZero() {
		return api.DataRepairHealth{}, ErrInvalidArgument
	}
	pageSize := limit
	if limit > 0 {
		pageSize++
	}
	raw, err := sqlc.New().DataRepairHealthSnapshot(ctx, s.pool, sqlc.DataRepairHealthSnapshotParams{ObservedAt: pgtypeFromTime(now), HeartbeatCutoff: pgtypeFromTime(now.Add(-DefaultHeartbeatStaleness)), AfterKey: after, PageSize: int32(pageSize)})
	if err != nil {
		return api.DataRepairHealth{}, err
	}
	var h api.DataRepairHealth
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, err
	}
	return finishDataRepairHealth(h, now, limit), nil
}

// MemStore has no scheduled VM executor. Its admitted apply operations still
// participate in the same health contract; PostgreSQL owns scheduled tasks.
func (m *MemStore) DataRepairHealth(ctx context.Context, now time.Time, after string, limit int) (api.DataRepairHealth, error) {
	if err := ctx.Err(); err != nil {
		return api.DataRepairHealth{}, err
	}
	if !ValidateDataRepairHealthCursor(after) || limit < 0 || limit > api.ObsAdminPaginationMax || now.IsZero() {
		return api.DataRepairHealth{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	h := api.DataRepairHealth{Items: []api.DataRepairHealthItem{}}
	byPhase := map[string]*api.DataRepairPhaseHealth{}
	for _, op := range m.operationMemoryLocked().operations {
		if op.DataRepair == nil || ((op.State != api.OperationRunning && op.State != api.OperationRequiresReconciliation) || (op.DataRepair.Phase != api.DataRepairApplying && op.DataRepair.Phase != api.DataRepairUnknown && op.DataRepair.Phase != api.DataRepairResolving)) && op.DataRepair.Phase != api.DataRepairReplacing {
			continue
		}
		at := op.DataRepair.PhaseStartedAt
		if at.IsZero() {
			at = op.CreatedAt
		}
		p := byPhase[op.DataRepair.Phase]
		if p == nil {
			p = &api.DataRepairPhaseHealth{Phase: op.DataRepair.Phase}
			byPhase[p.Phase] = p
		}
		p.Count++
		if p.OldestPhaseStartedAt == nil || at.Before(*p.OldestPhaseStartedAt) {
			v := at
			p.OldestPhaseStartedAt = &v
		}
		key := "o:" + op.ID
		if key > after && limit > 0 {
			h.Items = append(h.Items, api.DataRepairHealthItem{CursorKey: key, OperationID: op.ID, AccountID: op.AccountID, AppID: op.AppID, Phase: p.Phase, PhaseStartedAt: at, NodeStatus: "unassigned", FailureCode: op.FailureCode})
		}
	}
	for _, p := range byPhase {
		h.Phases = append(h.Phases, *p)
	}
	sort.Slice(h.Items, func(i, j int) bool { return h.Items[i].CursorKey < h.Items[j].CursorKey })
	return finishDataRepairHealth(h, now, limit), nil
}

func finishDataRepairHealth(h api.DataRepairHealth, now time.Time, limit int) api.DataRepairHealth {
	h.ObservedAt, h.Limit = now.UTC(), limit
	h.HeldMemoryMB = h.HeldSlots * int64(api.DataRepairSandboxMemoryMB+api.PerVMOverheadMB)
	h.HeldVCPUs = h.HeldSlots * int64(api.DataRepairSandboxVCPUs)
	h.HeldCPUMillicores = h.HeldSlots * int64(api.DataRepairSandboxCPUMillicores)
	phases := h.Phases
	h.Phases = []api.DataRepairPhaseHealth{}
	for _, phase := range api.DataRepairHealthPhases() {
		p := api.DataRepairPhaseHealth{Phase: phase, WarnAfterSeconds: api.DataRepairHealthWarnSeconds(phase)}
		if i := slices.IndexFunc(phases, func(v api.DataRepairPhaseHealth) bool { return v.Phase == phase }); i >= 0 {
			p.Count = phases[i].Count
			p.OldestPhaseStartedAt = phases[i].OldestPhaseStartedAt
		}
		if p.OldestPhaseStartedAt != nil {
			p.OldestAgeSeconds = repairHealthAge(now, *p.OldestPhaseStartedAt)
		}
		h.Phases = append(h.Phases, p)
	}
	if len(h.Items) > limit {
		h.Items = h.Items[:limit]
		if limit > 0 {
			h.NextCursor = h.Items[limit-1].CursorKey
		}
	}
	if h.Items == nil {
		h.Items = []api.DataRepairHealthItem{}
	}
	for i := range h.Items {
		v := &h.Items[i]
		v.AgeSeconds = repairHealthAge(now, v.PhaseStartedAt)
		if v.CapacityHeld {
			v.HeldMemoryMB = api.DataRepairSandboxMemoryMB + api.PerVMOverheadMB
			v.HeldVCPUs = api.DataRepairSandboxVCPUs
		}
		v.NeedsAttention = v.Phase == api.DataRepairUnknown || v.AgeSeconds > api.DataRepairHealthWarnSeconds(v.Phase) || v.CapacityHeld && v.NodeStatus != "available"
		v.RunbookURL = "https://github.com/poyrazK/faas/blob/main/docs/runbooks/FaasDataRepairStuck.md"
		v.RecoveryGuidance = repairRecoveryGuidance(v.Phase)
	}
	return h
}

func repairHealthAge(now, at time.Time) int64 {
	if at.After(now) {
		return 0
	}
	return int64(now.Sub(at) / time.Second)
}

func repairRecoveryGuidance(phase string) string {
	switch phase {
	case RepairRehearsalQueued:
		return "Check the original executor binding, dispatch gate, runner images and capacity. Use the existing rehearsal cancellation action if no longer needed."
	case RepairRehearsalRunning:
		return "Inspect the original node and task. Cancellation retains capacity until cleanup is confirmed. Do not dispatch a replacement for this task."
	case RepairRehearsalCleanup:
		return "Restore contact with the original node and inspect its vmmd task/native journals. Let schedd retire the original attempt; preserve its journals and capacity until proof is confirmed."
	case RepairRehearsalPublishing:
		return "Check the original executor binding and saved plan evidence. Let evidence reconciliation finish; the VM has been retired and must not be rerun."
	case api.DataRepairResolving:
		return "Resume apply resolution with the original plan digest and operation ID. A busy or unreachable source remains unresolved; only a committed receipt or durable source closure releases this reservation."
	case api.DataRepairReplacing:
		return "Inspect the original operation's saved replacement UUID and exact executor binding. Resume replacement with that same UUID and parent digest; do not apply the parent or choose another replacement identity."
	default:
		return "Inspect the original executor plan and repair operation receipt. Reconcile the existing outcome using the same repair operation ID; a missing response does not establish rollback."
	}
}

var _ DataRepairHealthStore = (*PgStore)(nil)
var _ DataRepairHealthStore = (*MemStore)(nil)

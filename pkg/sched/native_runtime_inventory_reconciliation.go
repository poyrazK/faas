package sched

// adr: 777
import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type RegionalRuntimeInventoryIssue struct {
	Permit runtimequota.CapacityPermit
	Reason string
}

// Matches identify original holds observed in this request only. They are never
// persisted as admission authority, deduplicated against rows or used to release.
type RegionalRuntimeInventoryReport struct {
	Matches []runtimequota.LaunchSource
	Issues  []RegionalRuntimeInventoryIssue
	// CurrentAttempts also have the original run/claim in the customer-intent
	// store at observation time. This is advisory evidence, never a dispatch
	// lease, a serving decision or permission to merge/release charges.
	CurrentAttempts  []runtimequota.LaunchSource
	CurrentIssues    []RegionalRuntimeInventoryIssue
	Nodes            int
	NativeUnresolved int
}

type nativeRuntimeInventoryReader interface {
	NativeRuntimeInventory(context.Context, string) (fcvm.NativeRuntimeInventorySample, error)
}

// ObserveRegionalRuntimeInventoryPage visits retained holds across all signers.
// Errors advance the cursor; an unavailable first node cannot starve later pages.
// Physical matches use original source/resource evidence; current-attempt reads
// are separate advisory observations. This changes no accounting or head.
func (e *Engine) ObserveRegionalRuntimeInventoryPage(ctx context.Context, after state.RegionalRuntimeRetirementCursor) (state.RegionalRuntimeRetirementCursor, RegionalRuntimeInventoryReport, error) {
	var report RegionalRuntimeInventoryReport
	rows, err := e.store.ListRegionalRuntimeInventory(ctx, "", after, api.RegionalRuntimeRetirementBatch)
	if err != nil || len(rows) > api.RegionalRuntimeRetirementBatch {
		return after, report, errors.Join(err, runtimequota.ErrBinding)
	}
	if len(rows) == 0 {
		return state.RegionalRuntimeRetirementCursor{}, report, ctx.Err()
	}
	for _, row := range rows {
		p := row.Receipt.Permit
		next := state.RegionalRuntimeRetirementCursor{AppID: p.Grant.Binding.RegionalAppID, PermitID: p.ID}
		if p.Validate() != nil || next.AppID < after.AppID || (next.AppID == after.AppID && next.PermitID <= after.PermitID) {
			return after, report, runtimequota.ErrBinding
		}
		after = next
	}
	next, observed, err := e.observeRegionalRuntimeRows(ctx, after, rows)
	currentErr := e.observeCurrentRegionalAttempts(ctx, &observed, time.Now())
	return next, observed, errors.Join(err, currentErr)
}

func (e *Engine) observeRegionalRuntimeRows(ctx context.Context, after state.RegionalRuntimeRetirementCursor, rows []state.RegionalRuntimeInventoryEntry) (state.RegionalRuntimeRetirementCursor, RegionalRuntimeInventoryReport, error) {
	return e.observeRegionalRuntimeRowsWithMatch(ctx, after, rows, nil)
}

func (e *Engine) observeRegionalRuntimeRowsWithMatch(ctx context.Context, after state.RegionalRuntimeRetirementCursor, rows []state.RegionalRuntimeInventoryEntry, matched func(state.RegionalRuntimeInventoryEntry, fcvm.NativeRuntimeInventorySample) error) (state.RegionalRuntimeRetirementCursor, RegionalRuntimeInventoryReport, error) {
	var report RegionalRuntimeInventoryReport
	reader, available := e.vmm.(nativeRuntimeInventoryReader)
	samples := map[string]fcvm.NativeRuntimeInventorySample{}
	failures := map[string]error{}
	var result error
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return after, report, errors.Join(result, err)
		}
		node := row.Receipt.Permit.NodeID
		sample, seen := samples[node]
		if !seen {
			var err error
			if available {
				sample, err = reader.NativeRuntimeInventory(ctx, node)
			} else {
				err = runtimequota.ErrTrust
			}
			if err == nil && (sample.Validate() != nil || sample.Host.NodeID != node) {
				err = runtimequota.ErrTrust
			}
			samples[node], failures[node] = sample, err
			result = errors.Join(result, err)
			report.Nodes++
			if err == nil {
				report.NativeUnresolved += len(sample.Inventory.Unresolved)
				for _, owner := range sample.Inventory.Owners {
					if owner.Unresolved != "" || owner.Placement == nil {
						report.NativeUnresolved++
					}
				}
			}
		}
		reason := "native observation unavailable"
		if failures[node] == nil {
			reason = regionalRuntimeObservationIssue(row, sample)
		}
		if reason != "" {
			report.Issues = append(report.Issues, RegionalRuntimeInventoryIssue{Permit: row.Receipt.Permit, Reason: reason})
		} else {
			report.Matches = append(report.Matches, *row.Source)
			if matched != nil {
				result = errors.Join(result, matched(row, sample))
			}
		}
	}
	return after, report, result
}

func regionalRuntimeObservationIssue(row state.RegionalRuntimeInventoryEntry, sample fcvm.NativeRuntimeInventorySample) string {
	p := row.Receipt.Permit
	if row.Receipt.IssuedAt == nil || row.Receipt.ReleasedAt != nil || row.Receipt.FenceID != "" || row.Receipt.Stop != nil || row.Receipt.StopEnvelope != nil {
		return "original hold is unissued, fenced or awaiting retirement reconciliation"
	}
	if row.Source == nil || row.Source.Validate() != nil || row.Source.Permit != p || !row.Resources.Valid() {
		return "original source or resource provenance is unavailable"
	}
	if p.Kind == runtimequota.CapacityAppTask && row.Source.ValidateTask() != nil {
		return "original app task lease relationship is unavailable"
	}
	if p.NodeID != sample.Host.NodeID || p.HostEpochID != sample.Host.HostEpochID {
		return "original host incarnation differs from observation"
	}
	var owner *fcvm.NativeRuntimeObservation
	for i := range sample.Inventory.Owners {
		o := &sample.Inventory.Owners[i]
		if o.Instance == row.Source.InstanceKey || (o.Placement != nil && o.Placement.Permit == p) {
			if owner != nil {
				return "duplicate native ownership is unresolved"
			}
			owner = o
		}
	}
	if owner == nil {
		return "original owner was not observed; absence requires native retirement evidence"
	}
	if owner.OriginalSource == nil || *owner.OriginalSource != *row.Source {
		return "original native launch source differs from the issued hold"
	}
	for _, helper := range sample.Inventory.HelperTasks {
		if helper.Instance == owner.Instance || helper.OwnerGeneration == owner.Generation {
			return "original helper producer remains unresolved"
		}
	}
	for i := range sample.Inventory.Owners {
		other := &sample.Inventory.Owners[i]
		if other != owner && other.PID == owner.PID {
			return "multiple native owners claim the original task"
		}
	}
	if owner.Instance != row.Source.InstanceKey || owner.Lease.Instance != owner.Instance || owner.Placement == nil || owner.Placement.Validate() != nil || owner.Placement.Permit != p ||
		owner.OriginalResources == nil || *owner.OriginalResources != row.Resources || owner.KernelBootID != sample.Inventory.KernelBootID || owner.Generation == "" ||
		!owner.Authorized || owner.Revoked || owner.ExitConfirmed || owner.ResourcesRemoved || owner.PID <= 0 || owner.StartTime == 0 || owner.Lease.Plan != p.Grant.Policy.Plan || owner.Lease.IsBuilder ||
		(owner.VMState != "Running" && owner.VMState != "Paused") || owner.Unresolved != "" || owner.RegionalRuntimeCompatibility() != "" {
		return "native ownership, machine or resource observation differs from original hold"
	}
	groups := 0
	for _, group := range sample.Inventory.Cgroups {
		if slices.Contains(group.PIDs, owner.PID) {
			if !reflect.DeepEqual(group, *owner.Cgroup) {
				return "independent cgroup inventory differs from owner observation"
			}
			groups++
		}
	}
	tasks := 0
	for _, task := range sample.Inventory.ManagedUIDTasks {
		if task.PID == owner.PID {
			if task.UID != owner.Lease.UID || task.StartTime != owner.StartTime {
				return "independent task incarnation differs from owner observation"
			}
			tasks++
		}
	}
	if groups != 1 || tasks != 1 {
		return "independent cgroup or task ownership is missing or duplicated"
	}
	return ""
}

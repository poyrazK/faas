package state

import (
	"context"
	"sort"
	"time"
)

type BindingRuntimeInventory struct {
	ChangedAt   *time.Time
	Deployments []BindingRuntimeDeployment
}

type BindingRuntimeDeployment struct {
	ID, Scope, DeploymentStatus string
	Serving, Resident           BindingRuntimeCounts
	Starting                    int
}

type BindingRuntimeCounts struct{ Current, Stale, Unknown int }

// BindingRefreshInventory contains only public correlation tokens, bounded
// status and sanitized reasons, never outbox payloads or raw errors.
type BindingRefreshInventory struct {
	WakeID, Status, FailureReason string
	Attempts                      int
	RequestedAt                   time.Time
	CompletedAt                   *time.Time
}

type BindingRuntimeInventoryStore interface {
	ReadBindingRuntimeInventory(context.Context, string, string, string) (BindingRuntimeInventory, error)
	ListBindingRefreshInventory(context.Context, string, string, []string) ([]BindingRefreshInventory, error)
}

func addBindingRuntimeInstance(row *BindingRuntimeDeployment, stamp *time.Time, instance Instance) {
	add := func(counts *BindingRuntimeCounts) {
		switch {
		case stamp == nil || instance.StartedAt.IsZero():
			counts.Unknown++
		case !instance.StartedAt.After(*stamp):
			counts.Stale++
		default:
			counts.Current++
		}
	}
	add(&row.Resident)
	if State(instance.State) == StateRunning {
		add(&row.Serving)
	}
	if State(instance.State) == StateWaking || State(instance.State) == StateColdBooting {
		row.Starting++
	}
}

func sortBindingRuntimeDeployments(rows []BindingRuntimeDeployment) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Scope != rows[j].Scope {
			return rows[i].Scope < rows[j].Scope
		}
		return rows[i].ID < rows[j].ID
	})
}

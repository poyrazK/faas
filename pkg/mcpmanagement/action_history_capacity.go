package mcpmanagement

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Preserve all live app/environment plans before retaining archived history.
func (m *management) pruneActionHistories(now time.Time) {
	type position struct {
		id                string
		at                time.Time
		live, environment bool
	}
	rows := make([]position, 0, len(m.actions)+len(m.environmentActions))
	for id, entry := range m.actions {
		rows = append(rows, position{id: id, at: entry.RecordedAt, live: m.actionPlanAvailable(entry, now)})
	}
	for id, entry := range m.environmentActions {
		rows = append(rows, position{id: id, at: entry.RecordedAt, live: m.environmentActionPlanAvailable(entry, now), environment: true})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].live != rows[j].live {
			return rows[i].live
		}
		return actionNewer(actionRecord{RecordedAt: rows[i].at, PlanID: rows[i].id}, actionRecord{RecordedAt: rows[j].at, PlanID: rows[j].id})
	})
	for _, row := range rows[min(len(rows), api.ManagementMCPActionHistoryMax):] {
		if row.environment {
			delete(m.environmentActions, row.id)
		} else {
			delete(m.actions, row.id)
		}
	}
}

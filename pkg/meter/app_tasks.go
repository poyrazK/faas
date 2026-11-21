package meter

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// appTasksInMinute groups by app the app tasks whose VMs ran in the minute.
// A store without app tasks bills none.
func (s *Sampler) appTasksInMinute(ctx context.Context, minute time.Time) (map[string][]state.AppTaskBillingWindow, error) {
	store, ok := s.store.(state.AppTaskBillingStore)
	if !ok {
		return nil, nil
	}
	windows, err := store.ListAppTasksInBillingWindow(ctx, minute, minute.Add(time.Minute))
	if err != nil {
		return nil, fmt.Errorf("meter: load app task billing: %w", err)
	}
	byApp := make(map[string][]state.AppTaskBillingWindow)
	for _, w := range windows {
		byApp[w.AppID] = append(byApp[w.AppID], w)
	}
	return byApp, nil
}

// rollAppTasks bills the app's task VMs for the minute (cost-control review
// 2026-11-19, N16). A task VM boots the app's deployment at the app's RAM
// and holds it until the command ends, so it bills plan RAM + 8 MB per
// running second like any instance (spec §4.7). It has no instance row, so
// its seconds come from the task's started_at/finished_at window, and its
// usage row is keyed by the task ID: rolling the minute again rewrites the
// same row. Egress from task VMs is not metered yet.
func (s *Sampler) rollAppTasks(ctx context.Context, out []RolledRow, app state.App, tasks []state.AppTaskBillingWindow,
	plans map[string]api.Plan, minute, observedAt time.Time) ([]RolledRow, error) {
	if len(tasks) == 0 {
		return out, nil
	}
	subject := meterAppSubject(app, plans)
	admissionMB := api.BillableRAMMBWithSidecars(app.RAMMB, nil)
	if admissionMB <= 0 {
		return out, nil
	}
	for _, task := range tasks {
		seconds := task.BillableSeconds(minute, minute.Add(time.Minute), observedAt)
		if seconds <= 0 {
			continue
		}
		row := RolledRow{
			InstanceID:  task.TaskID,
			AppID:       app.ID,
			AccountID:   subject,
			Minute:      minute,
			AdmissionMB: admissionMB,
			MBSeconds:   int64(admissionMB) * seconds,
			Mode:        "app_task",
			Plan:        string(plans[subject]),
		}
		if err := s.appendRolledRow(ctx, &row, 0, UsageDeltas{}); err != nil {
			return out, fmt.Errorf("meter: bill app task %s: %w", task.TaskID, err)
		}
		out = append(out, row)
	}
	return out, nil
}

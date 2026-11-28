package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AppTaskRetentionStore deletes finished app tasks past their plan's
// retention (api.Limits.AppTaskRetentionDays). schedd's app-task coordinator
// is the only caller.
type AppTaskRetentionStore interface {
	PruneAppTasks(ctx context.Context, params PruneAppTasksParams) (int, error)
}

// PruneAppTasksParams selects one plan's prunable tasks: manual and cron
// tasks of accounts on Plan that finished before FinishedBefore. Release
// tasks and binding verifications are deploy evidence and are never pruned,
// nor is a task a promotion migration or a live cost preview still names.
// At most Limit tasks, oldest first, are deleted per call.
type PruneAppTasksParams struct {
	Plan           api.Plan
	FinishedBefore time.Time
	Limit          int
}

func (p PruneAppTasksParams) validate() error {
	if p.Plan == "" || p.FinishedBefore.IsZero() || p.Limit <= 0 {
		return fmt.Errorf("%w: prune needs a plan, a cutoff and a positive limit", ErrAppTaskInvalid)
	}
	return nil
}

// prunableAppTask reports whether a task's own fields allow pruning.
func prunableAppTask(task AppTask, before time.Time) bool {
	if task.Kind != AppTaskKindManual && task.Kind != AppTaskKindCron {
		return false
	}
	if task.BindingVerification != nil || !task.Status.Terminal() || task.FinishedAt == nil {
		return false
	}
	return task.FinishedAt.Before(before)
}

// PruneAppTasks deletes the plan's prunable tasks with their attach and
// network records, mirroring the Postgres cascades.
func (m *MemStore) PruneAppTasks(_ context.Context, params PruneAppTasksParams) (int, error) {
	if err := params.validate(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var doomed []AppTask
	for _, task := range m.appTasks {
		account, ok := m.accounts[task.AccountID]
		if ok && account.Plan == params.Plan && prunableAppTask(task, params.FinishedBefore) {
			doomed = append(doomed, task)
		}
	}
	sort.Slice(doomed, func(i, j int) bool { return doomed[i].FinishedAt.Before(*doomed[j].FinishedAt) })
	if len(doomed) > params.Limit {
		doomed = doomed[:params.Limit]
	}
	for _, task := range doomed {
		delete(m.appTasks, task.ID)
		delete(m.appTaskNetworks, task.ID)
		delete(m.appTaskCapacityWaits, task.ID)
		delete(m.appTaskAttach, task.ID)
	}
	return len(doomed), nil
}

// PruneAppTasks deletes the plan's prunable tasks; their attach, network,
// inspection and scenario rows cascade and cron/schedule references are
// cleared by their ON DELETE SET NULL keys. SKIP LOCKED leaves a task another
// transaction holds for the next pass.
func (s *PgStore) PruneAppTasks(ctx context.Context, params PruneAppTasksParams) (int, error) {
	if err := params.validate(); err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, `
		with doomed as (
			select t.id
			  from app_tasks t
			  join accounts a on a.id = t.account_id
			 where a.plan = $1
			   and t.kind in ('manual', 'cron')
			   and t.binding_verification is null
			   and t.status in ('succeeded', 'failed', 'timed_out', 'cancelled')
			   and t.finished_at < $2
			   and not exists (select 1 from project_environment_promotion_migrations m where m.task_id = t.id)
			   and not exists (select 1 from financial_preview_command_runtimes f where f.task_id = t.id and f.phase <> 'retired')
			 order by t.finished_at
			 limit $3
			   for update of t skip locked
		)
		delete from app_tasks t using doomed where t.id = doomed.id`,
		string(params.Plan), params.FinishedBefore.UTC(), params.Limit)
	if err != nil {
		return 0, fmt.Errorf("prune app tasks: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

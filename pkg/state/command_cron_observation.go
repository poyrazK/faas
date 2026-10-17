package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// CommandCronObservation excludes command, output, identity and business-key
// payloads. Schedule text/timezone stay internal to the observer, never labels.
// Times are cursor evidence, not a continuous eligibility clock.
type CommandCronObservation struct {
	Schedule, Timezone                                               string
	CreatedAt, LastFiredAt                                           time.Time
	Enabled, Suspended, AppDeleted, AccountActive                    bool
	LiveDeployment, ActiveTasks, ActiveOrdinaryTasks, ExclusiveBound bool
	TimingValid, PolicyValid, BindingValid, SkipIfRunning            bool
	Policy                                                           *workpolicy.SchedulePolicy
}

type CommandCronObservationStore interface {
	CommandCronObservations(context.Context, string, string) ([]CommandCronObservation, error)
}

var (
	_ CommandCronObservationStore = (*PgStore)(nil)
	_ CommandCronObservationStore = (*MemStore)(nil)
)

func (s *PgStore) CommandCronObservations(ctx context.Context, owner, local string) ([]CommandCronObservation, error) {
	rows, err := sqlc.New().OperatorCommandCronObservations(ctx, s.pool, sqlc.OperatorCommandCronObservationsParams{OwnerNode: owner, LocalNode: local})
	if err != nil {
		return nil, fmt.Errorf("state: observe command cron admission: %w", err)
	}
	out := make([]CommandCronObservation, 0, len(rows))
	for _, r := range rows {
		p := CommandCronObservation{Schedule: r.Schedule, Timezone: r.Timezone, Enabled: r.Enabled, Suspended: r.Suspended, AppDeleted: r.AppDeleted,
			AccountActive: r.AccountActive, LiveDeployment: r.LiveDeployment, ActiveTasks: r.ActiveTasks, ActiveOrdinaryTasks: r.ActiveOrdinaryTasks,
			ExclusiveBound: r.ExclusiveBound, BindingValid: r.BindingValid, SkipIfRunning: r.SkipIfRunning, PolicyValid: true,
			TimingValid: r.CreatedAt.Valid && r.CreatedAt.InfinityModifier == pgtype.Finite && (!r.LastFiredAt.Valid || r.LastFiredAt.InfinityModifier == pgtype.Finite)}
		if r.CreatedAt.Valid && r.CreatedAt.InfinityModifier == pgtype.Finite {
			p.CreatedAt = r.CreatedAt.Time.UTC()
		}
		if r.LastFiredAt.Valid && r.LastFiredAt.InfinityModifier == pgtype.Finite {
			p.LastFiredAt = r.LastFiredAt.Time.UTC()
		}
		if len(r.SchedulePolicy) > 0 {
			p.Policy = &workpolicy.SchedulePolicy{}
			p.PolicyValid = json.Unmarshal(r.SchedulePolicy, p.Policy) == nil && p.Policy.Validate() == nil
		}
		out = append(out, p)
	}
	return out, nil
}

func (m *MemStore) CommandCronObservations(ctx context.Context, owner, local string) ([]CommandCronObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	live := make(map[string]bool)
	active, ordinary := make(map[string]bool), make(map[string]bool)
	for _, d := range m.deployments {
		if d.Status == DeployLive && d.RootfsKey != "" && d.ImageDigest != "" {
			live[d.AppID] = true
		}
	}
	for _, task := range m.appTasks {
		if task.Status == AppTaskQueued || task.Status == AppTaskRestoring || task.Status == AppTaskRunning {
			active[task.CronID] = true
			ordinary[task.CronID] = ordinary[task.CronID] || task.ExclusiveOperationID == ""
		}
	}
	var out []CommandCronObservation
	for _, c := range m.crons {
		if len(c.Command) == 0 {
			continue
		}
		a, ok := m.apps[c.AppID]
		if !ok {
			continue
		}
		// The nonempty-owner list is an exact placement match; the legacy list
		// is global and then filtered by Engine.ownsAppDecision's local fallback.
		if owner != "" && a.NodeID != owner || owner == "" && local != "" && a.NodeID != "" && a.NodeID != local {
			continue
		}
		account := m.accounts[a.AccountID]
		p := CommandCronObservation{Schedule: c.Schedule, Timezone: c.Timezone, CreatedAt: c.CreatedAt, LastFiredAt: c.LastFiredAt,
			Enabled: c.Enabled, Suspended: c.SuspendedReason != "", AppDeleted: a.Status == AppDeleted, AccountActive: account.Active(),
			Policy: workpolicy.Clone(c.SchedulePolicy), PolicyValid: c.SchedulePolicy == nil || c.SchedulePolicy.Validate() == nil,
			SkipIfRunning: c.SkipIfRunning, TimingValid: true, BindingValid: true}
		if b, exists := m.exclusiveTriggerBindings["cron\x00"+c.ID]; exists && b.AccountID == a.AccountID {
			p.ExclusiveBound = true
			p.BindingValid = b.AppID == c.AppID
		}
		p.LiveDeployment, p.ActiveTasks, p.ActiveOrdinaryTasks = live[a.ID], active[c.ID], ordinary[c.ID]
		out = append(out, p)
	}
	return out, nil
}

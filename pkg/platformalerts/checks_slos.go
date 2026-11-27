package platformalerts

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/slo"
	"github.com/onebox-faas/faas/pkg/state"
)

// Default alerts for synthetic checks and SLOs (ADR-20261115194000,
// amendment 2026-11-16). A check or SLO with no enabled alert rule naming it
// emails the account like an outage does; a rule naming it means its owner
// chose how to be told.

// CheckStore is the synthetic check read the failing-check detector needs;
// PgStore and MemStore satisfy it. Optional: without it no check notices.
type CheckStore interface {
	ListRunnableSyntheticChecks(ctx context.Context) ([]state.RunnableSyntheticCheck, error)
	ListSyntheticCheckRuns(ctx context.Context, checkID string, limit int) ([]state.SyntheticCheckRun, error)
}

// SLOLister lists every SLO; PgStore and MemStore satisfy it. Optional:
// without it, or without a Prometheus that answers scalar queries, no SLO
// notices.
type SLOLister interface {
	ListAllSLOs(ctx context.Context) ([]state.SLO, error)
}

type checkFailingDetail struct {
	Name       string    `json:"name"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	FailedRuns int       `json:"failed_runs"`
	Since      time.Time `json:"since"`
	LastStatus int       `json:"last_status"`
	LastError  string    `json:"last_error"`
}

type sloBurnDetail struct {
	Name      string  `json:"name"`
	BurnRate  float64 `json:"burn_rate"`
	Objective float64 `json:"objective_pct"`
}

// ruleCover answers "does an enabled rule name this subject" with one rule
// read per account per detection pass.
type ruleCover struct {
	w     *Worker
	rules map[string][]state.AlertRule
}

func (c *ruleCover) covered(ctx context.Context, accountID string, match func(state.AlertRule) bool) (bool, error) {
	if c.rules == nil {
		c.rules = map[string][]state.AlertRule{}
	}
	rules, ok := c.rules[accountID]
	if !ok {
		var err error
		if rules, err = c.w.Store.ListAlertRulesForAccount(ctx, accountID); err != nil {
			return false, err
		}
		c.rules[accountID] = rules
	}
	for _, r := range rules {
		if r.Enabled && r.EnvironmentID == "" && match(r) {
			return true, nil
		}
	}
	return false, nil
}

// DetectFailingChecks queues a check_failing notice for every enabled check
// whose last SyntheticCheckFailingRuns runs all failed and that no enabled
// alert rule names.
func (w *Worker) DetectFailingChecks(ctx context.Context) error {
	checks, ok := w.Store.(CheckStore)
	if !ok {
		return nil
	}
	runnable, err := checks.ListRunnableSyntheticChecks(ctx)
	if err != nil {
		return fmt.Errorf("list checks: %w", err)
	}
	cover := &ruleCover{w: w}
	for _, c := range runnable {
		if w.CheckReleased != nil && !w.CheckReleased(ctx, c) {
			continue
		}
		runs, err := checks.ListSyntheticCheckRuns(ctx, c.ID, api.SyntheticCheckFailingRuns)
		if err != nil {
			w.log().Warn("platform alerts: read check runs", "check_id", c.ID, "err", err)
			continue
		}
		if !allFailed(runs, api.SyntheticCheckFailingRuns) || staleRuns(runs, c.IntervalSeconds, w.now()) {
			continue
		}
		covered, err := cover.covered(ctx, c.AccountID, func(r state.AlertRule) bool { return r.SyntheticCheckID == c.ID })
		if err != nil {
			w.log().Warn("platform alerts: read alert rules", "account_id", c.AccountID, "err", err)
			continue
		}
		if covered {
			continue
		}
		newest, oldest := runs[0], runs[len(runs)-1]
		if oldest.StartedAt.After(newest.StartedAt) {
			newest, oldest = oldest, newest
		}
		detail, _ := json.Marshal(checkFailingDetail{
			Name: c.Name, Method: c.Method, Path: c.Path, FailedRuns: len(runs), Since: oldest.StartedAt,
			LastStatus: newest.StatusCode, LastError: newest.ErrorClass,
		})
		w.queue(ctx, state.PlatformAlertNotice{AccountID: c.AccountID, AppID: c.AppID, Kind: state.PlatformAlertCheckFailing, SubjectID: c.ID, Detail: detail})
	}
	return nil
}

// staleRuns reports runs whose newest is older than the check's staleness
// bound, the same bound the synthetic alert rule applies (a check that stopped
// running is unknown, not failing).
func staleRuns(runs []state.SyntheticCheckRun, intervalSeconds int, now time.Time) bool {
	if len(runs) == 0 || intervalSeconds <= 0 {
		return false
	}
	newest := runs[0].StartedAt
	for _, r := range runs[1:] {
		if r.StartedAt.After(newest) {
			newest = r.StartedAt
		}
	}
	return now.Sub(newest) > time.Duration(api.SyntheticCheckStaleIntervals*intervalSeconds)*time.Second
}

func allFailed(runs []state.SyntheticCheckRun, want int) bool {
	if len(runs) < want {
		return false
	}
	for _, r := range runs {
		if r.OK {
			return false
		}
	}
	return true
}

// DetectBurningSLOs queues an slo_burn notice for every SLO whose
// multi-window burn rate is at least PlatformAlertSLOBurnRate and that no
// enabled alert rule names.
func (w *Worker) DetectBurningSLOs(ctx context.Context) error {
	lister, ok := w.Store.(SLOLister)
	if !ok {
		return nil
	}
	prom, ok := w.Metrics.(slo.PromQL)
	if !ok || prom == nil {
		return nil
	}
	defs, err := lister.ListAllSLOs(ctx)
	if err != nil {
		return fmt.Errorf("list SLOs: %w", err)
	}
	cover := &ruleCover{w: w}
	for _, def := range defs {
		if w.SLOReleased != nil && !w.SLOReleased(ctx, def) {
			continue
		}
		burn, err := slo.MultiWindowBurn(ctx, prom, def)
		if err != nil || math.IsNaN(burn) || math.IsInf(burn, 0) || burn < api.PlatformAlertSLOBurnRate {
			continue
		}
		covered, err := cover.covered(ctx, def.AccountID, func(r state.AlertRule) bool { return r.SLOID == def.ID })
		if err != nil {
			w.log().Warn("platform alerts: read alert rules", "account_id", def.AccountID, "err", err)
			continue
		}
		if covered {
			continue
		}
		detail, _ := json.Marshal(sloBurnDetail{Name: def.Name, BurnRate: math.Round(burn*10) / 10, Objective: float64(def.ObjectiveBP) / 100})
		w.queue(ctx, state.PlatformAlertNotice{AccountID: def.AccountID, AppID: def.AppID, Kind: state.PlatformAlertSLOBurn, SubjectID: def.ID, Detail: detail})
	}
	return nil
}

// budgetLeft renders how long a 30-day budget lasts at this burn rate.
func budgetLeft(burn float64) string {
	if burn <= 0 {
		return "never"
	}
	d := time.Duration(float64(30*24*time.Hour) / burn)
	if d >= 48*time.Hour {
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	}
	return fmt.Sprintf("%.0f hours", math.Max(1, d.Hours()))
}

func (w *Worker) queue(ctx context.Context, n state.PlatformAlertNotice) {
	queued, err := w.Store.QueuePlatformAlertNotice(ctx, n, api.PlatformAlertCooldown)
	if err != nil {
		w.log().Warn("platform alerts: queue notice", "kind", n.Kind, "subject_id", n.SubjectID, "err", err)
		return
	}
	if queued {
		w.log().Info("platform alerts: notice queued", "kind", n.Kind, "app_id", n.AppID, "subject_id", n.SubjectID)
	}
}

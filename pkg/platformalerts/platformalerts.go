// Package platformalerts emails an account about conditions the platform
// detects without any alert rule the customer wrote (ADR-20261115194000):
// an app answering (almost) only 5xx, and a cron schedd paused because
// every recent run failed. meterd runs it. schedd queues cron notices in the
// transaction that pauses the cron; the outage detector here queues app
// notices; one sender drains both.
package platformalerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is what the worker reads and writes.
type Store interface {
	state.PlatformAlertNoticeStore
	AppByID(ctx context.Context, id string) (state.App, error)
	AccountByID(ctx context.Context, id string) (state.Account, error)
	ListOrgMembers(ctx context.Context, orgID string) ([]state.OrgMembership, error)
	ListAlertRulesForAccount(ctx context.Context, accountID string) ([]state.AlertRule, error)
}

// Metrics is the Prometheus read the outage detector needs; pkg/promql's
// Client satisfies it. Each map is keyed by the app label.
type Metrics interface {
	QueryMap(ctx context.Context, query string) (map[string]float64, error)
}

// Mailer sends one plain-text email; alertchannels.Mailer has the shape.
type Mailer interface {
	SendEmail(ctx context.Context, to, subject, body, idempotencyKey string) error
}

const (
	// Interval is how often Run checks for outages and sends notices.
	Interval    = time.Minute
	detectEvery = 5 * time.Minute
	trimEvery   = time.Hour
	batchSize   = 50
	lease       = 2 * time.Minute
	retryDelay  = 5 * time.Minute
	maxAttempts = 5
)

// The fleet-wide queries the detector runs: one round trip each, whatever
// the number of apps. The error rate matches appmetrics.ErrorRatePctQuery
// (5xx and capacity refusals over 2xx, 5xx and refusals; client 3xx/4xx are
// not evidence of an outage). An app refusing all its traffic for lack of
// capacity answered 429s the detector never saw (Observe audit 2026-11-21,
// finding 8).
var (
	outageErrorRateQuery = fmt.Sprintf(
		// "> 0" drops an idle app instead of answering 0/0 = NaN for it
		// (prod rc.347: one idle app failed every detection pass).
		`sum by (app) (rate(gateway_request_duration_seconds_count{class="5xx"}[%[1]s]) or rate(gateway_capacity_refusals_total[%[1]s])) / (sum by (app) (rate(gateway_request_duration_seconds_count{class=~"2xx|5xx"}[%[1]s]) or rate(gateway_capacity_refusals_total[%[1]s])) > 0) * 100`,
		api.AppOutageWindow)
	outageRequestsQuery = fmt.Sprintf(
		`sum by (app) (increase(gateway_request_duration_seconds_count{class=~"2xx|5xx"}[%[1]s]) or increase(gateway_capacity_refusals_total[%[1]s]))`, api.AppOutageWindow)
)

// Worker detects outages and delivers platform alert notices.
type Worker struct {
	Store   Store
	Metrics Metrics // nil: no outage detection; cron notices still send
	Mail    Mailer
	// DashboardURL, when set, adds a link to the app in each email.
	DashboardURL string
	Log          *slog.Logger
	Now          func() time.Time
	// Outcome, when set, counts each notice's delivery outcome.
	Outcome func(kind, outcome string)
	// CheckReleased and SLOReleased decide whether the account's plan and
	// capability flags still cover a synthetic check or an SLO, the same
	// gates meterd's runner and SLO rollup apply (ADR-20261028101615). A
	// check the runner no longer probes keeps its last failed runs for days;
	// without the gate it was reported as failing every day until they aged
	// out. nil covers every check and SLO.
	CheckReleased func(ctx context.Context, check state.RunnableSyntheticCheck) bool
	SLOReleased   func(ctx context.Context, def state.SLO) bool

	lastDetect, lastTrim time.Time
}

// Run works every Interval until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		w.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick runs the due parts of one iteration.
func (w *Worker) Tick(ctx context.Context) {
	now := w.now()
	if now.Sub(w.lastDetect) >= detectEvery {
		w.lastDetect = now
		if w.Metrics != nil {
			if err := w.DetectOutages(ctx); err != nil && ctx.Err() == nil {
				w.log().Warn("platform alerts: outage detection failed", "err", err)
			}
			if err := w.DetectBurningSLOs(ctx); err != nil && ctx.Err() == nil {
				w.log().Warn("platform alerts: SLO burn detection failed", "err", err)
			}
		}
		if err := w.DetectFailingChecks(ctx); err != nil && ctx.Err() == nil {
			w.log().Warn("platform alerts: failing check detection failed", "err", err)
		}
	}
	if err := w.SendDue(ctx); err != nil && ctx.Err() == nil {
		w.log().Warn("platform alerts: send failed", "err", err)
	}
	if now.Sub(w.lastTrim) >= trimEvery {
		w.lastTrim = now
		if _, err := w.Store.TrimPlatformAlertNotices(ctx, now.Add(-api.PlatformAlertRetention)); err != nil && ctx.Err() == nil {
			w.log().Warn("platform alerts: trim failed", "err", err)
		}
	}
}

// outageDetail is an app_outage notice's detail.
type outageDetail struct {
	ErrorRatePct float64 `json:"error_rate_pct"`
	Requests     int64   `json:"requests"`
	Window       string  `json:"window"`
}

// DetectOutages queues an app_outage notice for every app over the outage
// threshold that has no enabled error-rate alert rule of its own.
func (w *Worker) DetectOutages(ctx context.Context) error {
	rates, err := w.Metrics.QueryMap(ctx, outageErrorRateQuery)
	if err != nil {
		return fmt.Errorf("query error rates: %w", err)
	}
	var candidates []string
	for appID, pct := range rates {
		if !math.IsNaN(pct) && pct >= api.AppOutageErrorRatePct {
			candidates = append(candidates, appID)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	requests, err := w.Metrics.QueryMap(ctx, outageRequestsQuery)
	if err != nil {
		return fmt.Errorf("query request counts: %w", err)
	}
	for _, appID := range candidates {
		n := requests[appID]
		if math.IsNaN(n) || n < api.AppOutageMinRequests {
			continue
		}
		if err := w.queueOutage(ctx, appID, rates[appID], int64(math.Round(n))); err != nil {
			w.log().Warn("platform alerts: queue outage notice", "app_id", appID, "err", err)
		}
	}
	return nil
}

func (w *Worker) queueOutage(ctx context.Context, appID string, pct float64, requests int64) error {
	app, err := w.Store.AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if app.DeletedAt != nil {
		return nil
	}
	covered, err := w.hasOwnErrorRateRule(ctx, app)
	if err != nil || covered {
		return err
	}
	detail, _ := json.Marshal(outageDetail{ErrorRatePct: math.Round(pct*10) / 10, Requests: requests, Window: api.AppOutageWindow})
	queued, err := w.Store.QueuePlatformAlertNotice(ctx, state.PlatformAlertNotice{
		AccountID: app.AccountID, AppID: app.ID, Kind: state.PlatformAlertAppOutage, SubjectID: app.ID, Detail: detail,
	}, api.PlatformAlertCooldown)
	if err == nil && queued {
		w.log().Info("platform alerts: app outage notice queued", "app_id", app.ID, "error_rate_pct", pct, "requests", requests)
	}
	return err
}

// hasOwnErrorRateRule reports whether the app already has an enabled
// error-rate alert rule, or an enabled burn alert on an availability SLO
// (the protection baseline's, ADR-20261116120000 §7, or the owner's own):
// its owner already gets told, and a full outage burns the budget before
// the outage detector fires.
func (w *Worker) hasOwnErrorRateRule(ctx context.Context, app state.App) (bool, error) {
	rules, err := w.Store.ListAlertRulesForAccount(ctx, app.AccountID)
	if err != nil {
		return false, err
	}
	for _, r := range rules {
		// A stage's rule watches the stage's traffic, not production's
		// (ADR-20261104120000).
		if !r.Enabled || r.AppID != app.ID || r.EnvironmentID != "" {
			continue
		}
		if string(r.Metric) == "error_rate_pct" {
			return true, nil
		}
		if r.Metric == state.AlertMetricSLOBudgetBurn && r.SLOID != "" {
			available, err := w.availabilitySLO(ctx, app.ID, r.SLOID)
			if err != nil {
				return false, err
			}
			if available {
				return true, nil
			}
		}
	}
	return false, nil
}

// availabilitySLO reports whether the app's SLO measures availability. A
// store without SLO definitions cannot say, so a burn rule counts.
func (w *Worker) availabilitySLO(ctx context.Context, appID, sloID string) (bool, error) {
	slos, ok := w.Store.(state.SLOStore)
	if !ok {
		return true, nil
	}
	def, err := slos.GetSLO(ctx, appID, sloID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return def.SLI == state.SLIAvailability, nil
}

// SendDue delivers the notices due now.
func (w *Worker) SendDue(ctx context.Context) error {
	due, err := w.Store.ClaimPlatformAlertNotices(ctx, batchSize, lease)
	if err != nil {
		return fmt.Errorf("claim notices: %w", err)
	}
	for _, n := range due {
		w.count(string(n.Kind), w.deliver(ctx, n))
	}
	return nil
}

// Delivery outcomes reported to Outcome.
const (
	OutcomeSent    = "sent"
	OutcomeSkipped = "skipped"
	OutcomeRetry   = "retry"
	OutcomeFailed  = "failed"
)

func (w *Worker) deliver(ctx context.Context, n state.PlatformAlertNotice) string {
	app, err := w.Store.AppByID(ctx, n.AppID)
	if errors.Is(err, state.ErrNotFound) || err == nil && app.DeletedAt != nil {
		return w.finish(ctx, n, state.PlatformAlertSkipped, "app is gone")
	}
	if err != nil {
		return w.retry(ctx, n, err)
	}
	recipients, err := w.recipients(ctx, app)
	if err != nil {
		return w.retry(ctx, n, err)
	}
	if len(recipients) == 0 {
		return w.finish(ctx, n, state.PlatformAlertSkipped, "no recipient with a verified email")
	}
	subject, body, ok := message(n, app, w.DashboardURL)
	if !ok {
		return w.finish(ctx, n, state.PlatformAlertSkipped, "unknown notice kind")
	}
	for _, to := range recipients {
		if err := w.Mail.SendEmail(ctx, to.Email, subject, body, "platform-alert-"+n.ID+"-"+to.ID); err != nil {
			return w.retry(ctx, n, err)
		}
	}
	return w.finish(ctx, n, state.PlatformAlertSent, "")
}

// recipients are the app's personal owner, or its organization's owners and
// admins, with a verified email.
func (w *Worker) recipients(ctx context.Context, app state.App) ([]state.Account, error) {
	ids := []string{app.AccountID}
	if app.OrgID != "" {
		members, err := w.Store.ListOrgMembers(ctx, app.OrgID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		if len(members) > 0 {
			ids = ids[:0]
			for _, m := range members {
				if m.RemovedAt == nil && (m.Role == state.OrgRoleOwner || m.Role == state.OrgRoleAdmin) {
					ids = append(ids, m.AccountID)
				}
			}
		}
	}
	seen := map[string]bool{}
	var out []state.Account
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		acct, err := w.Store.AccountByID(ctx, id)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if acct.Email != "" && acct.EmailVerified() {
			out = append(out, acct)
		}
	}
	return out, nil
}

func (w *Worker) finish(ctx context.Context, n state.PlatformAlertNotice, status, reason string) string {
	if err := w.Store.FinishPlatformAlertNotice(ctx, n.ID, status, reason); err != nil {
		w.log().Warn("platform alerts: finish notice", "notice_id", n.ID, "err", err)
	}
	if status == state.PlatformAlertSent {
		return OutcomeSent
	}
	return OutcomeSkipped
}

func (w *Worker) retry(ctx context.Context, n state.PlatformAlertNotice, cause error) string {
	if n.Attempts >= maxAttempts {
		if err := w.Store.FinishPlatformAlertNotice(ctx, n.ID, state.PlatformAlertFailed, cause.Error()); err != nil {
			w.log().Warn("platform alerts: fail notice", "notice_id", n.ID, "err", err)
		}
		w.log().Warn("platform alerts: notice failed", "notice_id", n.ID, "kind", n.Kind, "err", cause)
		return OutcomeFailed
	}
	if err := w.Store.RetryPlatformAlertNotice(ctx, n.ID, w.now().Add(retryDelay), cause.Error()); err != nil {
		w.log().Warn("platform alerts: retry notice", "notice_id", n.ID, "err", err)
	}
	return OutcomeRetry
}

func (w *Worker) count(kind, outcome string) {
	if w.Outcome != nil {
		w.Outcome(kind, outcome)
	}
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// cronFailingDetail mirrors the detail schedd writes with the pause.
type cronFailingDetail struct {
	Path       string    `json:"path"`
	FailedRuns int       `json:"failed_runs"`
	Since      time.Time `json:"since"`
	LastError  string    `json:"last_error"`
}

// message renders a notice. Nothing from the detail is trusted beyond being
// shown: it is platform-written and bounded.
func message(n state.PlatformAlertNotice, app state.App, dashboardURL string) (subject, body string, ok bool) {
	var b strings.Builder
	link := ""
	if dashboardURL != "" {
		link = strings.TrimRight(dashboardURL, "/") + "/dashboard/apps/" + app.Slug
	}
	switch n.Kind {
	case state.PlatformAlertAppOutage:
		var d outageDetail
		_ = json.Unmarshal(n.Detail, &d)
		subject = fmt.Sprintf("Gregale: %s is failing (%.0f%% of requests return errors)", app.Slug, d.ErrorRatePct)
		fmt.Fprintf(&b, "Your app %s answered %.1f%% of its last %d requests with a server error (5xx) or a capacity refusal (429) over the past %s.\n\n",
			app.Slug, d.ErrorRatePct, d.Requests, d.Window)
		b.WriteString("Find out why:\n")
		fmt.Fprintf(&b, "  gregale debug status %s\n  gregale logs %s --since 30m\n\n", app.Slug, app.Slug)
		fmt.Fprintf(&b, "Gregale sends this at most once a day per app, and not for an app with its own error-rate alert:\n  gregale alert add --app %s --metric error_rate_pct ...\n", app.Slug)
	case state.PlatformAlertCronFailingPaused:
		var d cronFailingDetail
		_ = json.Unmarshal(n.Detail, &d)
		subject = fmt.Sprintf("Gregale: paused a failing cron on %s", app.Slug)
		fmt.Fprintf(&b, "The cron %s on %s (POST %s) failed its last %d runs, since %s, so Gregale paused it. Paused crons do not run or bill.\n\n",
			n.SubjectID, app.Slug, d.Path, d.FailedRuns, d.Since.UTC().Format("2006-01-02 15:04 MST"))
		if d.LastError != "" {
			fmt.Fprintf(&b, "Last error: %s\n\n", d.LastError)
		}
		fmt.Fprintf(&b, "See the runs:\n  gregale cron runs %s\n\nFix the app, then resume the cron (deploying the app also resumes it):\n  gregale crons update %s --enable\n", n.SubjectID, n.SubjectID)
	case state.PlatformAlertCheckFailing:
		var d checkFailingDetail
		_ = json.Unmarshal(n.Detail, &d)
		subject = fmt.Sprintf("Gregale: check %s on %s is failing", d.Name, app.Slug)
		fmt.Fprintf(&b, "The synthetic check %s (%s %s) on %s failed its last %d runs, since %s.\n",
			d.Name, d.Method, d.Path, app.Slug, d.FailedRuns, d.Since.UTC().Format("2006-01-02 15:04 MST"))
		if d.LastStatus != 0 || d.LastError != "" {
			fmt.Fprintf(&b, "Last run: status %d %s\n", d.LastStatus, d.LastError)
		}
		fmt.Fprintf(&b, "\nFind out why:\n  gregale debug status %s\n  gregale logs %s --since 30m\n\n", app.Slug, app.Slug)
		fmt.Fprintf(&b, "Gregale sends this at most once a day per check, and not for a check with its own alert rule:\n  gregale alert add --app %s --metric synthetic_check_consecutive_failures --synthetic-check %s ...\n", app.Slug, n.SubjectID)
	case state.PlatformAlertSLOBurn:
		var d sloBurnDetail
		_ = json.Unmarshal(n.Detail, &d)
		subject = fmt.Sprintf("Gregale: SLO %s on %s is burning its error budget %.0fx too fast", d.Name, app.Slug, d.BurnRate)
		fmt.Fprintf(&b, "The SLO %s (objective %.2f%%) on %s is spending its error budget %.1f times faster than it can sustain. At this rate a 30-day budget is gone in about %s.\n\n",
			d.Name, d.Objective, app.Slug, d.BurnRate, budgetLeft(d.BurnRate))
		fmt.Fprintf(&b, "Find out why:\n  gregale debug status %s\n  gregale logs %s --since 1h --level error\n\n", app.Slug, app.Slug)
		fmt.Fprintf(&b, "Gregale sends this at most once a day per SLO, and not for an SLO with its own alert rule:\n  gregale alert add --app %s --metric slo_budget_burn --slo %s ...\n", app.Slug, n.SubjectID)
	default:
		return "", "", false
	}
	if link != "" {
		fmt.Fprintf(&b, "\n%s\n", link)
	}
	return subject, b.String(), true
}

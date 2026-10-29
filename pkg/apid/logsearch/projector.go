package logsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ProjectorStore is the Store subset the build and deploy projector needs.
type ProjectorStore interface {
	state.LogProjectionStore
	InsertLogEvent(ctx context.Context, event state.LogEvent) (state.LogEvent, error)
}

// Projector copies deployment_audit rows and finished builds into the log
// ledger as deploy and build events (ADR-20261018083536 slice C). apid is the
// ledger's only writer, and these sources already live in its database, so
// no producer changes. Inserts are idempotent on source_event_id, so several
// apid replicas, a restart's lookback or a retried tick never duplicate.
type Projector struct {
	store    ProjectorStore
	now      func() time.Time
	log      *slog.Logger
	outcomes *prometheus.CounterVec

	auditAt time.Time
	auditID int64
	buildAt time.Time
	buildID string
}

// NewProjector starts both cursors LogProjectionStartupLookback back. reg
// may be nil in tests.
func NewProjector(store ProjectorStore, now func() time.Time, reg prometheus.Registerer, log *slog.Logger) *Projector {
	if log == nil {
		log = slog.Default()
	}
	start := now().Add(-api.LogProjectionStartupLookback)
	p := &Projector{
		store: store, now: now, log: log, auditAt: start, buildAt: start,
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "apid_log_projection_events_total",
			Help: "Build and deploy events projected into the log_events ledger, by source and outcome (ADR-20261018083536).",
		}, []string{"source", "outcome"}),
	}
	if reg != nil {
		reg.MustRegister(p.outcomes)
	}
	return p
}

// Run ticks until ctx ends. A failed tick logs and keeps its cursor, so the
// next tick retries the same rows.
func (p *Projector) Run(ctx context.Context) {
	ticker := time.NewTicker(api.LogProjectionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.Tick(ctx); err != nil && ctx.Err() == nil {
				p.log.Warn("log projection tick failed", "err", err)
			}
		}
	}
}

// Tick projects every source row committed before now minus the commit lag,
// up to LogProjectionTickRowsMax rows per source.
func (p *Projector) Tick(ctx context.Context) error {
	until := p.now().Add(-api.LogProjectionCommitLag)
	auditErr := p.projectAudit(ctx, until)
	buildErr := p.projectBuilds(ctx, until)
	if auditErr != nil {
		return auditErr
	}
	return buildErr
}

func (p *Projector) projectAudit(ctx context.Context, until time.Time) error {
	for read := 0; read < api.LogProjectionTickRowsMax; {
		rows, err := p.store.ListDeploymentAuditForLedger(ctx, p.auditAt, p.auditID, until, api.LogProjectionTickRowsMax-read)
		if err != nil {
			return fmt.Errorf("log projection: read deployment audit: %w", err)
		}
		for _, row := range rows {
			if err := p.insert(ctx, deployEvent(row)); err != nil {
				return err
			}
			p.auditAt, p.auditID = row.At, row.ID
		}
		if read += len(rows); len(rows) == 0 {
			return nil
		}
	}
	return nil
}

func (p *Projector) projectBuilds(ctx context.Context, until time.Time) error {
	for read := 0; read < api.LogProjectionTickRowsMax; {
		rows, err := p.store.ListBuildOutcomesForLedger(ctx, p.buildAt, p.buildID, until, api.LogProjectionTickRowsMax-read)
		if err != nil {
			return fmt.Errorf("log projection: read build outcomes: %w", err)
		}
		for _, row := range rows {
			if err := p.insert(ctx, buildEvent(row)); err != nil {
				return err
			}
			p.buildAt, p.buildID = row.FinishedAt, row.BuildID
		}
		if read += len(rows); len(rows) == 0 {
			return nil
		}
	}
	return nil
}

func (p *Projector) insert(ctx context.Context, event state.LogEvent) error {
	if _, err := p.store.InsertLogEvent(ctx, event); err != nil {
		p.outcomes.WithLabelValues(string(event.Source), "failed").Inc()
		return fmt.Errorf("log projection: insert %s: %w", event.SourceEventID, err)
	}
	p.outcomes.WithLabelValues(string(event.Source), "inserted").Inc()
	return nil
}

// deployLevels marks the audit kinds a searcher filters for; the rest are info.
var deployLevels = map[state.DeploymentAuditKind]string{
	state.DeployRolledBack:        "error",
	state.DeployRolloutAborted:    "error",
	state.DeployHealthProbeFailed: "warn",
	state.DeployScanRegressed:     "warn",
	state.DeployAlertRuleFired:    "warn",
	state.DeployRolloutHeld:       "warn",
	state.DeployMCPTaskHeld:       "warn",
}

func deployEvent(row state.LedgerDeploymentAudit) state.LogEvent {
	level := deployLevels[row.Kind]
	if level == "" {
		level = "info"
	}
	fields, _ := json.Marshal(map[string]string{"kind": string(row.Kind)})
	return state.LogEvent{
		OccurredAt: row.At, AccountID: row.AccountID, AppID: row.AppID, DeploymentID: row.DeploymentID,
		Source: state.LogEventSourceDeploy, SourceEventID: fmt.Sprintf("audit:%d", row.ID),
		Level: level, Stream: "system", Message: deployMessage(row), Fields: fields,
	}
}

// deployMessage names the kind and, for the payloads ADR-122 documents, the
// values a reader needs.
func deployMessage(row state.LedgerDeploymentAudit) string {
	var data struct {
		FromPercent *float64 `json:"from_percent"`
		ToPercent   *float64 `json:"to_percent"`
		Probe       string   `json:"probe"`
		TriggerKind string   `json:"trigger_kind"`
	}
	_ = json.Unmarshal(row.Data, &data)
	name := "deploy " + strings.ReplaceAll(strings.TrimPrefix(string(row.Kind), "deploy."), "_", " ")
	switch {
	case row.Kind == state.DeployTrafficChanged && data.FromPercent != nil && data.ToPercent != nil:
		return fmt.Sprintf("%s from %g%% to %g%%", name, *data.FromPercent, *data.ToPercent)
	case row.Kind == state.DeployHealthProbeFailed && data.Probe != "":
		return fmt.Sprintf("%s: %s", name, data.Probe)
	case row.Kind == state.DeployRolledBack && data.TriggerKind != "":
		return fmt.Sprintf("%s (trigger: %s)", name, data.TriggerKind)
	}
	return name
}

func buildEvent(row state.LedgerBuildOutcome) state.LogEvent {
	took := ""
	if !row.StartedAt.IsZero() && row.FinishedAt.After(row.StartedAt) {
		took = " after " + row.FinishedAt.Sub(row.StartedAt).Round(time.Second).String()
	}
	level, message := "info", "build "+string(row.Status)+took
	switch row.Status {
	case state.BuildFailed:
		level = "error"
		if row.FailureClass != "" {
			message = fmt.Sprintf("build failed (%s)%s", row.FailureClass, took)
		}
		if reason := truncateUTF8(strings.TrimSpace(row.DeploymentError), api.LogProjectionBuildErrorBytes); reason != "" {
			message += ": " + reason
		}
	case state.BuildCancelled:
		level = "warn"
	}
	if row.Kind != "" {
		message += " [" + row.Kind + "]"
	}
	fields, _ := json.Marshal(map[string]string{"status": string(row.Status), "failure_class": string(row.FailureClass)})
	return state.LogEvent{
		OccurredAt: row.FinishedAt, AccountID: row.AccountID, AppID: row.AppID, DeploymentID: row.DeploymentID,
		Source: state.LogEventSourceBuild, SourceEventID: "build:" + row.BuildID,
		Level: level, Stream: "system", Message: message, Fields: fields,
	}
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

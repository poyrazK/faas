package state

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// A full environment clone copies the source's crons into the stage paused
// (ADR-20261104120000 amendment 2026-11-12). The capture holds each cron's
// configuration without the scheduler's own columns (last fire, anchor,
// revision, suspension), so a firing source does not count as a changed one.

// ErrProjectEnvironmentCloneCronCaptureUnavailable refuses a source whose
// crons the clone would not copy: crons of an app that has no workload in the
// source environment.
var ErrProjectEnvironmentCloneCronCaptureUnavailable = fmt.Errorf("clone cron capture is unavailable: %w", ErrConflict)

type projectCloneCron struct {
	Schedule              string                     `json:"schedule"`
	Path                  string                     `json:"path"`
	Command               []string                   `json:"command"`
	CommandShell          bool                       `json:"command_shell"`
	CommandTimeoutSeconds int                        `json:"command_timeout_seconds"`
	CommandMaxOutputBytes int                        `json:"command_max_output_bytes"`
	RetryMax              int                        `json:"retry_max"`
	RetryBackoffSeconds   int                        `json:"retry_backoff_seconds"`
	Enabled               bool                       `json:"enabled"`
	Timezone              string                     `json:"timezone"`
	SkipIfRunning         bool                       `json:"skip_if_running"`
	SchedulePolicy        *workpolicy.SchedulePolicy `json:"schedule_policy,omitempty"`
	FailureRules          *workpolicy.FailureRules   `json:"failure_rules,omitempty"`
	Labels                map[string]string          `json:"labels,omitempty"`
}

// projectCloneCrons is one workload's crons in the source environment. A
// snapshot without it predates cron capture and copies none.
type projectCloneCrons struct {
	AppID       string             `json:"app_id"`
	SourceScope string             `json:"source_scope"`
	Crons       []projectCloneCron `json:"crons"`
}

func cronIdentityKey(c projectCloneCron) string {
	return c.Schedule + "\x00" + c.Path + "\x00" + strings.Join(c.Command, "\x00")
}

func normalizeCloneCrons(crons projectCloneCrons) (projectCloneCrons, error) {
	out := projectCloneCrons{AppID: crons.AppID, SourceScope: crons.SourceScope, Crons: make([]projectCloneCron, 0, len(crons.Crons))}
	for _, c := range crons.Crons {
		if c.Schedule == "" || c.Timezone == "" {
			return out, ErrConflict
		}
		c.Command = slices.Clone(c.Command)
		if c.Command == nil {
			c.Command = []string{}
		}
		if len(c.Labels) == 0 {
			c.Labels = nil
		} else {
			c.Labels = maps.Clone(c.Labels)
		}
		out.Crons = append(out.Crons, c)
	}
	sort.Slice(out.Crons, func(i, j int) bool { return cronIdentityKey(out.Crons[i]) < cronIdentityKey(out.Crons[j]) })
	for i := 1; i < len(out.Crons); i++ {
		if cronIdentityKey(out.Crons[i-1]) == cronIdentityKey(out.Crons[i]) {
			return out, ErrConflict
		}
	}
	return out, nil
}

// capturedCloneCrons returns each captured workload's crons by app.
func capturedCloneCrons(records []projectCloneWorkloadRecord) map[string]projectCloneCrons {
	crons := make(map[string]projectCloneCrons, len(records))
	for _, record := range records {
		if record.snapshot.Crons != nil {
			crons[record.AppID] = *record.snapshot.Crons
		}
	}
	return crons
}

// cloneCronCounts counts the crons a clone adds per app. Copied crons count
// against the plan's cron limits like any other, disabled or not.
func cloneCronCounts(crons map[string]projectCloneCrons) (map[string]int, int) {
	perApp := map[string]int{}
	total := 0
	for appID, captured := range crons {
		perApp[appID] += len(captured.Crons)
		total += len(captured.Crons)
	}
	return perApp, total
}

// checkCloneCronQuota refuses a copy that would take an app or the billing
// subject past the plan's cron limits.
func checkCloneCronQuota(perApp map[string]int, appCount func(appID string) (int, error), total, subjectCount int, limits api.Limits) error {
	if total == 0 {
		return nil
	}
	apps := slices.Sorted(maps.Keys(perApp))
	for _, appID := range apps {
		if perApp[appID] == 0 {
			continue
		}
		existing, err := appCount(appID)
		if err != nil {
			return err
		}
		if observed := existing + perApp[appID]; observed > limits.CronLimitPerApp {
			return &CronQuotaError{Scope: CronQuotaScopeApp, Limit: limits.CronLimitPerApp, Observed: observed}
		}
	}
	if observed := subjectCount + total; observed > limits.CronLimitPerAccount {
		return &CronQuotaError{Scope: CronQuotaScopeAccount, Limit: limits.CronLimitPerAccount, Observed: observed}
	}
	return nil
}

// validateCloneCronPublication proves the target holds exactly the captured
// crons, each paused: disabled, marked clone_paused when the source ran it.
func validateCloneCronPublication(record projectCloneWorkloadRecord, actual []projectCloneCron, storedEnabled []bool) error {
	want := projectCloneCrons{AppID: record.AppID}
	if record.snapshot.Crons != nil {
		want.Crons = record.snapshot.Crons.Crons
	}
	want, err := normalizeCloneCrons(want)
	if err != nil {
		return err
	}
	for _, enabled := range storedEnabled {
		if enabled {
			return fmt.Errorf("clone workload %q has an enabled cron before publication: %w", record.WorkloadSlug, ErrConflict)
		}
	}
	got, err := normalizeCloneCrons(projectCloneCrons{AppID: record.AppID, Crons: actual})
	if err != nil || !slices.EqualFunc(want.Crons, got.Crons, sameCloneCron) {
		return fmt.Errorf("clone workload %q crons differ from its capture: %w", record.WorkloadSlug, ErrConflict)
	}
	return nil
}

func sameCloneCron(a, b projectCloneCron) bool {
	return cronIdentityKey(a) == cronIdentityKey(b) && a.CommandShell == b.CommandShell &&
		a.CommandTimeoutSeconds == b.CommandTimeoutSeconds && a.CommandMaxOutputBytes == b.CommandMaxOutputBytes &&
		a.RetryMax == b.RetryMax && a.RetryBackoffSeconds == b.RetryBackoffSeconds && a.Enabled == b.Enabled &&
		a.Timezone == b.Timezone && a.SkipIfRunning == b.SkipIfRunning && maps.Equal(a.Labels, b.Labels) &&
		sameWorkPolicy(a.SchedulePolicy, b.SchedulePolicy) && sameWorkPolicy(a.FailureRules, b.FailureRules)
}

// CloneResumeStore enables the crons a full clone paused in one environment.
type CloneResumeStore interface {
	ResumeEnvironmentClonePausedCrons(ctx context.Context, accountID, projectID, environment string) ([]Cron, error)
}

func (m *MemStore) captureCloneCronsLocked(appID, scope string) (projectCloneCrons, error) {
	cronScope := NormalizeCronDeploymentScope(scope)
	captured := projectCloneCrons{AppID: appID, SourceScope: cronScope}
	for _, c := range m.crons {
		if c.AppID != appID || c.DeploymentScope != cronScope {
			continue
		}
		captured.Crons = append(captured.Crons, m.cloneCronFromLocked(c))
	}
	return normalizeCloneCrons(captured)
}

func (m *MemStore) cloneCronFromLocked(c Cron) projectCloneCron {
	timezone := c.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	return projectCloneCron{Schedule: c.Schedule, Path: c.Path, Command: c.Command, CommandShell: c.CommandShell,
		CommandTimeoutSeconds: c.CommandTimeoutSeconds, CommandMaxOutputBytes: c.CommandMaxOutputBytes,
		RetryMax: c.RetryMax, RetryBackoffSeconds: c.RetryBackoffSeconds, Enabled: c.Enabled || c.ClonePaused,
		Timezone: timezone, SkipIfRunning: c.SkipIfRunning, SchedulePolicy: c.SchedulePolicy, FailureRules: c.FailureRules,
		Labels: maps.Clone(m.resourceLabels[LabelKindCron][c.ID])}
}

// requireCloneCronsCapturedLocked refuses a source whose environment holds
// crons on project apps outside the captured workloads.
func (m *MemStore) requireCloneCronsCapturedLocked(projectID, source string, captured map[string]bool) error {
	cronScope := NormalizeCronDeploymentScope(source)
	for _, c := range m.crons {
		app, ok := m.apps[c.AppID]
		if !ok || app.ProjectID != projectID || app.Status == "deleted" || c.DeploymentScope != cronScope || captured[c.AppID] {
			continue
		}
		return ErrProjectEnvironmentCloneCronCaptureUnavailable
	}
	return nil
}

func (m *MemStore) memCronCountLocked(appID string) int {
	n := 0
	for _, c := range m.crons {
		if c.AppID == appID {
			n++
		}
	}
	return n
}

func (m *MemStore) checkCloneCronQuotaLocked(crons map[string]projectCloneCrons, limits api.Limits) error {
	perApp, total := cloneCronCounts(crons)
	if total == 0 {
		return nil
	}
	accountID := ""
	for appID := range perApp {
		if app, ok := m.apps[appID]; ok {
			accountID = app.AccountID
			break
		}
	}
	subject := 0
	for _, c := range m.crons {
		if app, ok := m.apps[c.AppID]; ok && app.AccountID == accountID && app.Status != "deleted" {
			subject++
		}
	}
	return checkCloneCronQuota(perApp, func(appID string) (int, error) { return m.memCronCountLocked(appID), nil }, total, subject, limits)
}

func (m *MemStore) copyCapturedCronsLocked(clone ProjectEnvironmentClone, target ProjectEnvironment, now time.Time, result *ProjectEnvironmentCloneResult) {
	for appID, crons := range clone.capturedCrons {
		for _, c := range crons.Crons {
			id := uuid.NewString()
			m.crons[id] = Cron{ID: id, AppID: appID, DeploymentScope: target.Slug, Schedule: c.Schedule, Path: c.Path,
				Command: slices.Clone(c.Command), CommandShell: c.CommandShell, CommandTimeoutSeconds: c.CommandTimeoutSeconds,
				CommandMaxOutputBytes: c.CommandMaxOutputBytes, RetryMax: c.RetryMax, RetryBackoffSeconds: c.RetryBackoffSeconds,
				Enabled: false, ClonePaused: c.Enabled, Timezone: c.Timezone, SkipIfRunning: c.SkipIfRunning,
				SchedulePolicy: c.SchedulePolicy, FailureRules: c.FailureRules, CreatedAt: now, ScheduleRevision: 1}
			if len(c.Labels) > 0 {
				if m.resourceLabels == nil {
					m.resourceLabels = make(map[LabelKind]map[string]map[string]string)
				}
				if m.resourceLabels[LabelKindCron] == nil {
					m.resourceLabels[LabelKindCron] = make(map[string]map[string]string)
				}
				m.resourceLabels[LabelKindCron][id] = maps.Clone(c.Labels)
			}
			result.CronsCopied++
		}
	}
}

func (m *MemStore) verifyCloneCronPublicationLocked(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) error {
	for _, record := range records {
		var actual []projectCloneCron
		var stored []bool
		for _, c := range m.crons {
			if c.AppID != record.AppID || c.DeploymentScope != op.TargetEnvironment {
				continue
			}
			actual = append(actual, m.cloneCronFromLocked(c))
			stored = append(stored, c.Enabled)
		}
		if err := validateCloneCronPublication(record, actual, stored); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) ResumeEnvironmentClonePausedCrons(_ context.Context, accountID, projectID, environment string) ([]Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if IsProductionScope(environment) {
		return nil, ErrInvalidArgument
	}
	found := false
	for _, env := range m.projectEnvironments {
		if env.AccountID == accountID && env.ProjectID == projectID && env.Slug == environment {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	now := time.Now()
	var resumed []Cron
	for id, c := range m.crons {
		app, ok := m.apps[c.AppID]
		if !ok || app.ProjectID != projectID || c.DeploymentScope != environment || !c.ClonePaused || c.Enabled {
			continue
		}
		before := c
		c.Enabled = true
		c = anchorCronSchedule(before, c, now)
		m.crons[id] = c
		resumed = append(resumed, c)
	}
	sort.Slice(resumed, func(i, j int) bool { return resumed[i].ID < resumed[j].ID })
	return resumed, nil
}

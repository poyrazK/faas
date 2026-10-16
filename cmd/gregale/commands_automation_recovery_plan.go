package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type automationRecoveryPlan struct {
	ParentJobID string                        `json:"parent_job_id,omitempty"`
	Version     int                           `json:"version"`
	App         string                        `json:"app"`
	AppID       string                        `json:"app_id"`
	Name        string                        `json:"name"`
	Key         string                        `json:"idempotency_key"`
	CreatedAt   time.Time                     `json:"created_at"`
	Runs        []automationRecoverySelection `json:"runs"`
}

func validateAutomationRecoveryPlan(p automationRecoveryPlan) error {
	if p.Version != 1 || !validAutomationRunSelection(p.App, p.Name) || !workflowUUIDPattern.MatchString(p.AppID) || p.Key == "" || p.CreatedAt.IsZero() || len(p.Runs) == 0 || len(p.Runs) > api.AutomationRecoveryBatchMaxRuns {
		return errors.New("invalid recovery plan version, scope or selection")
	}
	if err := validateDeployIdempotencyKey(p.Key); err != nil {
		return fmt.Errorf("validate plan key: %w", err)
	}
	if p.ParentJobID != "" && !workflowUUIDPattern.MatchString(p.ParentJobID) {
		return errors.New("invalid retry parent")
	}
	seen := map[string]bool{}
	positions := map[int64]bool{}
	for _, r := range p.Runs {
		parsed, err := parseAutomationRecoverySelection(fmt.Sprintf("%s:%d", r.ID, r.ExpectedResumeCount))
		if err != nil || parsed.ID != r.ID || seen[r.ID] {
			return errors.New("invalid or duplicate plan run")
		}
		if r.FailureEvidenceID != "" && (!workflowUUIDPattern.MatchString(r.FailureEvidenceID) || r.ExpectedResumeCount != 0 || p.ParentJobID != "") {
			return errors.New("invalid initial failure evidence")
		}
		if r.ParentPosition < 0 || (p.ParentJobID == "") != (r.ParentPosition == 0) || r.ParentPosition > api.AutomationRecoveryBatchMaxRuns || r.ParentPosition != 0 && positions[r.ParentPosition] || p.ParentJobID != "" && r.ExpectedResumeCount < 1 {
			return errors.New("invalid retry lineage")
		}
		positions[r.ParentPosition] = true
		seen[r.ID] = true
	}
	return nil
}

func saveAutomationRecoveryPlan(ctx context.Context, c *api.Client, app, name, key string, runs []automationRecoverySelection, path string) error {
	appID, err := loadAutomationRunScope(ctx, c, app, name)
	if err != nil {
		return fmt.Errorf("resolve plan scope: %w", err)
	}
	for _, item := range runs {
		run, err := c.GetWorkflowRun(ctx, item.ID)
		if err != nil {
			return fmt.Errorf("inspect plan run: %w", err)
		}
		if !recoveryRunInScope(run, appID, name, item.ID) || run.ResumeCount != item.ExpectedResumeCount || run.Status != "failed" && run.Status != "dead" {
			return errors.New("selected run scope or reviewed state changed")
		}
	}
	p := automationRecoveryPlan{Version: 1, App: app, AppID: appID, Name: name, Key: key, CreatedAt: time.Now().UTC(), Runs: runs}
	if err := validateAutomationRecoveryPlan(p); err != nil {
		return err
	}
	return writeAutomationRecoveryPlanFile(path, p)
}

func writeAutomationRecoveryPlanFile(path string, p automationRecoveryPlan) error {
	if err := validateAutomationRecoveryPlan(p); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create plan: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := persistRecoveryRecord(f, p); err != nil {
		return err
	}
	return f.Close()
}

func readAutomationRecoveryPlan(path string) (automationRecoveryPlan, error) {
	var p automationRecoveryPlan
	f, err := openCustomerFile(path)
	if err != nil {
		return p, fmt.Errorf("open plan: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return p, fmt.Errorf("inspect plan file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > api.AutomationRecoveryPlanMaxBytes {
		return p, errors.New("plan must be a regular file of at most 64 KiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, api.AutomationRecoveryPlanMaxBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, fmt.Errorf("decode plan: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return p, errors.New("plan must contain exactly one JSON object")
	}
	return p, validateAutomationRecoveryPlan(p)
}

// Sync each entry before proceeding: an attempted entry with no outcome means
// the request may have committed. Retry the original plan, never refreshed counts.
func persistRecoveryRecord(f *os.File, value any) error {
	if err := json.NewEncoder(f).Encode(value); err != nil {
		return fmt.Errorf("write recovery file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync recovery file: %w", err)
	}
	dir, err := openCustomerDirectoryForSync(filepath.Dir(f.Name()))
	if err != nil {
		return fmt.Errorf("open recovery directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync recovery directory: %w", err)
	}
	return dir.Close()
}

type automationRecoveryJournalEntry struct {
	Kind    string                       `json:"kind"`
	At      time.Time                    `json:"at"`
	Run     *automationRecoverySelection `json:"run,omitempty"`
	Outcome *automationRecoveryOutcome   `json:"outcome,omitempty"`
	Plan    *automationRecoveryPlan      `json:"plan,omitempty"`
}

func executeAutomationRecoveryPlan(ctx context.Context, c *api.Client, p automationRecoveryPlan, journal *os.File) (automationRecoveryReport, error) {
	report := automationRecoveryReport{App: p.App, Name: p.Name, StartedAt: time.Now().UTC(), Outcomes: []automationRecoveryOutcome{}}
	if err := validateAutomationRecoveryPlan(p); err != nil {
		return report, err
	}
	if p.ParentJobID != "" {
		return report, errors.New("submit retry plans with automations recovery-start")
	}
	for _, target := range p.Runs {
		if target.FailureEvidenceID != "" {
			return report, errors.New("submit failure-evidence plans with automations recovery-start")
		}
	}
	appID, err := loadAutomationRunScope(ctx, c, p.App, p.Name)
	if err != nil {
		return report, fmt.Errorf("resolve plan scope: %w", err)
	}
	if appID != p.AppID {
		return report, errors.New("plan app identity changed")
	}
	for _, item := range p.Runs {
		run, err := c.GetWorkflowRun(ctx, item.ID)
		if err != nil {
			return report, fmt.Errorf("inspect plan run: %w", err)
		}
		if !recoveryRunInScope(run, appID, p.Name, item.ID) {
			return report, errors.New("plan run outside selected scope")
		}
	}
	if err := persistRecoveryRecord(journal, automationRecoveryJournalEntry{Kind: "started", At: report.StartedAt, Plan: &p}); err != nil {
		return report, err
	}
	for _, item := range p.Runs {
		if ctx.Err() != nil {
			return report, fmt.Errorf("execute recovery plan: %w", ctx.Err())
		}
		if err := persistRecoveryRecord(journal, automationRecoveryJournalEntry{Kind: "attempted", At: time.Now().UTC(), Run: &item}); err != nil {
			return report, err
		}
		out := recoverAutomationRun(ctx, c, appID, p.Name, p.Key, item)
		report.Outcomes = append(report.Outcomes, out)
		if err := persistRecoveryRecord(journal, automationRecoveryJournalEntry{Kind: "outcome", At: time.Now().UTC(), Outcome: &out}); err != nil {
			return report, err
		}
	}
	report.CompletedAt = time.Now().UTC()
	if err := persistRecoveryRecord(journal, automationRecoveryJournalEntry{Kind: "completed", At: report.CompletedAt}); err != nil {
		return report, err
	}
	return report, nil
}

func cmdAutomationsRecoveryPlan(args []string) int {
	fs := newFlagSet("automations recovery-plan", flag.ContinueOnError)
	path := fs.String("plan", "", "reviewed recovery plan file")
	journalPath := fs.String("journal", "", "new journal file for this execution")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *path == "" || *journalPath == "" {
		return printErr("Invalid recovery plan arguments", errors.New("provide --plan and --journal"))
	}
	p, err := readAutomationRecoveryPlan(*path)
	if err != nil {
		return printErr("Invalid recovery plan", err)
	}
	if p.ParentJobID != "" {
		return printErr("Use durable retry submission", errors.New("submit retry plans with automations recovery-start"))
	}
	for _, target := range p.Runs {
		if target.FailureEvidenceID != "" {
			return printErr("Use durable recovery submission", errors.New("submit failure-evidence plans with automations recovery-start"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	journal, err := os.OpenFile(*journalPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return printErr("Could not create recovery journal", err)
	}
	defer func() { _ = journal.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := executeAutomationRecoveryPlan(ctx, client, p, journal)
	if err != nil {
		return printErr("Recovery stopped; inspect the journal and retry the original plan with a new journal", err)
	}
	if err := journal.Close(); err != nil {
		return printErr("Could not close recovery journal", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else if err := renderAutomationRecoveryReport(osStdout, report); err != nil {
		return printErr("Could not write recovery report", err)
	}
	for _, out := range report.Outcomes {
		if out.Outcome != "resumed" {
			return 1
		}
	}
	return 0
}

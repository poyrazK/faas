package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// ExecutionCheck is a deliberately narrow, credential-free projection. Neither
// repository commands nor guest logs, artifact paths or provider errors fit here.
type ExecutionCheck struct {
	AccountID         string
	ExecutionID       string
	AutomationEventID string // skipped source only; mutually exclusive with ExecutionID
	BindingID         string
	Pipeline          string
	InstallationID    int64
	RepositoryID      int64
	Repository        string
	CommitSHA         string
	Version           int64
	Status            string
	Conclusion        string
	Summary           string
	CheckID           int64
}

func (c ExecutionCheck) Name() string { return "gregale-ci / " + c.Pipeline + " / " + c.BindingID }
func (c ExecutionCheck) ExternalID() string {
	if c.AutomationEventID != "" {
		return "gregale-ci-source:" + c.AutomationEventID
	}
	return "gregale-ci-execution:" + c.ExecutionID
}
func (c ExecutionCheck) Title() string {
	return fmt.Sprintf("Gregale CI: %s (version %d)", c.Status, c.Version)
}

func (c ExecutionCheck) Valid() bool {
	validState := (c.Status == "queued" || c.Status == "in_progress") && c.Conclusion == "" ||
		c.Status == "completed" && (c.Conclusion == "success" || c.Conclusion == "failure" || c.Conclusion == "cancelled" || c.Conclusion == "timed_out")
	identity := ValidID(c.ExecutionID) && c.AutomationEventID == ""
	if c.AutomationEventID != "" {
		identity = c.ExecutionID == "" && ValidID(c.AutomationEventID) && c.Version == 1
		validState = c.Status == "completed" && c.Conclusion == "skipped"
	}
	return ValidID(c.AccountID) && identity && ValidID(c.BindingID) && ValidName(c.Pipeline) &&
		c.InstallationID > 0 && c.RepositoryID > 0 && repoPattern.MatchString(c.Repository) && len(c.Repository) <= api.CIRepositoryMaxBytes &&
		ValidCommitSHA(c.CommitSHA) && c.Version > 0 && c.CheckID >= 0 && validState && len(c.Summary) <= api.CIExecutionCheckSummaryMaxBytes
}

func executionCheck(row executionRecord) (ExecutionCheck, error) {
	e := row.Execution
	if e.Mode != ExecutionJobs || e.Compute == nil {
		return ExecutionCheck{}, ErrConflict
	}
	b := e.Compute.Binding
	c := ExecutionCheck{AccountID: e.AccountID, ExecutionID: e.ID, BindingID: b.ID, Pipeline: e.Pipeline,
		InstallationID: b.InstallationID, RepositoryID: b.RepositoryID, Repository: b.Repository, CommitSHA: e.CommitSHA, Version: row.Version}
	switch e.Status {
	case StatusQueued:
		c.Status = "queued"
	case StatusRunning, StatusCancelling:
		c.Status = "in_progress"
	case StatusSucceeded:
		c.Status, c.Conclusion = "completed", "success"
	case StatusCancelled:
		c.Status, c.Conclusion = "completed", "cancelled"
	case StatusFailed:
		c.Status, c.Conclusion = "completed", "failure"
		for _, job := range e.Jobs {
			if job.Attempt != nil && job.Attempt.CancelReason == ReasonTimeout {
				c.Conclusion = "timed_out"
			}
		}
	default:
		return c, ErrConflict
	}
	var summary strings.Builder
	fmt.Fprintf(&summary, "Execution: %s. State: %s.\n\n", e.ID, e.Status)
	for _, job := range e.Jobs {
		if !ValidName(job.Name) || !checkJobStatus(job.Status) {
			return c, ErrConflict
		}
		completed, total := 0, 0
		if job.Attempt != nil {
			total = len(job.Attempt.Steps)
			for _, step := range job.Attempt.Steps {
				if terminal(step.Status) {
					completed++
				}
			}
		}
		fmt.Fprintf(&summary, "- %s: %s; steps completed: %d/%d.\n", job.Name, job.Status, completed, total)
	}
	reports, err := ExecutionReports(e)
	if err != nil {
		return c, err
	}
	if totals := reports.Totals; totals.Expected > 0 {
		fmt.Fprintf(&summary, "\nTest reports parsed: %d/%d. Diagnostic totals from parsed reports: tests=%d, passed=%d, failed=%d, errors=%d, skipped=%d. Report problems=%d.\n",
			totals.Parsed, totals.Expected, totals.Tests, totals.Passed, totals.Failed, totals.Errors, totals.Skipped, totals.Problems)
	}
	c.Summary = summary.String()
	if !c.Valid() {
		return c, ErrConflict
	}
	return c, nil
}

func checkJobStatus(status string) bool {
	return terminal(status) || status == StatusWaiting || status == StatusQueued || status == StatusStarting || status == StatusRunning || status == StatusCancelling
}

// Versions used only for leases are acknowledged without redundant API writes.
func (c ExecutionCheck) digest() string {
	c.Version, c.CheckID = 0, 0
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type executionCheckClaim struct {
	account, execution, token string
	source                    bool
}
type ExecutionCheckPublisher func(context.Context, ExecutionCheck) (int64, error)

type ExecutionCheckStore interface {
	claimExecutionCheck(context.Context, string) (executionCheckClaim, error)
	deliverExecutionCheck(context.Context, executionCheckClaim, GitHubVerifier, ExecutionCheckPublisher) error
}

func PublishExecutionCheckOnce(ctx context.Context, cfg Config, store ExecutionCheckStore, verify GitHubVerifier, publish ExecutionCheckPublisher) (bool, error) {
	if !cfg.ExecutionChecksEnabled || !cfg.GitHubChecksEnabled || store == nil || verify == nil || publish == nil {
		return false, nil
	}
	for _, account := range cfg.GitHubAccounts() {
		claim, err := store.claimExecutionCheck(ctx, account)
		if errors.Is(err, ErrNoGitHubWork) {
			continue
		}
		if err != nil {
			return false, err
		}
		return true, store.deliverExecutionCheck(ctx, claim, verify, publish)
	}
	return false, nil
}

func RunExecutionCheckWorker(ctx context.Context, cfg Config, store ExecutionCheckStore, verify GitHubVerifier, publish ExecutionCheckPublisher, log *slog.Logger) {
	if !cfg.ExecutionChecksEnabled || !cfg.GitHubChecksEnabled || len(cfg.GitHubAccounts()) == 0 || store == nil || verify == nil || publish == nil {
		return
	}
	runGitHubWorker(ctx, func(ctx context.Context) (bool, error) {
		worked, err := PublishExecutionCheckOnce(ctx, cfg, store, verify, publish)
		if err != nil {
			err = errors.New("CI execution Check delivery unavailable")
		}
		return worked, err
	}, log)
}

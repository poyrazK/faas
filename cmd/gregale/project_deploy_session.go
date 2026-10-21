package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/api"
)

// Project sessions contain identity and acceptance metadata only. Never retain
// source, manifests, secrets, request bodies or arbitrary server error messages.
type savedProjectDeploy struct {
	SafeRelease       *api.ProjectSafeRelease `json:"safe_release,omitempty"`
	AttemptID         string                  `json:"submission_attempt_id,omitempty"`
	Refused           bool                    `json:"submission_refused,omitempty"`
	SelectionComplete bool                    `json:"selection_complete,omitempty"`
	SubmissionStatus  string                  `json:"submission_status,omitempty"`
	Slug              string                  `json:"slug"`
	ID                string                  `json:"project_id,omitempty"`
	Environment       string                  `json:"environment,omitempty"`
	Apps              []api.ApplyResponseApp  `json:"apps,omitempty"`
	Workloads         []savedProjectWorkload  `json:"workloads"`
}
type savedProjectWorkload struct {
	Slug          string `json:"slug"`
	AppID         string `json:"app_id,omitempty"`
	DeploymentID  string `json:"deployment_id,omitempty"`
	BuildID       string `json:"build_id,omitempty"`
	NotAccepted   bool   `json:"not_accepted,omitempty"`
	ManagedCrons  bool   `json:"managed_crons,omitempty"`
	CronSessionID string `json:"cron_session_id,omitempty"`
}
type projectDeployReceipt struct {
	api.ApplyResponse
	SessionID         string                  `json:"session_id,omitempty"`
	ResumeCommand     string                  `json:"resume_command,omitempty"`
	RecoverySaved     bool                    `json:"recovery_saved"`
	SubmissionOutcome deploySubmissionOutcome `json:"submission_outcome,omitempty"`
	SubmissionStatus  string                  `json:"submission_status,omitempty"`
}

func newSavedProjectDeploy(c *Client, slug, environment string, apply api.ApplyResponse, pendingAttempt ...string) (*savedDeploySession, *flock.Flock, error) {
	if _, err := validateConfigAPIBase(c.BaseURL()); err != nil {
		return nil, nil, err
	}
	dir, err := deploySessionDir(c)
	if err != nil {
		return nil, nil, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, err
	}
	p := &savedProjectDeploy{SafeRelease: savedSafeProjectRelease(apply.SafeRelease), Slug: slug, ID: apply.ProjectID, Environment: environment, Apps: append([]api.ApplyResponseApp{}, apply.Apps...)}
	for _, build := range apply.Builds {
		row := savedProjectWorkload{Slug: build.Slug, AppID: build.AppID, DeploymentID: build.DeploymentID, BuildID: build.BuildID, NotAccepted: build.DeploymentID == ""}
		if build.Configuration != nil && build.Configuration.Crons.Status != "not_managed" {
			row.ManagedCrons, row.CronSessionID = true, build.Configuration.Crons.SessionID
		}
		p.Workloads = append(p.Workloads, row)
	}
	s := &savedDeploySession{record: deploySessionRecord{Version: deploySessionVersion, ID: hex.EncodeToString(random[:]), Connection: deployConnection(c), AppSlug: slug, CreatedAt: time.Now().UTC(), Outcome: deploySubmissionAccepted, Project: p}}
	if len(pendingAttempt) > 0 {
		s.record.Outcome = deploySubmissionUnknown
		p.AttemptID = pendingAttempt[0]
		if p.AttemptID == "" {
			p.AttemptID = s.record.ID
		}
	}
	if err := validateSavedProjectDeploy(s.record); err != nil {
		return nil, nil, err
	}
	s.path = filepath.Join(dir, s.record.ID+".json")
	lock, err := lockDeploySession(s.path)
	if err != nil {
		return nil, nil, err
	}
	if err := s.save(); err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	return s, lock, nil
}

func projectRecoveryID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
		if !allowed {
			return false
		}
	}
	return true
}
func validateSavedProjectDeploy(r deploySessionRecord) error {
	p := r.Project
	if p == nil {
		return nil
	}
	invalid := errors.New("saved project deploy has invalid recovery metadata")
	if !api.ValidProjectSlug(p.Slug) || p.Slug != r.AppSlug || (r.Outcome != deploySubmissionAccepted && p.AttemptID == "") || r.DeploymentID != "" || r.AppID != "" || r.AttemptID != "" || r.Upload != nil || len(r.Settings) > 0 || r.CronJournal != nil || r.PolicyJournal != nil {
		return invalid
	}
	if p.AttemptID != "" && !api.ValidDeploymentAttemptID(p.AttemptID) {
		return invalid
	}
	if p.ID != "" && !projectRecoveryID(p.ID) || p.Environment != "" && !api.ValidProjectEnvironmentSlug(p.Environment) {
		return invalid
	}
	if safe := p.SafeRelease; safe != nil {
		if !projectRecoveryID(safe.PreviousReleaseID) || safe.ReleaseID != "" && !projectRecoveryID(safe.ReleaseID) || safe.TTLSeconds <= 0 || safe.Deadline.IsZero() {
			return invalid
		}
		switch safe.Status {
		case "building", "failed", "conflicted":
		case "activated", "verifying", "rolling_back", "succeeded", "rolled_back", "rollback_failed":
			if safe.ReleaseID == "" {
				return invalid
			}
		default:
			return invalid
		}
	}
	seenApps, seenDeployments := map[string]bool{}, map[string]bool{}
	for _, app := range p.Apps {
		if !api.ValidAppSlug(app.Slug) || !projectRecoveryID(app.ID) {
			return invalid
		}
	}
	for _, row := range p.Workloads {
		if !api.ValidAppSlug(row.Slug) || row.AppID != "" && !projectRecoveryID(row.AppID) || row.BuildID != "" && !projectRecoveryID(row.BuildID) {
			return invalid
		}
		if row.NotAccepted != (row.DeploymentID == "") || row.DeploymentID != "" && (!projectRecoveryID(row.DeploymentID) || seenDeployments[row.DeploymentID]) {
			return invalid
		}
		if row.AppID != "" && seenApps[row.AppID] {
			return invalid
		}
		if row.CronSessionID != "" && !api.ValidDeploymentAttemptID(row.CronSessionID) {
			return invalid
		}
		seenApps[row.AppID], seenDeployments[row.DeploymentID] = row.AppID != "", row.DeploymentID != ""
	}
	return nil
}

func (s *savedDeploySession) projectApplyReceipt() api.ApplyResponse {
	p := s.record.Project
	apply := api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: p.SafeRelease}, ProjectID: p.ID, Apps: append([]api.ApplyResponseApp{}, p.Apps...)}
	for _, saved := range p.Workloads {
		row := api.AppliedBuild{Slug: saved.Slug, AppID: saved.AppID, DeploymentID: saved.DeploymentID, BuildID: saved.BuildID}
		if saved.NotAccepted {
			row.Error = "workload was not accepted; inspect the original apply result before retrying"
		}
		if saved.ManagedCrons {
			config := api.NewDeploymentConfigurationStatus("prepared", "awaiting_readiness")
			config.Crons.SessionID = saved.CronSessionID
			row.Configuration = &config
		}
		apply.Builds = append(apply.Builds, row)
	}
	return apply
}

func renderProjectDeployReceipt(plan api.PlanResponse, apply api.ApplyResponse, session *savedDeploySession, timedOut bool, saveErr error) int {
	receipt := projectDeployReceipt{ApplyResponse: apply, RecoverySaved: session != nil}
	if session != nil {
		receipt.SessionID, receipt.ResumeCommand = session.record.ID, session.resumeCommand()
		receipt.SubmissionOutcome = session.record.Outcome
		receipt.SubmissionStatus = session.record.Project.SubmissionStatus
	}
	status := summarizeProjectApply(apply)
	code := 0
	if jsonOutput {
		if code = jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
		if status.buildsFailed > 0 {
			reportProjectApplyFailure(osStderr, status)
			code = 1
		}
	} else {
		code = renderProjectApplyResult(osStdout, plan, apply)
		if session != nil {
			PrintProgress(osStdout, "Saved project deploy %s; resume with: %s", session.record.ID, session.resumeCommand())
		}
	}
	if apply.SafeRelease != nil {
		if !jsonOutput {
			PrintProgress(osStdout, "Safe project release: %s; previous release %s", apply.SafeRelease.Status, apply.SafeRelease.PreviousReleaseID)
		}
		if !jsonOutput && apply.SafeRelease.ReleaseCommand != nil {
			command := apply.SafeRelease.ReleaseCommand
			PrintProgress(osStdout, "Release command: %s; task %s", command.Status, command.TaskID)
			if command.TaskID != "" {
				PrintProgress(osStdout, "Migration logs: GET /v1/apps/%s/tasks/%s", command.Slug, command.TaskID)
			}
		}
		if apply.SafeRelease.Status == "failed" || apply.SafeRelease.Status == "conflicted" || apply.SafeRelease.Status == "rolled_back" || apply.SafeRelease.Status == "rollback_failed" {
			if !jsonOutput && apply.SafeRelease.Message != "" {
				PrintWarn(osStdout, "%s", apply.SafeRelease.Message)
			}
			code = 1
		}
	}
	if saveErr != nil {
		return printErr("Project apply accepted but recovery could not be saved", saveErr)
	}
	if timedOut {
		return 3
	}
	return code
}
func resumeSavedProjectDeploy(ctx context.Context, c *Client, s *savedDeploySession, deadline time.Duration) int {
	waitCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	if s.record.Project.Refused {
		return projectSubmissionFailure(s, errors.New("this attempt was refused because its request intent changed; use a new idempotency key"))
	}
	if s.record.Project.AttemptID != "" {
		if err := s.discoverProjectSubmission(waitCtx, c); err != nil {
			return projectSubmissionFailure(s, err)
		}
	}
	if s.record.Project.SafeRelease != nil {
		return waitForSafeProjectRelease(waitCtx, c, s, deadline)
	}
	apply, timedOut := waitForProjectApply(waitCtx, c, s.projectApplyReceipt(), deadline)
	return renderProjectDeployReceipt(api.PlanResponse{}, apply, s, timedOut, nil)
}

func (s *savedDeploySession) rememberProjectApply(apply api.ApplyResponse) error {
	p := s.record.Project
	if p.SafeRelease != nil && apply.SafeRelease != nil && (p.SafeRelease.AddServices != apply.SafeRelease.AddServices || api.ProjectReleaseAdditionsPolicyID(p.SafeRelease.Additions) != api.ProjectReleaseAdditionsPolicyID(apply.SafeRelease.Additions)) {
		return errors.New("project service additions changed from the saved plan")
	}
	if p.SafeRelease != nil && apply.SafeRelease != nil && (p.SafeRelease.RemoveServices != apply.SafeRelease.RemoveServices || api.ProjectReleaseRemovalsPolicyID(p.SafeRelease.Removals) != api.ProjectReleaseRemovalsPolicyID(apply.SafeRelease.Removals)) {
		return errors.New("project service removals changed from the saved plan")
	}
	if p.SafeRelease != nil && apply.SafeRelease != nil {
		for i, addition := range p.SafeRelease.Additions {
			if addition.AppID != "" && addition.AppID != apply.SafeRelease.Additions[i].AppID {
				return errors.New("reserved service identity changed in the server receipt")
			}
		}
	}
	if p.SafeRelease != nil && apply.SafeRelease != nil && p.SafeRelease.PreviousReleaseID != apply.SafeRelease.PreviousReleaseID {
		return errors.New("safe release baseline changed in the server receipt")
	}
	if p.SafeRelease != nil && apply.SafeRelease != nil && api.ProjectReleaseVerificationPolicyID(p.SafeRelease.Verification) != api.ProjectReleaseVerificationPolicyID(apply.SafeRelease.Verification) {
		return errors.New("safe release verification policy changed in the server receipt")
	}
	if p.SafeRelease != nil && apply.SafeRelease != nil && api.ProjectReleaseWorkerPolicyID(p.SafeRelease.Worker) != api.ProjectReleaseWorkerPolicyID(apply.SafeRelease.Worker) {
		return errors.New("project worker binding changed from the saved plan")
	}
	if p.SafeRelease != nil && apply.SafeRelease != nil && api.ProjectReleaseCommandPolicyID(p.SafeRelease.ReleaseCommand) != api.ProjectReleaseCommandPolicyID(apply.SafeRelease.ReleaseCommand) {
		return errors.New("project migration command changed from the saved plan")
	}
	if p.SafeRelease != nil && apply.SafeRelease != nil && api.ProjectReleaseConfigurationPolicyID(p.SafeRelease.Configuration) != api.ProjectReleaseConfigurationPolicyID(apply.SafeRelease.Configuration) {
		return errors.New("release configuration changed in the server receipt")
	}
	if p.SafeRelease != nil && apply.SafeRelease == nil {
		return errors.New("safe release checkpoint missing from server response")
	}
	p.SafeRelease = savedSafeProjectRelease(apply.SafeRelease)
	p.ID, p.Apps = apply.ProjectID, append([]api.ApplyResponseApp{}, apply.Apps...)
	p.Workloads = nil
	for _, build := range apply.Builds {
		row := savedProjectWorkload{Slug: build.Slug, AppID: build.AppID, DeploymentID: build.DeploymentID, BuildID: build.BuildID, NotAccepted: build.DeploymentID == ""}
		if build.Configuration != nil && build.Configuration.Crons.Status != "not_managed" {
			row.ManagedCrons, row.CronSessionID = true, build.Configuration.Crons.SessionID
		}
		p.Workloads = append(p.Workloads, row)
	}
	s.record.Outcome = deploySubmissionAccepted
	if err := validateSavedProjectDeploy(s.record); err != nil {
		return err
	}
	return s.save()
}
func (s *savedDeploySession) discoverProjectSubmission(ctx context.Context, c *Client) error {
	p := s.record.Project
	for {
		receipt, err := c.GetProjectSubmission(ctx, p.AttemptID)
		if err == nil {
			if receipt.AttemptID != p.AttemptID || receipt.ProjectSlug != p.Slug || receipt.Environment != p.Environment {
				return errors.New("project submission receipt does not match the saved attempt")
			}
			if err := s.rememberProjectApply(receipt.Apply); err != nil {
				return err
			}
			p.SubmissionStatus = receipt.Status
			if receipt.Status == "processing" || receipt.Status == "interrupted" {
				s.record.Outcome = deploySubmissionUnknown
			}
			if receipt.Status == "rejected" {
				s.record.Outcome = deploySubmissionRejected
			}
			p.SelectionComplete = receipt.SelectionComplete
			if err := s.save(); err != nil {
				return err
			}
			switch receipt.Status {
			case "complete":
				return nil
			case "interrupted":
				if receipt.SelectionComplete {
					return nil
				}
				return errors.New("project submission was interrupted before workload selection could be confirmed; no work was retried")
			case "rejected":
				return errors.New("project submission " + receipt.Status + "; accepted workload IDs were recovered, but unconfirmed work was not retried")
			case "processing":
			default:
				return errors.New("unknown project submission status")
			}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("project submission acceptance remains uncertain; resume only checks receipts and never resubmits: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
func projectSubmissionFailure(s *savedDeploySession, err error) int {
	receipt := projectDeployReceipt{ApplyResponse: s.projectApplyReceipt(), SessionID: s.record.ID, ResumeCommand: s.resumeCommand(), RecoverySaved: true, SubmissionOutcome: s.record.Outcome, SubmissionStatus: s.record.Project.SubmissionStatus}
	if jsonOutput {
		if code := jsonOut(writeJSON(receipt)); code != 0 {
			return code
		}
	} else {
		PrintWarn(osStdout, "Project submission is incomplete; resume with: %s", s.resumeCommand())
		renderProjectApplyResult(osStdout, api.PlanResponse{}, receipt.ApplyResponse)
	}
	return printErr("Project submission could not be confirmed; resume with: "+s.resumeCommand(), err)
}

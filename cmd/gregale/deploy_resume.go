package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdDeployResume(ctx context.Context, args []string) int {
	fs := newFlagSet("deploy resume", flag.ContinueOnError)
	app := fs.String("app", "", "resume the most recent saved deploy for an app")
	timeout := fs.Int("timeout", int(defaultDeployWaitTimeout/time.Second), "maximum wait seconds")
	flagArgs, positionals := splitArgsForFlags(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if (*app == "" && len(positionals) != 1) || (*app != "" && len(positionals) != 0 && len(positionals) != 1) || *timeout <= 0 || (*app != "" && !api.ValidAppSlug(*app)) {
		return printErr("Invalid deploy resume arguments", errors.New("usage: gregale release resume SESSION_ID [--timeout SECONDS] or gregale release resume --app SLUG"))
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	path, err := selectDeploySession(c, positionals, *app)
	if err != nil {
		return printErr("Could not select deploy session", err)
	}
	lock, err := lockDeploySession(path)
	if err != nil {
		return printErr("Could not lock deploy session", err)
	}
	defer func() { _ = lock.Close() }()
	s, err := loadSavedDeploySession(c, path)
	if err != nil {
		return printErr("Could not read deploy session", err)
	}
	if *app != "" && s.record.AppSlug != *app {
		return printErr("Invalid deploy resume arguments", errors.New("saved deploy belongs to a different app"))
	}
	if s.record.Project != nil {
		if *app != "" {
			return printErr("Invalid deploy resume arguments", errors.New("use the session ID to resume a project deploy"))
		}
		return resumeSavedProjectDeploy(ctx, c, s, time.Duration(*timeout)*time.Second)
	}
	remoteApp, err := c.GetApp(ctx, s.record.AppSlug)
	if err != nil {
		return s.failure("Could not verify saved app", "recovery", err)
	}
	if s.record.AppID != "" && remoteApp.ID != s.record.AppID {
		return s.failure("App identity changed", "recovery", errors.New("the saved app was replaced; recovery stopped"))
	}
	if s.shouldCleanPolicies() {
		if err := s.cleanPolicies(ctx, c); err != nil {
			return s.failure("Deployment settings cleanup remains pending", "configuration", err)
		}
		return s.renderPolicyCleanup()
	}
	if err := s.confirmAcceptance(ctx, c); err != nil {
		return s.failure("Could not confirm deploy acceptance", "submission", err)
	}
	dep, err := c.GetDeployment(ctx, s.record.DeploymentID)
	if err != nil {
		return s.failure("Could not read saved deployment", "deployment", err)
	}
	if dep.ID != s.record.DeploymentID || remoteApp.ID == "" || dep.AppID != remoteApp.ID {
		return s.failure("Deployment identity mismatch", "recovery", errors.New("saved deployment does not belong to the saved app"))
	}
	s.record.AppID = remoteApp.ID
	ctx = context.WithValue(ctx, deploySessionContextKey{}, s)
	appURL := canonicalAppURL(remoteApp)
	prov := &zeroConfigProvenance{SHA: s.record.CommitSHA, Dirty: s.record.Dirty}
	deadline := time.Duration(*timeout) * time.Second
	if jsonOutput {
		return writeWaitedDeploymentReceiptUntilWithOptions(ctx, c, dep, prov, appURL, s.record.SourceSHA256, s.record.AppSlug, deadline, s.record.WaitForRollout, s.record.DarkDeploy)
	}
	waitCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	PrintProgress(osStderr, "Resuming deployment %s; %s", dep.ID, s.resumeCommand())
	final, ok := waitForDeploymentReceiptUntil(waitCtx, c, dep, deadline)
	stage := "deployment"
	if ok && s.record.WaitForRollout && final.Status == statusLive && !deploymentRolloutComplete(final) {
		stage = "rollout"
		final, ok = waitForDeploymentRollout(waitCtx, c, final)
	}
	if !ok {
		exit := 3
		if errors.Is(ctx.Err(), context.Canceled) {
			exit = 130
		}
		return printErr("Deploy resume stopped before completion", &deployRecoveryError{err: waitCtx.Err(), recovery: s.recovery(stage), exit: exit})
	}
	if final.Status != statusLive || (s.record.WaitForRollout && final.RolloutState == rolloutStateAborted) {
		return terminalExitForDeploymentWithFailureContext(ctx, c, final, s.record.AppSlug, nil)
	}
	if s.record.DarkDeploy && final.TrafficPercent != 0 {
		return s.failure("Unexpected production traffic", "deployment", errors.New("saved dark deployment has production traffic; recovery stopped"))
	}
	if err := s.applySettings(ctx, c); err != nil {
		return s.failure("Saved deploy settings remain pending", "configuration", err)
	}
	return renderSuccessfulDeploymentWithOptions(ctx, c, final, s.record.AppSlug, s.record.DarkDeploy)
}

func selectDeploySession(c *Client, ids []string, slug string) (string, error) {
	dir, err := deploySessionDir(c)
	if err != nil {
		return "", err
	}
	if len(ids) == 1 {
		if !validDeploySessionID(ids[0]) {
			return "", errors.New("session ID must be 32 lowercase hexadecimal characters")
		}
		return filepath.Join(dir, ids[0]+".json"), nil
	}
	files, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var newest *savedDeploySession
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		s, err := loadSavedDeploySession(c, filepath.Join(dir, file.Name()))
		if err != nil {
			return "", fmt.Errorf("read saved deploy %s: %w", file.Name(), err)
		}
		if s.record.Project == nil && s.record.AppSlug == slug && (newest == nil || s.record.CreatedAt.After(newest.record.CreatedAt)) {
			newest = s
		}
	}
	if newest == nil {
		return "", errors.New("no saved deploy for this app and connection; use the original profile, endpoint and credential")
	}
	return newest.path, nil
}

func (s *savedDeploySession) confirmAcceptance(ctx context.Context, c *Client) error {
	if s.record.DeploymentID != "" {
		return nil
	}
	if s.record.Outcome == deploySubmissionRejected {
		return errors.New("submission stopped before acceptance; rerun the original deploy after correcting the error")
	}
	if j := s.record.CronJournal; j != nil && j.Deferred && s.record.Upload != nil && s.record.AttemptID != "" {
		// Acceptance is committed before the upload row is marked committed.
		// Negative reads remain uncertain; an owned upload can still supply proof.
		if receipt, err := c.GetDeploymentSubmission(ctx, s.record.AppSlug, s.record.AttemptID); err == nil {
			return s.rememberSubmissionReceipt(receipt)
		}
	}
	if s.record.Upload == nil && s.record.AttemptID != "" {
		return s.confirmSubmissionReceipt(ctx, c)
	}
	if s.record.Upload == nil {
		return errors.New("acceptance is uncertain and no upload receipt is available; inspect deployment history before retrying the original intent")
	}
	upload := s.record.Upload
	remote, err := c.GetUploadSession(ctx, upload.ID)
	if err != nil {
		return fmt.Errorf("acceptance remains uncertain: %w", err)
	}
	state := resumableUploadState{UploadID: upload.ID, AppSlug: s.record.AppSlug, TotalSize: upload.TotalSize, ChunkSize: upload.ChunkSize}
	if err := validateDiscoveredUpload(state, remote, upload.TotalSize); err != nil {
		return err
	}
	if remote.Status != "committed" && remote.Status != "complete" && remote.Status != "completed" {
		return fmt.Errorf("acceptance remains uncertain (upload status %q); resume did not submit another build", remote.Status)
	}
	if remote.DeploymentID == nil || *remote.DeploymentID == "" {
		return errors.New("committed upload has no deployment ID; acceptance cannot be reconnected")
	}
	if err := s.bindCommittedCronUpload(ctx, c, upload.ID, *remote.DeploymentID); err != nil {
		return err
	}
	s.record.Outcome, s.record.DeploymentID = deploySubmissionAccepted, *remote.DeploymentID
	return s.save()
}

func (s *savedDeploySession) confirmSubmissionReceipt(ctx context.Context, c *Client) error {
	receipt, err := c.GetDeploymentSubmission(ctx, s.record.AppSlug, s.record.AttemptID)
	if err != nil {
		return fmt.Errorf("acceptance remains uncertain; no submission receipt could be confirmed: %w", err)
	}
	return s.rememberSubmissionReceipt(receipt)
}
func (s *savedDeploySession) rememberSubmissionReceipt(receipt api.DeploymentSubmissionReceipt) error {
	if receipt.AttemptID != s.record.AttemptID || receipt.AppID == "" || receipt.DeploymentID == "" ||
		(s.record.AppID != "" && receipt.AppID != s.record.AppID) || receipt.AcceptedAt.IsZero() {
		return errors.New("submission receipt does not match the saved deployment attempt; acceptance remains uncertain")
	}
	s.record.Outcome, s.record.DeploymentID, s.record.AppID = deploySubmissionAccepted, receipt.DeploymentID, receipt.AppID
	return s.save()
}

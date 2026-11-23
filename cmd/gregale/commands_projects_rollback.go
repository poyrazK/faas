// adr: 20261103172200
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const projectsRollbackUsage = "usage: gregale project rollback <slug> (--dry-run | --yes | --targets | --protect-deploy <uuid> [--ttl 168h] | --history | --status <operation-id> | --preview-resume <operation-id> | --resume <operation-id> | --pause <operation-id> | --preview-undo <operation-id> | --undo <operation-id>) [--all | --to-deploy <uuid>] [--verify [--verify-before-cutover] [--stabilize-for 30s] [--verify-live-traffic [--undo-on-live-traffic-failure]] [--undo-on-health-failure]] [--acknowledge-releases] [--plan-hash <sha256>] [--request-id <uuid>] [--timeout 10m]"

func cmdProjectsRollback(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run", "yes", "targets", "history", "all", "acknowledge-releases", "verify", "verify-before-cutover", "verify-live-traffic", "undo-on-health-failure", "undo-on-live-traffic-failure")
	fs := newFlagSet("projects-rollback", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "preview the exact rollback")
	yes := fs.Bool("yes", false, "start a project rollback")
	history := fs.Bool("history", false, "list recent project rollbacks and undos")
	targets := fs.Bool("targets", false, "list recorded deploys eligible for rollback preview")
	var protectDeploy string
	fs.Func("protect-deploy", "retain a recorded deploy's rollback artifacts", func(value string) error {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return errors.New("pass a non-zero deploy UUID from --targets")
		}
		protectDeploy = id.String()
		return nil
	})
	var ttlSet bool
	ttlSeconds := api.ProjectRollbackProtectionMaxSeconds
	fs.Func("ttl", "rollback artifact protection duration (default 168h)", func(value string) error {
		ttl, err := time.ParseDuration(value)
		if err != nil || ttl%time.Second != 0 || ttl < time.Second || ttl > time.Duration(api.ProjectRollbackProtectionMaxSeconds)*time.Second {
			return errors.New("pass a duration in whole seconds between 1s and 168h")
		}
		ttlSet, ttlSeconds = true, int(ttl/time.Second)
		return nil
	})
	status := fs.String("status", "", "read a server rollback receipt")
	previewResume := fs.String("preview-resume", "", "preview the remaining steps of a paused rollback or undo")
	resume := fs.String("resume", "", "resume or follow a saved project rollback")
	pause := fs.String("pause", "", "pause a running project rollback or undo")
	previewUndo := fs.String("preview-undo", "", "preview the exact undo of a paused rollback")
	undo := fs.String("undo", "", "restore the original revisions from a paused rollback")
	all := fs.Bool("all", false, "select each serving service and its current predecessor")
	var toDeploy string
	fs.Func("to-deploy", "restore the revisions recorded for a project deploy", func(value string) error {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return errors.New("pass a non-zero deploy UUID from --targets")
		}
		toDeploy = id.String()
		return nil
	})
	requestID := fs.String("request-id", "", "reuse a rollback start request UUID")
	var planHash string
	fs.Func("plan-hash", "apply the plan printed by --dry-run, --preview-resume, or --preview-undo", func(value string) error {
		if !api.ValidProjectRollbackPlanHash(value) {
			return errors.New("pass the 64-character lowercase hexadecimal plan hash from --dry-run, --preview-resume, or --preview-undo")
		}
		planHash = value
		return nil
	})
	verify := fs.Bool("verify", false, "verify configured HTTP health probes between cutovers")
	verifyBeforeCutover := fs.Bool("verify-before-cutover", false, "probe prepared revisions before moving project traffic")
	var stabilizeSeconds int
	fs.Func("stabilize-for", "require healthy post-cutover probes for this duration", func(value string) error {
		duration, err := time.ParseDuration(value)
		if err != nil || duration%time.Second != 0 || duration < time.Second || duration > api.ProjectRollbackMaxStabilizeSeconds*time.Second {
			return errors.New("pass a duration in whole seconds between 1s and 60s")
		}
		stabilizeSeconds = int(duration / time.Second)
		return nil
	})
	verifyLiveTraffic := fs.Bool("verify-live-traffic", false, "verify configured production 5xx budgets after cutover")
	autoUndoLiveTraffic := fs.Bool("undo-on-live-traffic-failure", false, "undo switched services on a sustained production 5xx breach")
	autoUndo := fs.Bool("undo-on-health-failure", false, "undo switched services on a confirmed health probe 5xx")
	ack := fs.Bool("acknowledge-releases", false, "accept rolling back across release commands")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long the CLI waits for each service")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 {
		PrintUsage(os.Stderr, projectsRollbackUsage, "projects")
		return 1
	}
	slug := positional[0]
	modes := 0
	for _, mode := range []bool{*dryRun, *yes, *targets, protectDeploy != "", *history, *status != "", *resume != "", *previewResume != "", *pause != "", *undo != "", *previewUndo != ""} {
		if mode {
			modes++
		}
	}
	planningRollback := *dryRun || *yes
	if modes != 1 || (ttlSet && protectDeploy == "") || (*requestID != "" && !*yes) || (planHash != "" && !*yes && *undo == "" && *resume == "") ||
		(*autoUndoLiveTraffic && !*verifyLiveTraffic) ||
		((*verifyBeforeCutover || stabilizeSeconds > 0 || *verifyLiveTraffic || *autoUndo) && (!*verify || !planningRollback)) ||
		(toDeploy != "" && (*all || !planningRollback)) ||
		((*targets || protectDeploy != "" || *history || *status != "" || *resume != "" || *previewResume != "" || *pause != "") && (*all || *ack || *verify)) ||
		((*undo != "" || *previewUndo != "") && (*all || *verify)) {
		PrintUsage(os.Stderr, projectsRollbackUsage, "projects")
		return 1
	}
	if !api.ValidProjectSlug(slug) || *timeout <= 0 {
		return printErr("Invalid project rollback", errors.New("pass a valid project slug and a positive timeout"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *history {
		return showProjectRollbackHistory(ctx, client, slug)
	}
	if *targets {
		return showProjectRollbackTargets(ctx, client, slug)
	}
	if protectDeploy != "" {
		return protectProjectRollbackDeploy(ctx, client, slug, protectDeploy, ttlSeconds)
	}

	req := api.ProjectRollbackRequest{
		All: *all, AcknowledgeReleases: *ack, Verify: *verify, ToDeploy: toDeploy,
		VerifyBeforeCutover: *verifyBeforeCutover, StabilizeForSeconds: stabilizeSeconds,
		VerifyLiveTraffic: *verifyLiveTraffic, UndoOnHealthFailure: *autoUndo, UndoOnLiveTrafficFailure: *autoUndoLiveTraffic,
	}
	var op api.ProjectRollbackOperation
	var expectedID, undoOf string
	switch {
	case *dryRun:
		op, err = client.PreviewProjectRollback(ctx, slug, req)
	case *status != "":
		expectedID = *status
		op, err = client.GetProjectRollback(ctx, slug, expectedID)
	case *pause != "":
		id, parseErr := uuid.Parse(*pause)
		if parseErr != nil || id == uuid.Nil {
			return printErr("Invalid project rollback", errors.New("pass a project rollback operation UUID to --pause"))
		}
		expectedID = id.String()
		op, err = client.PauseProjectRollback(ctx, slug, expectedID)
	case *undo != "" || *previewUndo != "":
		source, flagName := *undo, "--undo"
		if *previewUndo != "" {
			source, flagName = *previewUndo, "--preview-undo"
		}
		id, parseErr := uuid.Parse(source)
		if parseErr != nil || id == uuid.Nil {
			return printErr("Invalid project rollback", fmt.Errorf("pass a project rollback operation UUID to %s", flagName))
		}
		undoOf = id.String()
		if *previewUndo != "" {
			op, err = client.PreviewProjectRollbackUndo(ctx, slug, undoOf, *ack)
		} else {
			op, err = client.UndoProjectRollback(ctx, slug, undoOf, api.ProjectRollbackUndoRequest{AcknowledgeReleases: *ack, ExpectedPlanHash: planHash})
		}
	case *previewResume != "":
		id, parseErr := uuid.Parse(*previewResume)
		if parseErr != nil || id == uuid.Nil {
			return printErr("Invalid project rollback", errors.New("pass a project rollback operation UUID to --preview-resume"))
		}
		expectedID = id.String()
		op, err = client.PreviewProjectRollbackResume(ctx, slug, expectedID)
	case *resume != "":
		expectedID = *resume
		if planHash != "" {
			id, parseErr := uuid.Parse(*resume)
			if parseErr != nil || id == uuid.Nil {
				return printErr("Invalid project rollback", errors.New("pass a project rollback operation UUID to --resume"))
			}
			expectedID = id.String()
			op, err = client.ResumeReviewedProjectRollback(ctx, slug, expectedID, api.ProjectRollbackResumeRequest{ExpectedPlanHash: planHash})
			break
		}
		op, err = client.ResumeProjectRollback(ctx, slug, expectedID)
		var problem *api.APIError
		if errors.As(err, &problem) && problem.Problem.Status == http.StatusNotFound {
			if path, pathErr := projectRollbackSessionPath(client, expectedID); pathErr == nil {
				if _, statErr := os.Stat(path); statErr == nil {
					return resumeProjectRollback(ctx, client, slug, expectedID, *timeout)
				}
			}
		}
	default:
		id := uuid.New()
		if *requestID != "" {
			parsed, parseErr := uuid.Parse(*requestID)
			if parseErr != nil || parsed == uuid.Nil {
				return printErr("Invalid project rollback", errors.New("pass a non-zero UUID to --request-id"))
			}
			id = parsed
		}
		req.RequestID = id.String()
		req.ExpectedPlanHash = planHash
		progress := osStdout
		if jsonOutput {
			progress = osStderr
		}
		PrintProgress(progress, "Project rollback request ID %s", req.RequestID)
		op, err = client.StartProjectRollback(api.ContextWithIdempotencyKey(ctx, req.RequestID), slug, req)
	}
	if err != nil {
		return printErr("Could not read project rollback", err)
	}
	if op.Project != slug || op.ProjectID == "" || expectedID != "" && op.ID != expectedID || op.Status == "running" && op.ID == "" {
		return printErr("Invalid project rollback receipt", errors.New("server returned a different operation or project"))
	}
	if req.RequestID != "" && op.ID != "" && op.RequestID != req.RequestID {
		return printErr("Invalid project rollback receipt", errors.New("server returned a different rollback start request"))
	}
	if toDeploy != "" && op.DeployGroupID != toDeploy {
		return printErr("Invalid project rollback receipt", errors.New("server returned a different project deploy"))
	}
	confirmedHash := op.PlanHash
	if *resume != "" {
		confirmedHash = op.ResumePlanHash
	}
	if planHash != "" && (op.ID == "" || confirmedHash != planHash) {
		return printErr("Invalid project rollback receipt", errors.New("server did not confirm the reviewed rollback plan"))
	}
	if *previewResume != "" {
		planned := op.Status == "planned" && api.ValidProjectRollbackPlanHash(op.ResumePlanHash)
		blocked := op.Status == "blocked" && op.ResumePlanHash == ""
		if !planned && !blocked {
			return printErr("Invalid project rollback preview", errors.New("server returned an invalid rollback resume plan"))
		}
	}
	if *previewUndo != "" {
		planned := op.Status == "planned" && api.ValidProjectRollbackPlanHash(op.PlanHash)
		blocked := op.Status == "blocked" && op.PlanHash == ""
		if op.UndoOf != undoOf || op.ID != "" || (!planned && !blocked) {
			return printErr("Invalid project rollback preview", errors.New("server returned an invalid rollback undo plan"))
		}
	} else if undoOf != "" && (op.UndoOf != undoOf || op.ID == "" || op.ID == undoOf) {
		return printErr("Invalid project rollback receipt", errors.New("server returned a different rollback undo"))
	}
	if *verify && !op.Verify {
		return printErr("Invalid project rollback receipt", errors.New("server did not confirm health verification"))
	}
	if *verifyBeforeCutover && !op.VerifyBeforeCutover {
		return printErr("Invalid project rollback receipt", errors.New("server did not confirm pre-cutover health verification"))
	}
	if stabilizeSeconds > 0 && op.StabilizeForSeconds != stabilizeSeconds {
		return printErr("Invalid project rollback receipt", errors.New("server did not confirm the health stabilization duration"))
	}
	if *verifyLiveTraffic && !op.VerifyLiveTraffic {
		return printErr("Invalid project rollback receipt", errors.New("server did not confirm live-traffic verification"))
	}
	if *autoUndoLiveTraffic && !op.UndoOnLiveTrafficFailure {
		return printErr("Invalid project rollback receipt", errors.New("server did not confirm automatic undo on live-traffic failure"))
	}
	if *autoUndo && !op.UndoOnHealthFailure {
		return printErr("Invalid project rollback receipt", errors.New("server did not confirm automatic undo on health failure"))
	}
	if *status != "" {
		return renderProjectRollback(projectRollbackSession{ProjectRollbackOperation: op}, false, nil)
	}
	if op.Status == "running" {
		progress := osStdout
		if jsonOutput {
			progress = osStderr
		}
		if *pause != "" {
			PrintProgress(progress, "Pause requested for project rollback %s. Inspect with gregale project rollback %s --status %s", op.ID, slug, op.ID)
		} else if op.UndoOf != "" {
			PrintProgress(progress, "Project rollback undo %s accepted. Inspect with gregale project rollback %s --status %s", op.ID, slug, op.ID)
		} else {
			PrintProgress(progress, "Project rollback %s accepted. Inspect with gregale project rollback %s --status %s", op.ID, slug, op.ID)
		}
		op, err = waitProjectRollback(ctx, client, op, *timeout, 2*time.Second)
	}
	if err == nil && op.Status == "blocked" && *pause == "" {
		if op.AutoUndo != nil && op.AutoUndo.Status == api.ProjectRollbackAutoUndoStarted {
			if op.AutoUndo.Trigger == api.ProjectRollbackAutoUndoLiveTrafficTrigger {
				err = errors.New("project rollback failed live-traffic verification and started automatic undo")
			} else {
				err = errors.New("project rollback failed health verification and started automatic undo")
			}
		} else if op.PauseRequestedAt != nil {
			err = errors.New("project rollback was paused")
		} else {
			err = errors.New("project rollback has a service blocker")
		}
	}
	return renderProjectRollback(projectRollbackSession{ProjectRollbackOperation: op}, *dryRun || *previewUndo != "" || *previewResume != "", err)
}

func waitProjectRollback(ctx context.Context, client *api.Client, op api.ProjectRollbackOperation, timeout, interval time.Duration) (api.ProjectRollbackOperation, error) {
	deadline := time.Now().Add(timeout)
	index := projectRollbackCompletedSteps(op)
	for op.Status == "running" {
		callCtx, cancel := context.WithDeadline(ctx, deadline)
		select {
		case <-callCtx.Done():
			err := callCtx.Err()
			cancel()
			return op, fmt.Errorf("stopped waiting for the server: %w", err)
		case <-time.After(interval):
		}
		next, err := client.GetProjectRollback(callCtx, op.Project, op.ID)
		cancel()
		if err != nil {
			if api.TransientReadError(err) && ctx.Err() == nil && time.Now().Before(deadline) {
				continue
			}
			return op, err
		}
		if next.ID != op.ID || next.ProjectID != op.ProjectID || next.Project != op.Project ||
			next.UndoOf != op.UndoOf || next.PlanHash != op.PlanHash {
			return op, errors.New("server returned a different project rollback receipt")
		}
		if next.Status != "running" && next.Status != "blocked" && next.Status != "complete" {
			return op, errors.New("server returned an unknown project rollback state")
		}
		if completed := projectRollbackCompletedSteps(next); completed > index {
			index, deadline = completed, time.Now().Add(timeout)
		}
		op = next
	}
	return op, nil
}

func projectRollbackCompletedSteps(op api.ProjectRollbackOperation) int {
	count := 0
	for _, step := range op.Steps {
		if step.Status == "rolled_back" {
			count++
		}
	}
	return count
}

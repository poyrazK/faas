package deploystaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type SafeProjectReleaseStore interface {
	state.ProjectSafeReleaseStore
	state.ProjectSubmissionStore
	DeploymentByID(context.Context, string) (state.Deployment, error)
}

// Artifact validation is the same preflight required by manual publication and
// environment promotion. Failed or expired batches can terminate without it;
// a ready batch must pass before the transactional graph switch.
func ReconcileSafeProjectReleases(ctx context.Context, store SafeProjectReleaseStore, check func(context.Context, []state.ProjectReleaseMember) error, probes ...func(context.Context, api.ProjectSubmissionReceipt, state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error)) error {
	jobs, err := store.ListPendingSafeProjectSubmissions(ctx)
	if err != nil {
		return err
	}
	var firstError error
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		receipt, err := store.ProjectSubmission(ctx, job.AccountID, job.AttemptID)
		if err == nil && receipt.Apply.SafeRelease != nil && (receipt.Apply.SafeRelease.Status == "verifying" || receipt.Apply.SafeRelease.Status == "rolling_back" || api.ProjectReleaseMonitoring(receipt.Apply.SafeRelease)) {
			err = reconcileProjectVerification(ctx, store, job, receipt, check, probes)
			if err != nil && firstError == nil {
				firstError = fmt.Errorf("verify project attempt %s: %w", job.AttemptID, err)
			}
			continue
		}
		// Publication requires the preflight to cover the exact graph the store
		// activates; an unchecked graph is left building for the next pass.
		var prechecked []state.ProjectReleaseMember
		if err == nil && receipt.Apply.SafeRelease != nil && receipt.Status == "complete" && time.Now().Before(receipt.Apply.SafeRelease.Deadline) {
			deployments := map[string]state.Deployment{}
			for _, build := range receipt.Apply.Builds {
				if build.DeploymentID == "" {
					continue
				}
				deployment, readErr := store.DeploymentByID(ctx, build.DeploymentID)
				if readErr != nil {
					err = readErr
					break
				}
				deployments[build.DeploymentID] = deployment
			}
			if err == nil {
				members, status, _ := state.ProjectSafeReleaseCandidates(receipt, deployments)
				if status == "activated" {
					err = check(ctx, members)
					if err == nil {
						prechecked = members
					}
					if err == nil && receipt.Apply.SafeRelease.ReleaseCommand != nil && receipt.Apply.SafeRelease.ReleaseCommand.VerifyBaseline {
						err = reconcileMigrationBaseline(ctx, store, job, receipt, check, probes)
					}
				}
			}
		}
		if err == nil {
			_, err = store.ReconcileSafeProjectSubmission(state.ContextWithPrecheckedProjectRelease(ctx, prechecked), job.AccountID, job.AttemptID)
		}
		if err != nil && firstError == nil {
			firstError = fmt.Errorf("safe project attempt %s: %w", job.AttemptID, err)
		}
	}
	return firstError
}

func reconcileProjectVerification(ctx context.Context, store SafeProjectReleaseStore, job state.SafeProjectSubmission, receipt api.ProjectSubmissionReceipt, check func(context.Context, []state.ProjectReleaseMember) error, probes []func(context.Context, api.ProjectSubmissionReceipt, state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error)) error {
	worker, ok := store.(state.ProjectReleaseVerificationStore)
	if !ok {
		return fmt.Errorf("verification store unavailable")
	}
	safe := receipt.Apply.SafeRelease
	// Always check the active graph before external probes or baseline preflight.
	// The store repeats this under the project lock before checkpointing.
	reader, ok := store.(state.ProjectReleaseSetReader)
	if !ok {
		return fmt.Errorf("release reader unavailable")
	}
	active, err := reader.ActiveProjectReleaseSet(ctx, job.AccountID, receipt.Apply.ProjectID, "production")
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	expectedActive := safe.ReleaseID
	if safe.Verification != nil && safe.Verification.Progressive && safe.Verification.Percent < api.ProjectCanaryFinalPercent {
		expectedActive = safe.PreviousReleaseID
	}
	if active.ID != expectedActive {
		_, err := worker.AdvanceProjectReleaseVerification(ctx, job.AccountID, job.AttemptID, nil)
		return err
	}
	if (safe.Status == "verifying" || api.ProjectReleaseMonitoring(safe)) && (safe.Verification == nil || safe.Verification.Deadline == nil) {
		return fmt.Errorf("verification checkpoint incomplete")
	}
	if (safe.Status == "verifying" || api.ProjectReleaseMonitoring(safe)) && !safe.Verification.Progressive && !time.Now().Before(*safe.Verification.Deadline) {
		_, err := worker.AdvanceProjectReleaseVerification(ctx, job.AccountID, job.AttemptID, nil)
		return err
	}
	if (safe.Status == "verifying" || api.ProjectReleaseMonitoring(safe)) && safe.Verification.CheckedAt != nil && time.Since(*safe.Verification.CheckedAt) < api.ProjectReleaseVerificationInterval {
		return nil
	}
	baseline, err := reader.ProjectReleaseSetByID(ctx, job.AccountID, receipt.Apply.ProjectID, "production", safe.PreviousReleaseID)
	if errors.Is(err, state.ErrNotFound) && (safe.Status == "rolling_back" || api.ProjectReleaseMonitoring(safe)) {
		_, err = worker.AdvanceProjectReleaseVerification(ctx, job.AccountID, job.AttemptID, nil)
		return err
	}
	if err != nil {
		return err
	}
	if safe.Verification != nil && safe.Verification.Progressive {
		if err := check(ctx, baseline.Members); err != nil {
			if api.ProjectReleaseMonitoring(safe) || safe.Verification.Control != nil && safe.Verification.Control.Status == "pending" {
				_, advanceErr := worker.AdvanceProjectReleaseVerification(ctx, job.AccountID, job.AttemptID, nil)
				if advanceErr != nil {
					return advanceErr
				}
			}
			return err
		}
	}
	if safe.Status == "rolling_back" {
		if baseline.ExpiresAt != nil && time.Now().Before(*baseline.ExpiresAt) {
			if err := check(ctx, baseline.Members); err != nil {
				return err
			}
		}
		_, err := worker.AdvanceProjectReleaseVerification(ctx, job.AccountID, job.AttemptID, nil)
		return err
	}
	round := &state.ProjectReleaseVerificationRound{PreviousCheckedAt: safe.Verification.CheckedAt, Generation: safe.Verification.Generation}
	if len(probes) > 0 && probes[0] != nil {
		round.Evidence, err = probes[0](ctx, receipt, baseline)
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		// Delivery or infrastructure failures yield unknown evidence. They cannot
		// count as health and the durable deadline still drives recovery.
		if err != nil {
			round.Evidence = nil
		}
	}
	_, err = worker.AdvanceProjectReleaseVerification(ctx, job.AccountID, job.AttemptID, round)
	return err
}

// A successful expansion is necessary but insufficient for publication: the
// still-active production graph must pass fresh attributed critical probes.
func reconcileMigrationBaseline(ctx context.Context, store SafeProjectReleaseStore, job state.SafeProjectSubmission, receipt api.ProjectSubmissionReceipt, check func(context.Context, []state.ProjectReleaseMember) error, probes []func(context.Context, api.ProjectSubmissionReceipt, state.ProjectReleaseSet) ([]api.ProjectReleaseProbeEvidence, error)) error {
	tasks, ok := store.(interface {
		ReleaseAppTaskByDeployment(context.Context, string) (state.AppTask, error)
	})
	if !ok {
		return fmt.Errorf("migration task reader unavailable")
	}
	command := receipt.Apply.SafeRelease.ReleaseCommand
	var task state.AppTask
	for _, build := range receipt.Apply.Builds {
		if build.AppID != command.AppID || build.DeploymentID == "" {
			continue
		}
		var err error
		task, err = tasks.ReleaseAppTaskByDeployment(ctx, build.DeploymentID)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	if task.Status != state.AppTaskSucceeded || task.ExitCode == nil || *task.ExitCode != 0 || task.FinishedAt == nil {
		return nil
	}
	if proof := command.BaselineVerification; proof != nil && proof.TaskID == task.ID && (proof.Status == "failed" || time.Since(proof.CheckedAt) < api.ProjectReleaseVerificationInterval) {
		return nil
	}
	reader, ok := store.(state.ProjectReleaseSetReader)
	if !ok {
		return fmt.Errorf("migration baseline reader unavailable")
	}
	active, err := reader.ActiveProjectReleaseSet(ctx, job.AccountID, receipt.Apply.ProjectID, "production")
	if err != nil {
		return err
	}
	if active.ID != receipt.Apply.SafeRelease.PreviousReleaseID {
		return nil
	}
	recorder, ok := store.(state.ProjectMigrationBaselineStore)
	if !ok {
		return fmt.Errorf("migration baseline recorder unavailable")
	}
	round := state.ProjectMigrationBaselineRound{TaskID: task.ID, StartedAt: time.Now().UTC()}
	if err = check(ctx, active.Members); err == nil && len(probes) > 0 && probes[0] != nil {
		round.Evidence, err = probes[0](ctx, receipt, active)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		round.Evidence = nil
	}
	_, err = recorder.RecordProjectMigrationBaseline(ctx, job.AccountID, job.AttemptID, round)
	return err
}

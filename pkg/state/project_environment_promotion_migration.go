package state

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectPromotionMigrationStore interface {
	ReconcileProjectEnvironmentPromotionMigration(context.Context, string, string) (*api.ProjectPromotionHealth, error)
}

type promotionMigrationCapture struct {
	TaskID             string
	RuntimeFingerprint string
	PreparedLeaseToken string
}

func (c *promotionReleaseConfiguration) prepareMigration(migrate bool) error {
	if !migrate {
		return nil
	}
	for _, w := range c.Workloads {
		if len(w.Source.ReleaseCommand) == 0 {
			continue
		}
		if c.Migration != nil || w.Source.RootfsKey == "" || w.Source.ImageDigest == "" || validateDeploymentReleaseCommand(w.Source.ReleaseCommand, w.Source.ReleaseCommandShell) != nil {
			return ErrReleaseConfigurationTransferUnsupported
		}
		c.Migration = &api.ProjectPromotionMigrationPolicy{AppID: w.AppID, Slug: w.Slug, CommandHash: api.ProjectReleaseCommandHash(w.Source.ReleaseCommand, w.Source.ReleaseCommandShell), Compatibility: "backward-compatible"}
	}
	if c.Migration == nil {
		return ErrReleaseConfigurationTransferUnsupported
	}
	return nil
}

func promotionMigrationProgress(policy *api.ProjectPromotionMigrationPolicy) *api.ProjectReleaseCommand {
	return &api.ProjectReleaseCommand{AppID: policy.AppID, Slug: policy.Slug, CommandHash: policy.CommandHash, Compatibility: policy.Compatibility, Status: "waiting", VerifyBaseline: true}
}

func validatePromotionMigrationAdmission(p ProjectEnvironmentPromotion, c *promotionReleaseConfiguration) error {
	if c == nil || c.Migration == nil {
		if p.Migrate || p.HealthPolicy != nil && p.HealthPolicy.Migration != nil {
			return ErrInvalidArgument
		}
		return nil
	}
	if !p.Migrate || p.HealthPolicy == nil || p.HealthPolicy.Migration == nil || *p.HealthPolicy.Migration != *c.Migration {
		return ErrInvalidArgument
	}
	return nil
}

// Reuse the existing release-task ownership and immutable exit-receipt checks.
func promotionMigrationOutcome(accountID string, h *api.ProjectPromotionHealth, d Deployment, task *AppTask, now time.Time) *api.ProjectPromotionHealth {
	next := clonePromotionHealth(h)
	r := api.ProjectSubmissionReceipt{Apply: api.ApplyResponse{PlanResponse: api.PlanResponse{SafeRelease: &api.ProjectSafeRelease{ReleaseCommand: next.Migration, Verification: &api.ProjectReleaseVerification{}}}, Builds: []api.AppliedBuild{{AppID: d.AppID, DeploymentID: d.ID}}}}
	tasks := map[string]AppTask{}
	if task != nil {
		tasks[d.ID] = *task
	}
	status, message := projectReleaseTaskOutcome(accountID, r, map[string]Deployment{d.ID: d}, tasks)
	message = strings.NewReplacer("production", "destination", "Production", "Destination", "new deploy", "new promotion").Replace(message)
	switch status {
	case "":
		next.Phase, next.StartedAt, next.Deadline = "preflight", now, now.Add(api.ProjectReleaseVerificationTimeout)
		next.Message = "Destination migration succeeded; verifying the previous release and candidate graph."
	case "failed", "conflicted":
		next.Phase, next.Message = "failed", message
	}
	if !now.Before(h.Deadline) {
		next.Phase, next.Message = "failed", "Migration deadline expired; the destination release was not switched. Inspect the task and database before another attempt."
		if task != nil && task.StartedAt != nil && task.ExitCode == nil {
			next.Migration.Status = "uncertain"
		}
	}
	if (task == nil || task.StartedAt == nil && !task.Status.Terminal()) && !now.Before(h.StartedAt.Add(api.ProjectPromotionMigrationStartTimeout)) {
		next.Phase, next.Message = "failed", "Migration did not start inside its admission window; the destination release was not switched."
	}
	if !reflect.DeepEqual(h, next) {
		next.Generation++
	}
	return next
}

func promotionMigrationTaskParams(p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, d Deployment) (CreateAppTaskParams, error) {
	deadline := h.StartedAt.Add(api.ProjectPromotionMigrationStartTimeout)
	return resolveCreateAppTask(CreateAppTaskParams{AccountID: p.AccountID, AppID: d.AppID, DeploymentID: d.ID, Kind: AppTaskKindRelease,
		Command: append([]string(nil), d.ReleaseCommand...), CommandShell: d.ReleaseCommandShell, StartDeadlineAt: &deadline})
}

func verifyPromotionMigrationTask(accountID string, h *api.ProjectPromotionHealth, d Deployment, task AppTask, pin promotionMigrationCapture) error {
	if h.Policy.Migration == nil || pin.TaskID == "" || task.ID != pin.TaskID || pin.PreparedLeaseToken == "" || task.Kind != AppTaskKindRelease || task.AccountID != accountID || task.Status != AppTaskSucceeded || task.ExitCode == nil || *task.ExitCode != 0 || task.AppID != h.Policy.Migration.AppID || task.DeploymentID != d.ID || task.ArtifactKey != d.RootfsKey || task.ImageDigest != d.ImageDigest || task.DeploymentScope != d.Scope || api.ProjectReleaseCommandHash(task.Command, task.CommandShell) != h.Policy.Migration.CommandHash {
		return fmt.Errorf("promotion migration lacks owned, runtime-attested success: %w", ErrConflict)
	}
	return nil
}

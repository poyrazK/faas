package taskrehearsal

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type ExecutionStore interface {
	BuildByID(context.Context, string) (state.Build, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	ReleaseAppTaskByDeployment(context.Context, string) (state.AppTask, error)
	CancelDeploymentTx(context.Context, string, string, state.CancelReason) (state.Deployment, []string, error)
}

// BuildInput is private. The submitter uses the shared source pipeline.
type BuildInput struct {
	OperationID string
	Account     state.Account
	App         state.App
	Archive     []byte
	Migration   bool
}

type Runner struct {
	AdmissionEnabled bool
	Store            ExecutionStore
	Submit           func(context.Context, BuildInput) (apidsource.EnqueueResult, error)
	RemoveSource     func(context.Context, string) error
}

// Execution contains only opaque resource identities. This stage establishes
// migration and build completion, which is not a runtime smoke test.
type Execution struct {
	MigrationDeploymentID string
	MigrationTaskID       string
	WebDeploymentID       string
	WorkerDeploymentID    string
}

var migrationCommand = []string{"node", "tasks-migrate.js", "apply"}

func MigrationCommand() []string { return slices.Clone(migrationCommand) }

func (r *Runner) Run(ctx context.Context, p *Provisioner, account state.Account, run state.HostedTaskRehearsalRun,
	web, worker []byte, policy tasktoolbinding.Policy) (Execution, error) {
	return r.run(ctx, p, account, run, web, worker, policy, nil)
}

func (r *Runner) run(ctx context.Context, p *Provisioner, account state.Account, run state.HostedTaskRehearsalRun,
	web, worker []byte, policy tasktoolbinding.Policy, observer *pgruntime.TaskObservation) (Execution, error) {
	if !r.AdmissionEnabled || p == nil || r.Store == nil || r.Submit == nil || r.RemoveSource == nil || len(web) == 0 || len(worker) == 0 {
		return Execution{}, state.ErrInvalidArgument
	}
	env, err := newRunnerEnvironment("rehearsal-"+run.ID, policy)
	if err != nil {
		return Execution{}, err
	}
	target, err := p.provisionRun(ctx, account, run, env)
	if err != nil {
		return Execution{}, err
	}
	execute, stop := context.WithDeadline(ctx, target.expiresAt)
	defer stop()
	result, err := r.execute(execute, target, env, web, worker, observer)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if e := target.cleanup(cleanup); e != nil {
			err = errors.Join(err, e)
		}
		err = errors.Join(err, r.RemoveRunSources(cleanup, run.ID))
	}
	return result, err
}

func (r *Runner) execute(ctx context.Context, p *Prepared, env *runnerEnvironment, web, worker []byte, observer *pgruntime.TaskObservation) (Execution, error) {
	var result Execution
	migration, err := r.submit(ctx, p, Migration, worker)
	if err != nil {
		return result, err
	}
	migrationDigest := toolpublication.Digest(worker)
	if err := waitFor(ctx, func(ctx context.Context) (bool, error) {
		dep, err := r.deployment(ctx, p, Migration, migrationDigest, migration)
		if err != nil {
			return false, err
		}
		task, err := r.Store.ReleaseAppTaskByDeployment(ctx, dep.ID)
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		done, err := migrationCompleted(p, dep, task)
		if done {
			result.MigrationTaskID = task.ID
		}
		return done, err
	}); err != nil {
		return Execution{}, fmt.Errorf("wait for contained Task migration: %w", err)
	}
	result.MigrationDeploymentID = migration.DeploymentID
	if _, _, err := r.Store.CancelDeploymentTx(ctx, migration.DeploymentID, p.account.ID, state.CancelReasonSystem); err != nil {
		return Execution{}, fmt.Errorf("retire rehearsal migration deployment: %w", err)
	}
	if err := p.removeCredentials(ctx, p.AppID(Migration)); err != nil {
		return Execution{}, fmt.Errorf("remove rehearsal migration credentials: %w", err)
	}
	if err := r.RemoveSource(ctx, migration.BuildID); err != nil {
		return Execution{}, fmt.Errorf("retire rehearsal migration source: %w", err)
	}
	if err := p.bindRuntime(ctx, env, observer); err != nil {
		return Execution{}, err
	}
	webBuild, err := r.submit(ctx, p, Web, web)
	if err != nil {
		return Execution{}, err
	}
	workerBuild, err := r.submit(ctx, p, Worker, worker)
	if err != nil {
		return Execution{}, err
	}
	if err := r.finishBuild(ctx, p, Web, web, webBuild); err != nil {
		return Execution{}, err
	}
	if err := r.finishBuild(ctx, p, Worker, worker, workerBuild); err != nil {
		return Execution{}, err
	}
	result.WebDeploymentID, result.WorkerDeploymentID = webBuild.DeploymentID, workerBuild.DeploymentID
	return result, nil
}

func (r *Runner) submit(ctx context.Context, p *Prepared, role string, archive []byte) (apidsource.EnqueueResult, error) {
	var zero apidsource.EnqueueResult
	app, err := p.provisioner.Store.AppByID(ctx, p.AppID(role))
	if err != nil {
		return zero, err
	}
	member, err := p.provisioner.Store.ScenarioTestMemberByApp(ctx, app.ID)
	if err != nil {
		return zero, err
	}
	if !p.owns(app, role) || app.Status == state.AppDeleted || app.PreviewPrState != state.PreviewPrStateOpen ||
		app.PreviewExpiresAt == nil || !app.PreviewExpiresAt.Equal(p.expiresAt) ||
		member.AccountID != p.account.ID || member.RunID != p.scenarioID() || member.Workload != role ||
		member.NetworkMode != state.ScenarioNetworkContained || member.WebhookMode != state.ScenarioWebhookCaptureMode {
		return zero, state.ErrConflict
	}
	op := buildOperation(p.run.String())
	got, err := r.Submit(ctx, BuildInput{OperationID: op, Account: p.account, App: app, Archive: archive, Migration: role == Migration})
	if err != nil {
		return zero, fmt.Errorf("queue rehearsal %s build: %w", role, err)
	}
	deploymentID, buildID := apidsource.OperationIDs(op, app.ID)
	if got.DeploymentID != deploymentID || got.BuildID != buildID {
		return zero, state.ErrConflict
	}
	return got, nil
}

func (r *Runner) deployment(ctx context.Context, p *Prepared, role, digest string, result apidsource.EnqueueResult) (state.Deployment, error) {
	d, err := r.Store.DeploymentByID(ctx, result.DeploymentID)
	if err != nil {
		return d, err
	}
	var command []string
	if role == Migration {
		command = migrationCommand
	}
	if d.ID != result.DeploymentID || d.AppID != p.AppID(role) || d.Kind != state.DeploymentKindTarball ||
		d.SourceSHA256 != digest || d.Scope != api.DefaultEnvScope ||
		!d.TrafficPercentExplicit || d.TrafficPercent != 0 || d.ReleaseCommandShell || !slices.Equal(d.ReleaseCommand, command) ||
		d.Status.IsTerminal() {
		return d, state.ErrConflict
	}
	return d, nil
}

func migrationCompleted(p *Prepared, d state.Deployment, task state.AppTask) (bool, error) {
	spec, err := rehearsalcommand.Decode(task.Command)
	if err != nil || spec.RunID != p.scenarioID() || task.AccountID != p.account.ID || task.DeploymentScope != d.Scope ||
		!state.ReleaseTaskRunsDeploymentRelease(task, d) || task.CommandShell ||
		task.ArtifactKey != d.RootfsKey || d.RootfsKey == "" || task.ImageDigest != d.ImageDigest || d.ImageDigest == "" {
		return false, state.ErrConflict
	}
	if !task.Status.Terminal() {
		return false, nil
	}
	if task.Status != state.AppTaskSucceeded || task.ExitCode == nil || *task.ExitCode != 0 || task.OutputTruncated {
		return false, state.ErrConflict
	}
	receipt, err := rehearsalcommand.ParseReceipt([]byte(task.StdoutTail), task.ID, spec)
	if err != nil || receipt.OutputTruncated {
		return false, state.ErrConflict
	}
	return true, nil
}

func (r *Runner) finishBuild(ctx context.Context, p *Prepared, role string, archive []byte, queued apidsource.EnqueueResult) error {
	digest := toolpublication.Digest(archive)
	if err := waitFor(ctx, func(ctx context.Context) (bool, error) {
		deployment, err := r.deployment(ctx, p, role, digest, queued)
		if err != nil {
			return false, err
		}
		build, err := r.Store.BuildByID(ctx, queued.BuildID)
		if err != nil {
			return false, err
		}
		if build.ID != queued.BuildID || build.DeploymentID != queued.DeploymentID {
			return false, state.ErrConflict
		}
		switch build.Status {
		case state.BuildQueued, state.BuildRunning:
			return false, nil
		case state.BuildSucceeded:
			return deployment.RootfsKey != "", nil
		default:
			return false, state.ErrConflict
		}
	}); err != nil {
		return fmt.Errorf("wait for rehearsal %s build: %w", role, err)
	}
	return r.RemoveSource(ctx, queued.BuildID)
}

func waitFor(ctx context.Context, inspect func(context.Context) (bool, error)) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := inspect(ctx)
		if done || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func buildOperation(runID string) string { return "hosted-task-rehearsal-v1:" + runID }

// RemoveRunSources rederives each build identity after a restart.
func (r *Runner) RemoveRunSources(ctx context.Context, runID string) error {
	run, err := uuid.Parse(runID)
	if err != nil || run == uuid.Nil || run.String() != runID || r.RemoveSource == nil {
		return state.ErrInvalidArgument
	}
	var errs []error
	for _, role := range []string{Migration, Web, Worker} {
		_, buildID := apidsource.OperationIDs(buildOperation(runID), tasktoolrehearsal.WorkloadID(run, role))
		if err := r.RemoveSource(ctx, buildID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CleanupExpiredApp is called by preview teardown, including after a restart.
// The native app ID and preview lease marker identify this run's resources.
func (r *Runner) CleanupExpiredApp(ctx context.Context, p *Provisioner, app state.App) error {
	marker, found := strings.CutPrefix(app.PreviewOfSlug, "task-")
	if !found || len(marker) != 32 || app.PreviewPrNumber != 0 || app.Visibility != api.AppVisibilityInternal {
		return nil
	}
	run, err := uuid.Parse(marker)
	if err == nil && run != uuid.Nil {
		target := Prepared{provisioner: p, account: state.Account{ID: app.AccountID}, run: run}
		for _, role := range workloads {
			if target.owns(app, role.name) {
				if role.name != Observer {
					_, buildID := apidsource.OperationIDs(buildOperation(run.String()), app.ID)
					if err := r.RemoveSource(ctx, buildID); err != nil {
						return err
					}
				}
				return target.removeCredentials(ctx, app.ID)
			}
		}
	}
	return nil
}

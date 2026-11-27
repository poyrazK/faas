package taskrehearsal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type startupStore interface {
	AppByID(context.Context, string) (state.App, error)
	ScenarioTestMemberByApp(context.Context, string) (state.ScenarioTestMember, error)
	InstanceByID(context.Context, string) (state.Instance, error)
	ListInstancesForApp(context.Context, string) ([]state.Instance, error)
}

// ClaimLogFrame carries the scheduler's attribution for one worker log line.
type ClaimLogFrame struct {
	InstanceID, DeploymentID, Stream, Line string
	WrittenAt                              time.Time
	IsGap                                  bool
}

type ClaimLogStream func() (ClaimLogFrame, error)

// NativeStartup is wired only by the private acceptance caller. Forward
// returns a transport bound to the supplied VM through the private gateway;
// it must never resolve a public route or fall back to another instance.
type NativeStartup struct {
	// SourceSHA256 comes from the independently prepared fixture trees.
	// The private executor compares it to both reviewed trees before claiming.
	SourceSHA256 StartupFixture
	NodeID       string

	Store         startupStore
	WakeWeb       func(ctx context.Context, appID, deploymentID, scope string) (instanceID, nodeID, resolvedDeploymentID, wakeID string, port int, err error)
	OpenClaimLogs func(ctx context.Context, appID, deploymentID string, since time.Time, event string) (ClaimLogStream, error)
	CheckHost     func(ctx context.Context, accountID, nodeID string) error
	Forward       func(state.Instance, int) http.RoundTripper
}

type StartupFixture struct {
	WebSHA256, WorkerSHA256 string
}

func (d *NativeStartup) Preflight(ctx context.Context, accountID string) error {
	if d == nil || uuid.Validate(d.NodeID) != nil || d.NodeID == uuid.Nil.String() || d.Store == nil || d.WakeWeb == nil || d.OpenClaimLogs == nil || d.CheckHost == nil || d.Forward == nil ||
		!toolpublication.ValidID(d.SourceSHA256.WebSHA256) || !toolpublication.ValidID(d.SourceSHA256.WorkerSHA256) {
		return state.ErrInvalidArgument
	}
	return d.CheckHost(ctx, accountID, d.NodeID)
}

// RunStartup builds the claimed fixture, observes its first lease and disposes
// of the run. Only the private owner entry can acquire the archived sources.
func (r *Runner) RunStartup(ctx context.Context, p *Provisioner, account state.Account, run state.HostedTaskRehearsalRun,
	web, worker []byte, policy tasktoolbinding.Policy, startup *NativeStartup) (StartupEvidence, error) {
	if startup == nil {
		return StartupEvidence{}, state.ErrInvalidArgument
	}
	var observe pgruntime.TaskObservation
	execution, err := r.run(ctx, p, account, run, web, worker, policy, &observe)
	if err != nil {
		return StartupEvidence{}, err
	}
	target, err := p.target(account, run.ID)
	if err != nil {
		return StartupEvidence{}, err
	}
	target.expiresAt = run.ReservedAt.Add(leaseDuration)
	bounded, cancel := context.WithDeadline(ctx, target.expiresAt)
	evidence, err := r.startAndObserve(bounded, target, target.smokeScope(run.PlanSHA256, execution), startup, observe.Claim)
	cancel()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer stop()
	return evidence, errors.Join(err, target.cleanup(cleanup))
}

// StartupEvidence proves the first live claim inside the reserved rehearsal.
// This private report has no recovery or native acceptance verdict.
type StartupEvidence struct {
	TaskID        string
	WebInstanceID string
	Initial       tasktoolrehearsal.WorkerClaim
}

const claimEvent = "gregale_hosted_task_claim"

func (r *Runner) startAndObserve(ctx context.Context, target *Prepared, scope tasktoolrehearsal.SmokeScope, d *NativeStartup, observe pgruntime.TaskClaimObserver) (StartupEvidence, error) {
	var evidence StartupEvidence
	if observe == nil {
		return evidence, state.ErrInvalidArgument
	}
	session, err := r.openStartupSession(ctx, target, scope, d)
	if err != nil {
		return evidence, err
	}
	defer session.close()
	evidence.WebInstanceID = session.web.ID
	taskID, err := submitReport(ctx, d.Forward(session.web, session.port))
	if err != nil {
		return evidence, err
	}
	evidence.TaskID = taskID
	evidence.Initial, err = d.observeClaim(ctx, scope, session.logs, observe, taskID, session.submittedAt)
	if err != nil {
		return evidence, err
	}
	if ready, err := r.startupDeploymentsReady(ctx, target, scope, d.Store); err != nil || !ready {
		return evidence, errors.Join(state.ErrConflict, err)
	}
	return evidence, nil
}

type startupSession struct {
	web         state.Instance
	port        int
	logs        ClaimLogStream
	submittedAt time.Time
	close       context.CancelFunc
}

func (r *Runner) openStartupSession(ctx context.Context, target *Prepared, scope tasktoolrehearsal.SmokeScope, d *NativeStartup) (startupSession, error) {
	if err := waitFor(ctx, func(ctx context.Context) (bool, error) {
		return r.startupDeploymentsReady(ctx, target, scope, d.Store)
	}); err != nil {
		return startupSession{}, fmt.Errorf("wait for contained runtime deployments: %w", err)
	}
	web, port, err := d.wakeWeb(ctx, scope)
	if err != nil {
		return startupSession{}, fmt.Errorf("wake pinned rehearsal web deployment: %w", err)
	}
	// Worker deployments start through the existing imaged/schedd prime.
	// Wait for that exact worker before submitting a once-only MCP call.
	if err := waitFor(ctx, func(ctx context.Context) (bool, error) {
		return d.workerRunning(ctx, scope)
	}); err != nil {
		return startupSession{}, fmt.Errorf("wait for contained worker: %w", err)
	}
	if ready, err := r.startupDeploymentsReady(ctx, target, scope, d.Store); err != nil || !ready {
		return startupSession{}, errors.Join(state.ErrConflict, err)
	}
	logsCtx, stop := context.WithCancel(ctx)
	submittedAt := time.Now()
	logs, err := d.OpenClaimLogs(logsCtx, scope.WorkerAppID, scope.WorkerDeploymentID, submittedAt, claimEvent)
	if err != nil {
		stop()
		return startupSession{}, fmt.Errorf("open contained worker log stream: %w", err)
	}
	return startupSession{web: web, port: port, logs: logs, submittedAt: submittedAt, close: stop}, nil
}

func (r *Runner) startupDeploymentsReady(ctx context.Context, prepared *Prepared, scope tasktoolrehearsal.SmokeScope, store startupStore) (bool, error) {
	ready := true

	for _, target := range []struct{ app, deployment, role string }{
		{scope.WebAppID, scope.WebDeploymentID, Web}, {scope.WorkerAppID, scope.WorkerDeploymentID, Worker},
	} {
		deploymentID, _ := apidsource.OperationIDs(buildOperation(scope.RunID), target.app)
		if target.deployment != deploymentID {
			return false, state.ErrConflict
		}
		app, err := store.AppByID(ctx, target.app)
		if err != nil {
			return false, err
		}
		member, err := store.ScenarioTestMemberByApp(ctx, target.app)
		if err != nil {
			return false, err
		}
		if !prepared.owns(app, target.role) || app.Status != state.AppActive || app.PreviewPrState != state.PreviewPrStateOpen ||
			app.PreviewExpiresAt == nil || !app.PreviewExpiresAt.Equal(prepared.expiresAt) || !prepared.expiresAt.After(time.Now()) ||
			member.AccountID != scope.AccountID || member.RunID != prepared.scenarioID() || member.Workload != target.role ||
			member.NetworkMode != state.ScenarioNetworkContained || member.WebhookMode != state.ScenarioWebhookCaptureMode {
			return false, state.ErrConflict
		}
		dep, err := r.Store.DeploymentByID(ctx, target.deployment)
		if err != nil {
			return false, err
		}
		if !validSmokeDeployment(dep, target.app, target.deployment) {
			return false, state.ErrConflict
		}
		ready = ready && dep.Status == state.DeployLive
	}
	return ready, nil
}

func (d *NativeStartup) wakeWeb(ctx context.Context, scope tasktoolrehearsal.SmokeScope) (state.Instance, int, error) {
	id, node, deployment, wake, port, err := d.WakeWeb(ctx, scope.WebAppID, scope.WebDeploymentID, api.DefaultEnvScope)
	if err != nil {
		return state.Instance{}, 0, err
	}
	ins, err := d.Store.InstanceByID(ctx, id)
	if err != nil {
		return state.Instance{}, 0, err
	}
	if ins.NodeID != d.NodeID || !runningInstance(ins, id, scope.WebAppID, scope.WebDeploymentID, state.InstanceModeNormal) ||
		deployment != scope.WebDeploymentID || node != ins.NodeID || wake != ins.WakeID || port < 0 || port > 65535 {
		return state.Instance{}, 0, state.ErrConflict
	}
	return ins, port, nil
}

func (d *NativeStartup) workerRunning(ctx context.Context, scope tasktoolrehearsal.SmokeScope) (bool, error) {
	instances, err := state.ListLiveInstancesForApp(ctx, d.Store, scope.WorkerAppID)
	if err != nil {
		return false, err
	}
	for _, ins := range instances {
		if ins.NodeID == d.NodeID && runningInstance(ins, ins.ID, scope.WorkerAppID, scope.WorkerDeploymentID, state.InstanceModeWorker) {
			return true, nil
		}
	}
	return false, nil
}

func runningInstance(ins state.Instance, id, app, deployment string, mode state.InstanceMode) bool {
	return id != "" && ins.ID == id && ins.AppID == app && ins.DeploymentID == deployment &&
		ins.State == string(state.StateRunning) && ins.Mode == string(mode) && ins.NodeID != "" && ins.WakeID != "" && ins.Netns != ""
}

type claimReceipt struct {
	Event       string `json:"event"`
	Namespace   string `json:"namespace"`
	TaskID      string `json:"task_id"`
	Attempt     int    `json:"attempt"`
	WorkerID    string `json:"worker_id"`
	LeaseSHA256 string `json:"lease_sha256"`
	Checkpoint  bool   `json:"at_checkpoint,omitempty"`
}

func (d *NativeStartup) observeClaim(ctx context.Context, scope tasktoolrehearsal.SmokeScope, logs ClaimLogStream, observe pgruntime.TaskClaimObserver, taskID string, submittedAt time.Time) (tasktoolrehearsal.WorkerClaim, error) {
	claim, err := d.readClaim(ctx, scope, logs, observe, taskID, submittedAt, 1)
	return claim.worker, err
}

type observedClaim struct {
	worker     tasktoolrehearsal.WorkerClaim
	lease      pgruntime.TaskClaim
	instance   state.Instance
	writtenAt  time.Time
	checkpoint bool
}

func (d *NativeStartup) readClaim(ctx context.Context, scope tasktoolrehearsal.SmokeScope, logs ClaimLogStream, observe pgruntime.TaskClaimObserver, taskID string, since time.Time, attempt int) (observedClaim, error) {
	for {
		if err := ctx.Err(); err != nil {
			return observedClaim{}, err
		}
		frame, err := logs()
		if err != nil {
			return observedClaim{}, fmt.Errorf("read contained worker claim receipt: %w", err)
		}
		var claim claimReceipt
		if frame.IsGap || frame.Stream != "stdout" || frame.WrittenAt.Before(since) ||
			frame.DeploymentID != scope.WorkerDeploymentID || len(frame.Line) > 1024 ||
			json.Unmarshal([]byte(frame.Line), &claim) != nil || claim.Event != claimEvent ||
			claim.Namespace != scope.Namespace || claim.TaskID != taskID || claim.Attempt != attempt {
			continue
		}
		ins, err := d.Store.InstanceByID(ctx, frame.InstanceID)
		if err != nil {
			return observedClaim{}, err
		}
		if ins.NodeID != d.NodeID || !runningInstance(ins, frame.InstanceID, scope.WorkerAppID, scope.WorkerDeploymentID, state.InstanceModeWorker) ||
			frame.WrittenAt.Before(ins.StartedAt) {
			return observedClaim{}, state.ErrConflict
		}
		lease := pgruntime.TaskClaim{
			TaskID: taskID, DeploymentID: scope.WorkerDeploymentID, WorkerID: claim.WorkerID, LeaseSHA256: claim.LeaseSHA256, Attempt: claim.Attempt,
		}
		ok, err := observe(ctx, lease)
		if err != nil || !ok {
			return observedClaim{}, errors.Join(errors.New("worker receipt has no matching live PostgreSQL lease"), err)
		}
		current, err := d.Store.InstanceByID(ctx, ins.ID)
		if err != nil || !runningInstance(current, ins.ID, scope.WorkerAppID, scope.WorkerDeploymentID, state.InstanceModeWorker) ||
			current.NodeID != ins.NodeID || current.WakeID != ins.WakeID {
			return observedClaim{}, errors.Join(state.ErrConflict, err)
		}
		return observedClaim{
			worker: tasktoolrehearsal.WorkerClaim{TaskID: taskID, Namespace: scope.Namespace, DeploymentID: scope.WorkerDeploymentID, InstanceID: ins.ID, Status: "running", Attempt: attempt},
			lease:  lease, instance: current, writtenAt: frame.WrittenAt, checkpoint: claim.Checkpoint,
		}, nil
	}
}

func openRehearsalMCP(ctx context.Context, transport http.RoundTripper) (*mcphosting.Client, error) {
	if transport == nil {
		return nil, state.ErrInvalidArgument
	}
	client, err := mcphosting.NewClient("http://127.0.0.1/mcp", "", api.MCPProtocolVersion)
	if err != nil {
		return nil, err
	}
	client.HTTP.Transport = transport
	if err := client.OpenStateless(ctx); err != nil {
		return nil, errors.New("initialize the pinned rehearsal MCP endpoint")
	}
	return client, nil
}

func submitReport(ctx context.Context, transport http.RoundTripper) (string, error) {
	client, err := openRehearsalMCP(ctx, transport)
	if err != nil {
		return "", err
	}
	// The exact fixture tool has no HTTP parameter headers.
	tool := mcphosting.Tool{Name: "build_report", InputSchema: map[string]any{"type": "object"}}
	exchange, err := client.CallWithOptions(ctx, tool,
		map[string]any{"report": "hosted-task-rehearsal", "steps": 20}, mcphosting.CallOptions{EnableTasks: true})
	if err != nil {
		return "", errors.New("submit the fixed rehearsal Task")
	}
	var result mcphosting.Task
	if err := json.Unmarshal(exchange.Result, &result); err != nil || result.ResultType != "task" || result.Status != "working" {
		return "", errors.New("rehearsal MCP endpoint did not return an accepted Task")
	}
	id, err := uuid.Parse(result.TaskID)
	if err != nil || id == uuid.Nil || id.String() != result.TaskID {
		return "", errors.New("invalid rehearsal Task identity")
	}
	return result.TaskID, nil
}

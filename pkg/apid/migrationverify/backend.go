// adr:1659 — resumable migration evidence over existing runtime owners.
// Package migrationverify coordinates existing task, park, wake and hosting
// verification owners. A bounded server worker advances durable steps.
package migrationverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Snapshot struct {
	App            state.App
	Deployment     state.Deployment
	ChangedAt      time.Time
	RuntimeCurrent bool
	BlockedReason  string
}
type Backend struct {
	Store                 state.MigrationVerificationStore
	Tasks                 state.AppTaskStore
	AccountID, AppID, App string
	Snapshot              func(context.Context) (Snapshot, error)
	Smoke                 func(context.Context, api.MigrationVerification) (api.MigrationVerificationCheck, error)
	Park                  func(context.Context, api.MigrationVerification) error
	Drained               func(context.Context) (bool, error)
	Wake                  func(context.Context, api.MigrationVerification) error
	Running               func(context.Context, api.MigrationVerification) (string, bool, error)
	Restore               func(context.Context, api.MigrationVerification) (string, error)
	ConnectionsCurrent    func(context.Context, []api.MigrationServiceConnectionCheck) error
}

func (b Backend) Create(ctx context.Context, in api.CreateMigrationVerificationRequest) (api.MigrationVerification, error) {
	if err := in.Validate(); err != nil {
		return api.MigrationVerification{}, err
	}
	selectedDeployment, _ := uuid.Parse(in.DeploymentID)
	in.DeploymentID = selectedDeployment.String()
	// The caller's ID reconciles a lost creation response before current input
	// checks; a retry must discover the original receipt rather than replace it.
	if old, err := b.Store.GetMigrationVerification(ctx, b.AccountID, b.AppID, in.ID); err == nil {
		if !reflect.DeepEqual(old.Report.Request, in) {
			return old.Report, state.ErrConflict
		}
		return b.observe(ctx, old.Report)
	} else if !errors.Is(err, state.ErrNotFound) {
		return old.Report, err
	}
	snap, err := b.Snapshot(ctx)
	if err != nil {
		return api.MigrationVerification{}, err
	}
	if snap.BlockedReason != "" {
		return api.MigrationVerification{}, state.ErrConflict
	}
	if err := b.connectionsCurrent(ctx, in.Checks.ServiceConnections); err != nil {
		return api.MigrationVerification{}, err
	}
	snapshotDeployment, _ := uuid.Parse(snap.Deployment.ID)
	if snapshotDeployment != selectedDeployment || snap.Deployment.Status != state.DeployLive || snap.Deployment.TrafficPercent != 100 || snap.App.Visibility == api.AppVisibilityInternal || (snap.App.WorkloadClass != "" && snap.App.WorkloadClass != state.WorkloadClassHTTP && snap.App.WorkloadClass != state.WorkloadClassGraphQL) {
		return api.MigrationVerification{}, api.ErrValidation("verification requires the current public HTTP deployment with all Gregale traffic")
	}
	now := time.Now().UTC()
	r := api.MigrationVerification{ID: in.ID, App: b.App, DeploymentID: snap.Deployment.ID, Scope: snap.Deployment.Scope, ConfigurationRevision: state.MigrationConfigurationRevision(snap.Deployment, snap.ChangedAt), Status: "pending", Phase: "dependencies", Request: in, Checks: []api.MigrationVerificationCheck{}, CreatedAt: now, ExpiresAt: now.Add(api.MigrationVerificationTTL)}
	if r.Scope == "" {
		r.Scope = "default"
	}
	saved, err := b.Store.CreateMigrationVerification(ctx, state.MigrationVerificationRecord{AccountID: b.AccountID, AppID: b.AppID, Report: r})
	return saved.Report, err
}

func (b Backend) Get(ctx context.Context, id string) (api.MigrationVerification, error) {
	if _, err := uuid.Parse(id); err != nil {
		return api.MigrationVerification{}, state.ErrNotFound
	}
	r, err := b.Store.GetMigrationVerification(ctx, b.AccountID, b.AppID, id)
	if err != nil {
		return r.Report, err
	}
	return b.observe(ctx, r.Report)
}
func (b Backend) List(ctx context.Context) (api.MigrationVerificationList, error) {
	rows, err := b.Store.ListMigrationVerifications(ctx, b.AccountID, b.AppID)
	out := api.MigrationVerificationList{Runs: []api.MigrationVerification{}}
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		r, err := b.observe(ctx, row.Report)
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, r)
	}
	return out, nil
}
func (b Backend) observe(ctx context.Context, r api.MigrationVerification) (api.MigrationVerification, error) {
	// Pending includes stopping and restoration. Only its worker may declare it
	// terminal, after cleanup. Completed failures/cancellations keep their cause.
	if r.Status != "passed" {
		return r, nil
	}
	return b.validity(ctx, r)
}

func (b Backend) validity(ctx context.Context, r api.MigrationVerification) (api.MigrationVerification, error) {
	s, err := b.Snapshot(ctx)
	if errors.Is(err, state.ErrNotFound) {
		r.Status, r.Reason = "blocked", "deployment_changed"
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if s.BlockedReason != "" {
		r.Status, r.Reason = "blocked", s.BlockedReason
	} else if s.Deployment.ID != r.DeploymentID || s.Deployment.Status != state.DeployLive || s.Deployment.TrafficPercent != 100 || s.App.Visibility == api.AppVisibilityInternal || state.MigrationConfigurationRevision(s.Deployment, s.ChangedAt) != r.ConfigurationRevision {
		r.Status, r.Reason = "blocked", "deployment_or_configuration_changed"
	} else if r.Status == "pending" && !r.ExpiresAt.After(time.Now()) {
		r.Status, r.Reason = "expired", "verification_deadline_exceeded"
	} else if r.Status == "passed" && !s.RuntimeCurrent {
		r.Status, r.Reason = "blocked", "runtime_configuration_not_current"
	}
	if r.Status == "passed" || r.Status == "pending" {
		if err := b.connectionsCurrent(ctx, r.Request.Checks.ServiceConnections); errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			r.Status, r.Reason = "blocked", "service_connection_target_changed"
		} else if err != nil {
			return r, err
		}
	}
	return r, nil
}

func (b Backend) connectionsCurrent(ctx context.Context, connections []api.MigrationServiceConnectionCheck) error {
	if len(connections) == 0 {
		return nil
	}
	if b.ConnectionsCurrent == nil {
		return api.ErrCapacity("runtime service connection verification is unavailable")
	}
	return b.ConnectionsCurrent(ctx, connections)
}

func (b Backend) Advance(ctx context.Context, id string) (api.MigrationVerification, error) {
	if _, err := uuid.Parse(id); err != nil {
		return api.MigrationVerification{}, state.ErrNotFound
	}
	r, err := b.Store.GetMigrationVerification(ctx, b.AccountID, b.AppID, id)
	if err != nil {
		return r.Report, err
	}
	if r.Report.Status != "pending" {
		return b.observe(ctx, r.Report)
	}
	if r.LeaseUntil.After(time.Now()) {
		return b.observe(ctx, r.Report)
	}
	r.LeaseUntil = time.Now().Add(api.MigrationVerificationLease)
	r.LeaseID = uuid.NewString()
	// Persist the possibility of a committed park BEFORE calling its owner.
	// Older pending receipts also recover from their recorded cold phase.
	if r.Report.Phase == "park" || r.Report.Phase == "drain" || r.Report.Phase == "wake" || r.Report.Phase == "waking" {
		r.RestoreRequired = true
	}
	r, err = b.Store.SaveMigrationVerification(ctx, r, r.Version, nil)
	if errors.Is(err, state.ErrConflict) {
		return b.Get(ctx, id)
	}
	if err != nil {
		return r.Report, err
	}
	ctx, cancel := context.WithDeadline(ctx, r.LeaseUntil.Add(-api.MigrationVerificationPersistTimeout))
	defer cancel()
	if r.StopStatus == "" {
		switch {
		case r.Report.CancelRequestedAt != nil:
			stop(&r, "cancelled", "verification_cancelled")
		case !r.Report.ExpiresAt.After(time.Now()):
			stop(&r, "expired", "verification_deadline_exceeded")
		default:
			var observed api.MigrationVerification
			observed, err = b.validity(ctx, r.Report)
			if err == nil && observed.Status != "pending" {
				stop(&r, observed.Status, observed.Reason)
			}
		}
	}
	if err != nil {
		return b.retry(ctx, r, err)
	}
	if r.StopStatus != "" {
		return b.cleanup(ctx, r)
	}
	workCtx, workCancel := context.WithDeadline(ctx, r.Report.ExpiresAt)
	defer workCancel()
	ctx = workCtx
	var task *state.CreateAppTaskParams
	r.Report.Reason = ""
	switch r.Report.Phase {
	case "dependencies", "cold_dependencies":
		task, err = b.dependencies(ctx, &r)
	case "startup", "cold_smoke":
		var snap Snapshot
		snap, err = b.Snapshot(ctx)
		if err == nil && !snap.RuntimeCurrent {
			r.Report.Reason = "waiting_for_current_runtime"
			break
		}
		if err == nil {
			var check api.MigrationVerificationCheck
			check, err = b.Smoke(ctx, r.Report)
			if err == nil {
				check.Name = r.Report.Phase
				r.Report.Checks = append(r.Report.Checks, check)
				if check.Status != "passed" {
					r.Report.Status, r.Report.Reason = "failed", "smoke_failed"
				} else if r.Report.Phase == "startup" && r.Report.Request.ColdWake {
					r.Report.Phase = "park"
					r.Report.WakeID = uuid.NewString()
				} else if r.Report.Phase == "cold_smoke" {
					r.Report.Phase = "cold_dependencies"
				} else {
					finish(&r.Report)
				}
			}
		}
	case "park":
		err = b.Park(ctx, r.Report)
		if err == nil {
			r.Report.Phase = "drain"
		}
	case "drain":
		var drained bool
		drained, err = b.Drained(ctx)
		if err == nil && drained {
			now := time.Now().UTC()
			r.Report.DrainedAt = &now
			r.Report.Phase = "wake"
		}
	case "wake":
		// The correlation ID is durable before the notification. Replays use
		// that identity even when publication succeeds and its response is lost.
		err = b.Wake(ctx, r.Report)
		if err == nil {
			r.Report.Phase = "waking"
		}
	case "waking":
		var instance string
		var ready bool
		instance, ready, err = b.Running(ctx, r.Report)
		if err == nil && ready {
			r.RestoreRequired = false
			r.Report.InstanceID = instance
			r.Report.Checks = append(r.Report.Checks, api.MigrationVerificationCheck{Name: "cold_wake", Status: "passed", Detail: "All serving instances drained; the selected deployment resumed with this verification’s wake identity."})
			r.Report.Phase = "cold_smoke"
		} else if err == nil {
			// LISTEN can disconnect after a successful publication. Re-publish
			// the original wake identity while waiting; schedd still owns
			// deduplication and instance/capacity admission.
			err = b.Wake(ctx, r.Report)
		}
	default:
		err = state.ErrConflict
	}
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			stop(&r, "blocked", "verification_state_changed")
			return b.cleanup(ctx, r)
		}
		return b.retry(ctx, r, err)
	}
	if r.Report.Status == "failed" {
		stop(&r, r.Report.Status, r.Report.Reason)
		return b.cleanup(ctx, r)
	}
	r.Failures = 0
	r.NextAttemptAt = time.Now().Add(api.MigrationVerificationStepInterval)
	return b.save(ctx, r, task)
}

func finish(r *api.MigrationVerification) {
	now := time.Now().UTC()
	r.Status, r.Phase = "passed", "complete"
	r.CompletedAt = &now
}
func (b Backend) save(ctx context.Context, r state.MigrationVerificationRecord, task *state.CreateAppTaskParams) (api.MigrationVerification, error) {
	leaseID := r.LeaseID
	r.LeaseUntil = time.Time{}
	r.LeaseID = ""
	// A disconnected HTTP client or daemon shutdown cannot erase a completed
	// step. CAS still fences cancellation and another lease holder.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrationVerificationPersistTimeout)
	defer cancel()
	saved, err := b.Store.SaveMigrationVerification(ctx, r, r.Version, task)
	if errors.Is(err, state.ErrConflict) {
		return b.releaseCancelledLease(ctx, r.Report.ID, leaseID)
	}
	if err != nil {
		return saved.Report, err
	}
	return b.observe(ctx, saved.Report)
}

func (b Backend) releaseCancelledLease(ctx context.Context, id, leaseID string) (api.MigrationVerification, error) {
	r, err := b.Store.GetMigrationVerification(ctx, b.AccountID, b.AppID, id)
	if err != nil {
		return r.Report, err
	}
	// The side effect has ended and cancellation won its save. Release only
	// our own lease so cleanup can start promptly, without touching a new owner.
	if leaseID != "" && r.LeaseID == leaseID && r.Report.CancelRequestedAt != nil {
		r.LeaseUntil, r.LeaseID, r.NextAttemptAt = time.Time{}, "", time.Now().UTC()
		r, err = b.Store.SaveMigrationVerification(ctx, r, r.Version, nil)
		if errors.Is(err, state.ErrConflict) {
			return b.Get(ctx, id)
		}
		if err != nil {
			return r.Report, err
		}
	}
	return b.observe(ctx, r.Report)
}

func (b Backend) retry(ctx context.Context, r state.MigrationVerificationRecord, cause error) (api.MigrationVerification, error) {
	r.Failures = min(r.Failures+1, api.MigrationVerificationCancelAttempts)
	delay := min(api.MigrationVerificationStepInterval*time.Duration(1<<r.Failures), api.MigrationVerificationRetryMax)
	r.NextAttemptAt = time.Now().Add(delay)
	if r.StopStatus == "" {
		r.Report.Reason = "verification_retrying"
	}
	out, err := b.save(ctx, r, nil)
	if err != nil {
		return out, err
	}
	return out, cause
}

func (b Backend) dependencies(ctx context.Context, r *state.MigrationVerificationRecord) (*state.CreateAppTaskParams, error) {
	if reflect.DeepEqual(r.Report.Request.Checks, api.MigrationDependencyChecks{}) {
		if r.Report.Phase == "dependencies" {
			r.Report.Phase = "startup"
		} else {
			finish(&r.Report)
		}
		return nil, nil
	}
	id := r.Report.DependencyTaskID
	prefix := "startup/"
	if r.Report.Phase == "cold_dependencies" {
		id = r.Report.ColdDependencyTaskID
		prefix = "after_wake/"
	}
	if id == "" {
		timeout := api.MigrationVerificationTaskTimeout + len(r.Report.Request.Checks.ServiceConnections)*api.BindingVerificationRunProbeTimeoutSeconds
		return &state.CreateAppTaskParams{AccountID: b.AccountID, AppID: b.AppID, DeploymentID: r.Report.DeploymentID, RequireLiveDeployment: true, Kind: state.AppTaskKindManual, Command: api.MigrationDependenciesCommand(r.Report.Request.Checks), TimeoutSeconds: timeout, MaxOutputBytes: api.MigrationVerificationMaxOutputBytes, CreatedAt: time.Now().UTC()}, nil
	}
	task, err := b.Tasks.AppTaskByID(ctx, b.AccountID, b.AppID, id)
	if err != nil {
		return nil, err
	}
	if !task.Status.Terminal() {
		return nil, nil
	}
	var report api.MigrationDependencyReport
	valid := !task.OutputTruncated && len(task.StdoutTail) <= api.MigrationVerificationMaxOutputBytes && json.Unmarshal([]byte(task.StdoutTail), &report) == nil
	wanted := []string{}
	c := r.Report.Request.Checks
	if c.PostgresEnvKey != "" {
		wanted = append(wanted, "postgres")
	}
	if c.ObjectStoragePrefix != "" {
		wanted = append(wanted, "object_storage")
	}
	if c.HTTPEnvKey != "" {
		wanted = append(wanted, "external_api")
	}
	for _, connection := range c.ServiceConnections {
		wanted = append(wanted, "service/"+connection.EnvKey)
	}
	if len(report.Checks) != len(wanted) {
		valid = false
	}
	passed := valid && task.Status == state.AppTaskSucceeded && task.ExitCode != nil && *task.ExitCode == 0
	for i, check := range report.Checks {
		if i >= len(wanted) || check.Name != wanted[i] || (check.Status != "passed" && check.Status != "failed") {
			valid = false
			break
		}
		if check.Status != "passed" {
			passed = false
		}
	}
	if !valid {
		r.Report.Checks = append(r.Report.Checks, api.MigrationVerificationCheck{Name: prefix + "dependencies", Status: "failed", Detail: "The dependency task did not return a complete report. Inspect its task status and rerun verification."})
		passed = false
	} else {
		for _, check := range report.Checks {
			// Guest output is untrusted: accept the stage and verdict, reconstruct
			// diagnostic text instead of reflecting arbitrary output into a report.
			check.Detail = dependencyDetail(check.Name, check.Status)
			check.Name = prefix + check.Name
			r.Report.Checks = append(r.Report.Checks, check)
		}
	}
	if !passed {
		r.Report.Status, r.Report.Reason = "failed", "dependency_failed"
	} else if r.Report.Phase == "dependencies" {
		r.Report.Phase = "startup"
	} else {
		finish(&r.Report)
	}
	return nil, nil
}
func dependencyDetail(name, status string) string {
	if strings.HasPrefix(name, "service/") {
		if status == "passed" {
			return "The runtime variable matches its declared private HTTPS service; the caller reached the selected deployment through its authorized route."
		}
		return "Check the runtime variable, declared service binding, target deployment, private TLS trust and caller permissions."
	}
	if status == "passed" {
		switch name {
		case "postgres":
			return "Authenticated connection and read-only SELECT 1 succeeded."
		case "object_storage":
			return "Authenticated read-only storage listing succeeded."
		default:
			return "External API returned a successful response."
		}
	}
	return fmt.Sprintf("Check the %s environment variables, network access, TLS settings and permissions.", strings.ReplaceAll(name, "_", " "))
}

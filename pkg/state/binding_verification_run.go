// adr:1355 — fixed-deadline, atomically admitted verification receipts.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type VerificationRunAdmission struct {
	Task  *CreateAppTaskParams
	Queue *queueCanarySnapshot
}
type VerificationRunCreate struct {
	Report     api.BindingVerificationRun
	Admissions []VerificationRunAdmission
}
type verificationRunRecord struct {
	Report     api.BindingVerificationRun
	Admissions []VerificationRunAdmission
	Artifact   string
}
type BindingVerificationRunStore interface {
	BindingVerificationRunBudget(context.Context, string, string, string) (api.BindingVerificationBudget, error)
	CreateBindingVerificationRun(context.Context, string, string, VerificationRunCreate) (api.BindingVerificationRun, error)
	GetBindingVerificationRun(context.Context, string, string, string) (api.BindingVerificationRun, error)
	CheckBindingVerificationRun(context.Context, string, string, string, ...[]VerificationRunAdmission) error
	AdmitBindingVerificationRunStep(context.Context, string, string, string, int) (api.BindingVerificationRunStep, error)
	SetBindingVerificationRunStatus(context.Context, string, string, string, string) error
}

func verificationArtifactJSON(raw []byte) (string, error) {
	dep, err := bindingVerificationDeploymentJSON(raw)
	if err != nil {
		return "", err
	}
	return bindingVerificationArtifact(dep)
}
func validateVerificationRunCreate(account, app string, in VerificationRunCreate) error {
	r := in.Report
	if api.BindingVerificationConcurrency(r.MaxConcurrency) < 1 || api.BindingVerificationConcurrency(r.MaxConcurrency) > api.BindingVerificationRunnerMaxConcurrency || !canonicalStateUUID(r.ID) || !canonicalStateUUID(r.DeploymentID) || r.Policy.AppID != app || r.Policy.Mode != "enforce" || r.Policy.Revision < 1 || r.Scope != r.Policy.Scope || r.Inventory.RuntimeFreshness == nil || r.Inventory.RuntimeFreshness.ConfigChangedAt == nil || r.Inventory.RuntimeFreshness.ConfigChangedAt.IsZero() || !r.Inventory.Complete || r.CreatedAt.IsZero() || r.CreatedAt.After(time.Now()) || !r.ExpiresAt.After(r.CreatedAt) || r.ExpiresAt.Sub(r.CreatedAt) > time.Duration(api.BindingVerificationRunMaxTTLSeconds)*time.Second || len(r.Steps) > api.BindingVerificationRunMaxSteps || len(in.Admissions) != len(r.Steps) || !bindingRevisionPattern.MatchString(r.ReviewDigest) {
		return ErrInvalidArgument
	}
	for i, step := range r.Steps {
		if step.Owned {
			return ErrInvalidArgument
		}
		switch step.Plan.Action {
		case "probe":
			if step.ResourceID != "" || step.ResourceKind != "" {
				return ErrInvalidArgument
			}
			task := in.Admissions[i].Task
			if task == nil || task.AccountID != account || task.AppID != app || task.DeploymentID != r.DeploymentID || task.BindingVerification == nil || task.Kind != AppTaskKindManual || !task.RequireLiveDeployment || task.TimeoutSeconds != api.BindingVerificationRunProbeTimeoutSeconds || task.MaxOutputBytes != api.BindingVerificationRunProbeOutputBytes {
				return ErrInvalidArgument
			}
			if _, err := resolveCreateAppTask(*task); err != nil {
				return err
			}
		case "queue-canary":
			if step.ResourceID != "" || step.ResourceKind != "" {
				return ErrInvalidArgument
			}
			if !r.QueuePublish {
				return ErrInvalidArgument
			}
		case "wait-queue", "wait", "reuse", "observe", "skip":
		default:
			return ErrInvalidArgument
		}
		if step.Plan.Type == api.BindingTypeObjectStorage && step.Plan.Action == "probe" && step.Plan.ProbeMode != "read" && !r.ObjectStorageWrite {
			return ErrInvalidArgument
		}
	}
	return nil
}
func verificationRunActive(r api.BindingVerificationRun) error {
	if r.Status != "pending" || !r.ExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	return nil
}
func cloneVerificationRecord(r verificationRunRecord) verificationRunRecord {
	raw, _ := json.Marshal(r)
	var out verificationRunRecord
	_ = json.Unmarshal(raw, &out)
	return out
}
func publicVerificationRun(r verificationRunRecord) api.BindingVerificationRun {
	out := cloneVerificationRecord(r).Report
	out.MaxConcurrency = api.BindingVerificationConcurrency(out.MaxConcurrency)
	if out.Status != "cancelled" && !out.ExpiresAt.After(time.Now()) {
		out.Status, out.Reason = "expired", "run_expired"
	}
	return out
}

// Reads project live resource status without exposing private pins, command
// configuration, stdout or credentials. Missing resources never authorize reuse.
func observeVerificationRun(ctx context.Context, store interface {
	AppTaskByID(context.Context, string, string, string) (AppTask, error)
}, canaries QueueBindingCanaryStore, account, app string, r api.BindingVerificationRun) (api.BindingVerificationRun, error) {
	for i := range r.Steps {
		step := &r.Steps[i]
		if step.ResourceID == "" {
			continue
		}
		if step.ResourceKind == "task" {
			task, err := store.AppTaskByID(ctx, account, app, step.ResourceID)
			if errors.Is(err, ErrNotFound) {
				step.Status = "missing"
				continue
			}
			if err != nil {
				return r, err
			}
			step.Status = string(task.Status)
		} else if step.ResourceKind == "queue" {
			canary, err := canaries.GetQueueBindingCanary(ctx, account, app, step.ResourceID)
			if errors.Is(err, ErrNotFound) {
				step.Status = "missing"
				continue
			}
			if err != nil {
				return r, err
			}
			step.Status = canary.Status
		}
	}
	return r, nil
}

func verificationRunAdmissionsMatch(r verificationRunRecord, supplied [][]VerificationRunAdmission) bool {
	if len(supplied) == 0 {
		return true
	}
	if len(supplied) != 1 || len(supplied[0]) != len(r.Admissions) {
		return false
	}
	for i, old := range r.Admissions {
		if old.Task != nil {
			a, _ := json.Marshal(old.Task)
			b, _ := json.Marshal(supplied[0][i].Task)
			if string(a) != string(b) {
				return false
			}
		}
	}
	return true
}

type VerificationRunCapacityError struct{ Limit, Observed int }

func (e *VerificationRunCapacityError) Error() string { return ErrQuotaExceeded.Error() }
func (e *VerificationRunCapacityError) Unwrap() error { return ErrQuotaExceeded }

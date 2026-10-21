package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// This capability is also the scheduler's authoritative candidate hold. A
// failed lookup must stop reconciliation, never authorize retiring a worker.
type ProjectReleaseWorkerStore interface {
	PreviewProjectReleaseWorker(context.Context, string, string, string) (*api.ProjectReleaseWorker, error)
	HeldProjectReleaseWorkers(context.Context, string) ([]string, error)
	ClaimProjectWorkerDrain(context.Context, string, string, string) (bool, error)
}

type projectReleaseWorkerContextKey struct{}

func ContextWithProjectReleaseWorkerAttempt(ctx context.Context, attempt string) context.Context {
	return context.WithValue(ctx, projectReleaseWorkerContextKey{}, attempt)
}
func projectReleaseWorkerAttempt(ctx context.Context) string {
	attempt, _ := ctx.Value(projectReleaseWorkerContextKey{}).(string)
	return attempt
}

// Capture all bindings and consumer intent, including disabled/other-scope
// records. Concurrent operator changes invalidate the plan rather than transfer
// work to a changed consumer. No payload, credentials or queue messages appear.
type projectWorkerCapture struct {
	Worker    *api.ProjectReleaseWorker `json:"worker"`
	Bindings  []json.RawMessage         `json:"bindings"`
	Consumers []json.RawMessage         `json:"consumers"`
}

func (capture projectWorkerCapture) policy() (*api.ProjectReleaseWorker, error) {
	if capture.Worker == nil || capture.Worker.AppID == "" || len(capture.Bindings) != 1 || len(capture.Consumers) != 1 {
		return nil, ErrConflict
	}
	var binding struct {
		ID, Mode, Name string
		Enabled        bool
		Scope          string     `json:"deployment_scope"`
		Class          string     `json:"workload_class"`
		RetiredAt      *time.Time `json:"retired_at"`
	}
	if json.Unmarshal(capture.Bindings[0], &binding) != nil || binding.ID == "" || binding.Mode != "push" || !binding.Enabled || binding.Scope != "production" || binding.Class != string(WorkloadClassWorker) || binding.RetiredAt != nil {
		return nil, ErrConflict
	}
	var consumer struct {
		Kind      string
		Enabled   bool
		Source    string
		BindingID string `json:"queue_binding_id"`
		Scope     string `json:"queue_binding_scope"`
	}
	if json.Unmarshal(capture.Consumers[0], &consumer) != nil || consumer.Kind != "queue" || consumer.Source != "queue" || !consumer.Enabled || consumer.BindingID != canonicalMemUUID(binding.ID) || consumer.Scope != "production" {
		return nil, ErrConflict
	}
	if capture.Worker.DrainSeconds <= 0 {
		capture.Worker.DrainSeconds = 30
	}
	if capture.Worker.DrainSeconds > api.ProjectReleaseWorkerMaxDrainSeconds {
		return nil, ErrInvalidArgument
	}
	raw, _ := json.Marshal(struct{ Bindings, Consumers []json.RawMessage }{capture.Bindings, capture.Consumers})
	copy := *capture.Worker
	copy.BindingID = binding.ID
	copy.BindingHash = fmt.Sprintf("%x", sha256.Sum256(raw))
	copy.Status = "building"
	return &copy, nil
}

func workerReceiptCandidate(receipt api.ProjectSubmissionReceipt, d Deployment) bool {
	safe := receipt.Apply.SafeRelease
	return receipt.Status == "processing" && safe != nil && safe.Worker != nil && safe.Status == "building" && safe.Worker.AppID == d.AppID && safe.Verification != nil && d.Scope == "production" && d.TrafficPercentExplicit && d.TrafficPercent == 0 && d.submissionAttemptID == ProjectWorkloadAttempt(receipt.AttemptID, d.AppID)
}

func selectedProjectWorkerDeployment(receipt api.ProjectSubmissionReceipt) string {
	for _, build := range receipt.Apply.Builds {
		if build.AppID == receipt.Apply.SafeRelease.Worker.AppID {
			return build.DeploymentID
		}
	}
	return ""
}

// A desired worker manifest cannot turn an older HTTP deployment into a safe
// rollback target. Resolve the retained deployment's immutable settings.
func validatePreviousProjectWorker(ctx context.Context, store interface {
	AppByID(context.Context, string) (App, error)
	DeploymentByID(context.Context, string) (Deployment, error)
}, worker *api.ProjectReleaseWorker) (*api.ProjectReleaseWorker, error) {
	dep, err := store.DeploymentByID(ctx, worker.PreviousDeploymentID)
	if err != nil {
		return nil, err
	}
	app, err := AppForDeployment(ctx, store, dep)
	if err != nil {
		return nil, err
	}
	grace := app.Manifest.StopGracePeriodS
	if grace == 0 {
		grace = 30
	}
	if app.ID != worker.AppID || app.Manifest.ExecutionMode != api.ExecutionModeWorker || grace != worker.DrainSeconds {
		return nil, ErrConflict
	}
	return worker, nil
}

package tasktoolrelease

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func ExecutionReleaseID(record api.TaskToolReleaseIntentRecord) string {
	if record.Execution == nil {
		return ""
	}
	return toolpublication.Digest([]byte(record.AccountID + "/" + record.ID + "/" + record.Execution.OperationID))[:32]
}

func receiptStage(stage string) bool {
	return slices.Contains([]string{"prepared", "deploying_worker", "deploying_web", "replacements_started", "web_promoted", "draining_previous", "previous_parked", "complete"}, stage)
}

// ValidateReceipt checks immutable identity and bounded checkpoint metadata.
// It does not certify that a deployment or Task worker is currently healthy.
func ValidateReceipt(record api.TaskToolReleaseIntentRecord, r api.TaskToolReleaseReceipt) error {
	if r.Revision == 0 || r.Revision > api.TaskToolReleaseReceiptRevisionMax || r.ReleaseID != ExecutionReleaseID(record) || !receiptStage(r.Stage) || r.PreviousDeployments == nil || r.WorkerIDs == nil || r.Parked == nil || len(r.WorkerIDs) > api.TaskToolReleaseReceiptWorkerIDsMax {
		return ErrInvalid
	}
	for _, id := range []string{r.ServingDeploymentID, r.WebDeploymentID, r.WorkerDeploymentID} {
		if id != "" && uuid.Validate(id) != nil {
			return ErrInvalid
		}
	}
	if r.PendingSubmission != "" && r.PendingSubmission != "web" && r.PendingSubmission != "worker" {
		return ErrInvalid
	}
	if r.PendingSubmission == "web" && r.WebDeploymentID != "" || r.PendingSubmission == "worker" && r.WorkerDeploymentID != "" {
		return ErrInvalid
	}
	previous := map[string]string{}
	serving := ""
	for _, app := range record.Intent.Apps {
		if app.Role == "previous_worker" {
			previous[app.App] = app.Review.LatestDeploymentID
		}
		if app.Role == "web" && len(app.Review.ServingDeploymentIDs) == 1 {
			serving = app.Review.ServingDeploymentIDs[0]
		}
	}
	if len(r.PreviousDeployments) > len(previous) || len(r.Parked) > len(previous) {
		return ErrInvalid
	}
	for app, id := range r.PreviousDeployments {
		if uuid.Validate(id) != nil || previous[app] != id {
			return ErrChanged
		}
	}
	for app, parked := range r.Parked {
		if !parked || r.PreviousDeployments[app] == "" {
			return ErrInvalid
		}
	}
	if r.ServingCaptured && (r.ServingDeploymentID != serving || !reflect.DeepEqual(previous, r.PreviousDeployments)) {
		return ErrChanged
	}
	if !r.ServingCaptured && (r.ServingDeploymentID != "" || len(r.PreviousDeployments) != 0 || r.WebDeploymentID != "" || r.WorkerDeploymentID != "" || r.PendingSubmission != "") {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range r.WorkerIDs {
		if uuid.Validate(id) != nil || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	if len(r.WorkerIDs) > 0 && r.WorkerDeploymentID == "" || r.Promoted && r.WebDeploymentID == "" || len(r.Parked) > 0 && !r.Promoted {
		return ErrInvalid
	}
	return nil
}

func advanceReceipt(record api.TaskToolReleaseIntentRecord, next api.TaskToolReleaseReceipt) (*api.TaskToolReleaseReceipt, error) {
	if err := ValidateReceipt(record, next); err != nil {
		return nil, err
	}
	old := record.Execution.Receipt
	if old != nil {
		stages := []string{"prepared", "deploying_worker", "deploying_web", "replacements_started", "web_promoted", "draining_previous", "previous_parked", "complete"}
		if slices.Index(stages, next.Stage) < slices.Index(stages, old.Stage) {
			return nil, ErrChanged
		}
		if next.Revision < old.Revision || next.Revision == old.Revision && !reflect.DeepEqual(*old, next) {
			return nil, ErrChanged
		}
		if old.ServingCaptured && !next.ServingCaptured || old.Promoted && !next.Promoted ||
			old.WebDeploymentID != "" && old.WebDeploymentID != next.WebDeploymentID || old.WorkerDeploymentID != "" && old.WorkerDeploymentID != next.WorkerDeploymentID ||
			len(old.WorkerIDs) > 0 && !slices.Equal(old.WorkerIDs, next.WorkerIDs) {
			return nil, ErrChanged
		}
		for app, id := range old.PreviousDeployments {
			if next.PreviousDeployments[app] != id {
				return nil, ErrChanged
			}
		}
		for app, parked := range old.Parked {
			if parked && !next.Parked[app] {
				return nil, ErrChanged
			}
		}
		if old.PendingSubmission != "" && next.PendingSubmission != old.PendingSubmission {
			id := next.WorkerDeploymentID
			if old.PendingSubmission == "web" {
				id = next.WebDeploymentID
			}
			if id == "" {
				return nil, ErrChanged
			}
		}
	}
	// Copy caller-owned maps/slices before storing the checkpoint.
	b, _ := json.Marshal(next)
	var out api.TaskToolReleaseReceipt
	_ = json.Unmarshal(b, &out)
	return &out, nil
}

func ReceiptStages(r api.TaskToolReleaseReceipt) []string {
	out := []string{}
	if r.ServingCaptured {
		out = append(out, "baseline_captured")
	}
	if r.WorkerDeploymentID != "" {
		out = append(out, "worker_submitted")
	}
	if r.WebDeploymentID != "" {
		out = append(out, "web_submitted")
	}
	if len(r.WorkerIDs) > 0 {
		out = append(out, "workers_recorded")
	}
	if r.Promoted {
		out = append(out, "promotion_recorded")
	}
	if r.Promoted && len(r.Parked) == len(r.PreviousDeployments) {
		out = append(out, "previous_workers_parked")
	}
	if r.Stage == "complete" && r.WebDeploymentID != "" && r.WorkerDeploymentID != "" && len(r.WorkerIDs) > 0 && r.PendingSubmission == "" && r.Promoted && len(r.Parked) == len(r.PreviousDeployments) {
		out = append(out, "release_completed")
	}
	return out
}

func receiptComplete(r *api.TaskToolReleaseReceipt) bool {
	return r != nil && slices.Contains(ReceiptStages(*r), "release_completed")
}

func SubmissionReason(releaseID, role string) string {
	if len(releaseID) != 32 || strings.Trim(releaseID, "0123456789abcdef") != "" || role != "web" && role != "worker" {
		return ""
	}
	return "mcp-release-" + releaseID + "-" + role
}

package tasktoolrelease_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func runningReceiptFixture(t *testing.T) (api.TaskToolReleaseIntentRecord, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	in := fixture()
	for i := range in.Apps {
		if in.Apps[i].Role != "worker" {
			in.Apps[i].Review.LatestDeploymentID = uuid.NewString()
		}
		if in.Apps[i].Role == "web" {
			in.Apps[i].Review.ServingDeploymentIDs = []string{in.Apps[i].Review.LatestDeploymentID}
		}
	}
	in.Apps = append(in.Apps, api.TaskToolReleaseApp{Role: "previous_worker", App: "previous", Review: api.AppReleaseReview{AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("b", 64), LatestDeploymentID: uuid.NewString(), ServingDeploymentIDs: []string{}}})
	r, err := tasktoolrelease.New(uuid.NewString(), uuid.NewString(), strings.Repeat("c", 64), in, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	r.Status = "approved"
	r.Execution = &api.TaskToolReleaseExecution{OperationID: strings.Repeat("d", 64), BindingsSHA256: strings.Repeat("e", 64), AuthorizerKeyID: uuid.NewString(), State: "running", AuthorizedAt: now.Add(-30 * time.Second), ClaimSHA256: strings.Repeat("f", 64)}
	return r, now
}

func cloneReceipt(in api.TaskToolReleaseReceipt) api.TaskToolReleaseReceipt {
	b, _ := json.Marshal(in)
	var out api.TaskToolReleaseReceipt
	_ = json.Unmarshal(b, &out)
	return out
}

func TestMCPTaskReceiptMonotonicCheckpoints(t *testing.T) {
	record, now := runningReceiptFixture(t)
	token := strings.Repeat("1", 64)
	// Acquire a real claim digest through the state machine rather than guessing it.
	record.Execution.State = "authorized"
	record.Execution.ClaimSHA256 = ""
	in := api.TaskToolReleaseIntentDecision{ReleaseFingerprint: record.Intent.ReleaseFingerprint, Execution: &api.TaskToolReleaseExecutionRequest{Action: "claim", OperationID: record.Execution.OperationID, BindingsSHA256: record.Execution.BindingsSHA256, ClaimToken: token}}
	var err error
	record, err = tasktoolrelease.Execution(record, in, true, now)
	if err != nil {
		t.Fatal(err)
	}
	r := api.TaskToolReleaseReceipt{Revision: 1, ReleaseID: tasktoolrelease.ExecutionReleaseID(record), Stage: "prepared", PreviousDeployments: map[string]string{}, WorkerIDs: []string{}, Parked: map[string]bool{}}
	checkpoint := func() {
		t.Helper()
		in.Execution.Action = "guard"
		in.Execution.Stage = r.Stage
		in.Execution.Receipt = &r
		record, err = tasktoolrelease.Execution(record, in, true, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	checkpoint()
	r.Revision++
	r.Stage = "deploying_worker"
	r.ServingCaptured = true
	for _, app := range record.Intent.Apps {
		if app.Role == "web" {
			r.ServingDeploymentID = app.Review.ServingDeploymentIDs[0]
		}
		if app.Role == "previous_worker" {
			r.PreviousDeployments[app.App] = app.Review.LatestDeploymentID
		}
	}
	checkpoint()
	r.Revision++
	r.PendingSubmission = "worker"
	checkpoint()
	if record.Execution.Receipt.PendingSubmission != "worker" {
		t.Fatal("submission was not durable before acknowledgement")
	}
	// Clearing an unknown POST without a receipt must not make it retryable.
	bad := cloneReceipt(r)
	bad.Revision++
	bad.PendingSubmission = ""
	in.Execution.Receipt = &bad
	if _, err := tasktoolrelease.Execution(record, in, true, now); err == nil {
		t.Fatal("uncertain submission was erased")
	}
	r.Revision++
	r.PendingSubmission = ""
	r.WorkerDeploymentID = uuid.NewString()
	checkpoint()
	checkpoint() // Lost checkpoint reply: exact duplicate succeeds without replacement.
	for _, change := range []string{"revision", "release", "worker", "baseline", "stage"} {
		t.Run(change, func(t *testing.T) {
			bad := cloneReceipt(r)
			bad.Revision++
			switch change {
			case "revision":
				bad.Revision = r.Revision
				bad.PendingSubmission = "web"
			case "release":
				bad.ReleaseID = strings.Repeat("0", 32)
			case "worker":
				bad.WorkerDeploymentID = uuid.NewString()
			case "baseline":
				bad.ServingDeploymentID = uuid.NewString()
			case "stage":
				bad.Stage = "prepared"
			}
			request := in
			copyReq := *in.Execution
			request.Execution = &copyReq
			copyReq.Receipt = &bad
			copyReq.Stage = bad.Stage
			if _, err := tasktoolrelease.Execution(record, request, true, now); err == nil {
				t.Fatal("checkpoint rewrote retained identity")
			}
		})
	}
	r.Revision++
	r.Stage = "deploying_web"
	r.PendingSubmission = "web"
	checkpoint()
	r.Revision++
	r.PendingSubmission = ""
	r.WebDeploymentID = uuid.NewString()
	checkpoint()
	r.Revision++
	r.Stage = "replacements_started"
	r.WorkerIDs = []string{uuid.NewString()}
	checkpoint()
	r.Revision++
	r.Stage = "web_promoted"
	r.Promoted = true
	checkpoint()
	if record.Execution.CompletedStages[len(record.Execution.CompletedStages)-1] != "promotion_recorded" {
		t.Fatal("completed stage evidence missing")
	}
	in.Execution.Action = "complete"
	in.Execution.Receipt = nil
	if _, err := tasktoolrelease.Execution(record, in, true, now); err == nil {
		t.Fatal("incomplete checkpoint certified completion")
	}
	r.Revision++
	r.Stage = "previous_parked"
	r.Parked["previous"] = true
	checkpoint()
	r.Revision++
	r.Stage = "complete"
	checkpoint()
	in.Execution.Action = "complete"
	in.Execution.Receipt = nil
	record, err = tasktoolrelease.Execution(record, in, true, now)
	if err != nil || record.Execution.State != "completed" || record.Execution.CompletedStages[len(record.Execution.CompletedStages)-1] != "release_completed" {
		t.Fatal(record, err)
	}
}

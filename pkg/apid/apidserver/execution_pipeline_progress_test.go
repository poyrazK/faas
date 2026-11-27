package apidserver_test

import (
	"encoding/json"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPipelineLiveProgressAndExplicitRerun(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	identity := apidserver.EnableExecutionAPIForTest(t, &e)
	old, olds := *apidserver.MfaIdentity, *apidserver.MfaIdentities
	*apidserver.MfaIdentity = func() *age.X25519Identity { return identity }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.MfaIdentity, *apidserver.MfaIdentities = old, olds })
	s, store := *e.S(), *e.Store()
	s.WithExecutionV2Enabled(true).WithExecutionTasksEnabled(true).WithExecutionPipelinesEnabled(true)
	request := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "live", Version: "v1", Pipeline: &api.ExecutionPipelineOptions{}, Steps: []api.CreateManagedExecutionWorkflowStep{{Label: "work", Request: apidserver.ExecutionRequest()}}}
	accepted := e.Do(t, http.MethodPost, "/v2/execution-workflows", request, nil)
	var parent api.ManagedExecutionWorkflowResponse
	if accepted.Code != 202 || json.Unmarshal(accepted.Body.Bytes(), &parent) != nil || parent.ID == "" {
		t.Fatal(accepted.Code, accepted.Body.String())
	}
	path := "/v1/execution-pipelines/" + parent.ID + "/events"
	initial := e.Do(t, http.MethodGet, path, nil, nil)
	var first api.ExecutionPipelineEventsResponse
	if initial.Code != 200 || json.Unmarshal(initial.Body.Bytes(), &first) != nil || len(first.Events) == 0 || first.Terminal {
		t.Fatal(initial.Code, initial.Body.String())
	}
	drive := func() {
		t.Helper()
		claim, err := store.ClaimExecutionPipeline(t.Context(), "recreated-apid", time.Now().Add(3*time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.DriveExecutionPipelineForTest(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
	}
	finish := func(failed bool) {
		t.Helper()
		run, err := store.ClaimExecution(t.Context(), "schedd", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.MarkExecutionRunning(t.Context(), run.ID, *run.LeaseToken, time.Now()); err != nil {
			t.Fatal(err)
		}
		p := state.CompleteExecutionParams{ID: run.ID, LeaseToken: *run.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`), Usage: api.ExecutionUsage{WallTimeMS: 1}, FinishedAt: time.Now()}
		if failed {
			code, message := "guest_error", "failed"
			p.Status = api.ExecutionStatusFailed
			p.FailureCode = &code
			p.FailureMessage = &message
			p.Result = nil
		}
		if _, err = store.CompleteExecution(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	drive()
	finish(true)
	drive()
	resume := e.Do(t, http.MethodGet, path+"?after=1", nil, nil)
	var page api.ExecutionPipelineEventsResponse
	if resume.Code != 200 || json.Unmarshal(resume.Body.Bytes(), &page) != nil || !page.Terminal || page.Events[0].Sequence <= 1 {
		t.Fatal(resume.Code, resume.Body.String())
	}
	stream := e.Do(t, http.MethodGet, path, nil, map[string]string{"Accept": "text/event-stream"})
	if stream.Code != 200 || !strings.Contains(stream.Body.String(), "event: terminal") {
		t.Fatal(stream.Code, stream.Body.String())
	}
	bad := api.RerunExecutionPipelineRequest{WorkflowID: "recovery", Original: request}
	if r := e.Do(t, http.MethodPost, "/v2/execution-pipelines/"+parent.ID+"/reruns", bad, nil); r.Code != 409 {
		t.Fatal("missing consent", r.Code, r.Body.String())
	}
	bad.RepeatSteps = []string{"work"}
	created := e.Do(t, http.MethodPost, "/v2/execution-pipelines/"+parent.ID+"/reruns", bad, nil)
	var child api.ManagedExecutionWorkflowResponse
	if created.Code != 202 || json.Unmarshal(created.Body.Bytes(), &child) != nil || child.ID == parent.ID || child.Pipeline.Rerun.ParentID != parent.ID {
		t.Fatal(created.Code, created.Body.String())
	}
	drive()
	finish(false)
	drive()
	s.WithExecutionPipelinesEnabled(false)
	replay := e.Do(t, http.MethodPost, "/v2/execution-pipelines/"+parent.ID+"/reruns", bad, nil)
	if replay.Code != 202 || !strings.Contains(replay.Body.String(), child.ID) {
		t.Fatal("rollback replay", replay.Code, replay.Body.String())
	}
	if r := e.Do(t, http.MethodGet, path+"?after=99999", nil, nil); r.Code != 400 {
		t.Fatal("future cursor", r.Code, r.Body.String())
	}
}

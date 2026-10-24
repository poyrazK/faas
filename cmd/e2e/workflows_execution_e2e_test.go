package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

// Jobs and workflows acceptance (Hunt 13 H13-5). TestE2E_Workflows_Lifecycle
// only drives apid's run API; these runs execute their steps through the
// production path — apid, schedd's orchestrator, gatewayd-internal's synth
// bridge and the vmmd forward stream — into a guest HTTP handler.

// workflowGuest is the app behind the fake vmmd. It records every request
// the bridge delivers and answers by path.
type workflowGuest struct {
	mu       sync.Mutex
	requests []e2etest.RequestCapture
	answer   func(e2etest.RequestCapture) (any, int)
}

func (g *workflowGuest) handle(_ context.Context, capture e2etest.RequestCapture) (e2etest.FakeResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, capture)
	g.mu.Unlock()
	value, status := g.answer(capture)
	body, err := json.Marshal(value)
	if err != nil {
		return e2etest.FakeResponse{}, err
	}
	return e2etest.FakeResponse{Status: status, Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}}, Body: body}, nil
}

func (g *workflowGuest) requestsFor(uri string) []e2etest.RequestCapture {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []e2etest.RequestCapture
	for _, request := range g.requests {
		if request.Init.GetRequestUri() == uri {
			out = append(out, request)
		}
	}
	return out
}

func guestHeader(init *vmmdpb.ForwardHTTPRequestInit, name string) string {
	for _, header := range init.GetHeaders() {
		if strings.EqualFold(header.GetName(), name) {
			return header.GetValue()
		}
	}
	return ""
}

func newWorkflowFixture(t *testing.T, slug string, specs []api.WorkflowSpec, guest *workflowGuest) *normalPathFixture {
	t.Helper()
	f := newNormalPathFixtureWithPlanAndEnv(t, slug, api.PlanHobby, "FAAS_WORKFLOWS_ENABLED=1")
	if f == nil {
		return nil
	}
	f.vmmd.SetForwardHandler("", guest.handle)
	definitions, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID: f.app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("7", 64), Workflows: definitions,
	})
	if err != nil {
		t.Fatalf("create workflow deployment: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark workflow deployment live: %v", err)
	}
	publishNormalPathLayer(t, f, dep.ID)
	if _, err := f.store.CreateInstance(f.ctx, f.app.ID, dep.ID, string(state.StateRunning), f.app.RAMMB, f.nodeID, ""); err != nil {
		t.Fatalf("create workflow instance: %v", err)
	}
	return f
}

func startWorkflowRun(t *testing.T, f *normalPathFixture, workflow string, input any) api.WorkflowRunResponse {
	t.Helper()
	body, status := doReq(t, f.h, f.key, http.MethodPost, fmt.Sprintf("/v1/apps/%s/workflows/%s/runs", f.app.Slug, workflow), input)
	if status != http.StatusCreated {
		t.Fatalf("start %s: status=%d body=%s", workflow, status, body)
	}
	var run api.WorkflowRunResponse
	if err := json.Unmarshal(body, &run); err != nil {
		t.Fatalf("decode run: %v body=%s", err, body)
	}
	return run
}

// waitWorkflow polls the run and its steps until done reports true.
func waitWorkflow(t *testing.T, f *normalPathFixture, runID string, timeout time.Duration, done func(api.WorkflowRunResponse, map[string]string) bool) (api.WorkflowRunResponse, map[string]string) {
	t.Helper()
	var run api.WorkflowRunResponse
	steps := map[string]string{}
	deadline := time.Now().Add(timeout)
	for {
		body, status := doReq(t, f.h, f.key, http.MethodGet, "/v1/workflows/runs/"+runID, nil)
		if status != http.StatusOK {
			t.Fatalf("get run: status=%d body=%s", status, body)
		}
		if err := json.Unmarshal(body, &run); err != nil {
			t.Fatalf("decode run: %v body=%s", err, body)
		}
		body, status = doReq(t, f.h, f.key, http.MethodGet, "/v1/workflows/runs/"+runID+"/steps", nil)
		if status != http.StatusOK {
			t.Fatalf("get steps: status=%d body=%s", status, body)
		}
		var list api.ListWorkflowStepsResponse
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("decode steps: %v body=%s", err, body)
		}
		for _, step := range list.Steps {
			steps[step.StepName] = step.Status
		}
		if done(run, steps) {
			return run, steps
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s: status=%s steps=%v error=%v", runID, run.Status, steps, run.LastError)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestE2E_Workflows_DeclarativeStepsRunThroughTheGateway(t *testing.T) {
	guest := &workflowGuest{answer: func(capture e2etest.RequestCapture) (any, int) {
		switch capture.Init.GetRequestUri() {
		case "/charge":
			return map[string]string{"charge": "ch_1"}, http.StatusOK
		case "/ship":
			return map[string]bool{"shipped": true}, http.StatusOK
		}
		return map[string]string{"status": "ok"}, http.StatusOK
	}}
	f := newWorkflowFixture(t, "wf-declarative", []api.WorkflowSpec{{
		Name: "fulfil",
		Steps: []api.WorkflowStepSpec{
			{Name: "charge", Path: "/charge"},
			{Name: "ship", Path: "/ship", DependsOn: []string{"charge"}},
		},
	}}, guest)
	if f == nil {
		return
	}
	run := startWorkflowRun(t, f, "fulfil", map[string]string{"order": "o1"})
	final, steps := waitWorkflow(t, f, run.ID, 60*time.Second, func(r api.WorkflowRunResponse, _ map[string]string) bool {
		return r.Status == state.WorkflowRunStatusSucceeded || r.Status == state.WorkflowRunStatusFailed || r.Status == state.WorkflowRunStatusDead
	})
	if final.Status != state.WorkflowRunStatusSucceeded || steps["charge"] != state.WorkflowStepStatusSucceeded || steps["ship"] != state.WorkflowStepStatusSucceeded {
		t.Fatalf("run=%s steps=%v error=%v", final.Status, steps, final.LastError)
	}
	for _, step := range []string{"charge", "ship"} {
		requests := guest.requestsFor("/" + step)
		if len(requests) != 1 {
			t.Fatalf("%s reached the guest %d times, want once", step, len(requests))
		}
		init := requests[0].Init
		if init.GetMethod() != http.MethodPost || guestHeader(init, api.WorkflowRunIDHeader) != run.ID ||
			guestHeader(init, api.WorkflowStepHeader) != step || guestHeader(init, api.WorkflowAttemptHeader) != "1" ||
			guestHeader(init, "Idempotency-Key") != "workflow/"+run.ID+"/"+step {
			t.Fatalf("%s request = %s %s headers %v", step, init.GetMethod(), init.GetRequestUri(), init.GetHeaders())
		}
	}
}

// errGuestSuspend unwinds the guest's workflow function at its first new
// operation, as the Go SDK does.
var errGuestSuspend = errors.New("suspend")

// codeGuest replays a journal the way the SDKs do (pkg/workflowcode is the
// wire format); the SDKs' own suites test their side of the same protocol.
type codeGuest struct {
	journal []workflowcode.Entry
	seq     int
	ops     []workflowcode.ReportedOp
}

func (g *codeGuest) claim(name string, kind workflowcode.Kind) (workflowcode.Entry, bool) {
	seq := g.seq
	g.seq++
	if seq >= len(g.journal) {
		return workflowcode.Entry{Seq: seq, Name: name, Kind: kind}, false
	}
	return g.journal[seq], true
}

func (g *codeGuest) step(name string, value any) json.RawMessage {
	entry, recorded := g.claim(name, workflowcode.KindStep)
	if recorded && entry.Status == workflowcode.EntrySucceeded {
		return entry.Output
	}
	output, _ := json.Marshal(value)
	g.ops = append(g.ops, workflowcode.ReportedOp{Seq: entry.Seq, Name: name, Kind: workflowcode.KindStep, Status: workflowcode.ResultSucceeded, Output: output})
	panic(errGuestSuspend)
}

func (g *codeGuest) sleep(name string, d time.Duration) {
	entry, recorded := g.claim(name, workflowcode.KindSleep)
	if recorded && entry.Status == workflowcode.EntrySucceeded {
		return
	}
	if !recorded {
		g.ops = append(g.ops, workflowcode.ReportedOp{Seq: entry.Seq, Name: name, Kind: workflowcode.KindSleep, DurationMS: d.Milliseconds()})
	}
	panic(errGuestSuspend)
}

func (g *codeGuest) waitForEvent(name, event string, timeout time.Duration) (json.RawMessage, bool) {
	entry, recorded := g.claim(name, workflowcode.KindEvent)
	if recorded && entry.Status == workflowcode.EntrySucceeded {
		return entry.Output, entry.TimedOut
	}
	if !recorded {
		g.ops = append(g.ops, workflowcode.ReportedOp{Seq: entry.Seq, Name: name, Kind: workflowcode.KindEvent, Event: event, TimeoutMS: timeout.Milliseconds()})
	}
	panic(errGuestSuspend)
}

// orderWorkflow charges, cools off for a second, then waits for approval.
func orderWorkflow(g *codeGuest) any {
	charge := g.step("charge", map[string]string{"id": "ch_1"})
	g.sleep("cool_off", time.Second)
	approval, timedOut := g.waitForEvent("approval", "approved", 5*time.Minute)
	if timedOut {
		return map[string]string{"status": "expired"}
	}
	return map[string]json.RawMessage{"charge": charge, "approval": approval}
}

func answerCodeWorkflow(capture e2etest.RequestCapture) (any, int) {
	// The SDKs refuse callers without Gregale's workflow headers.
	if guestHeader(capture.Init, api.WorkflowCodeModeHeader) != "code" || guestHeader(capture.Init, api.WorkflowRunIDHeader) == "" {
		return map[string]string{"error": "not a Gregale workflow call"}, http.StatusForbidden
	}
	var request workflowcode.Request
	if err := json.Unmarshal(capture.Body, &request); err != nil || request.Protocol != api.WorkflowCodeProtocol {
		return map[string]string{"error": "bad request"}, http.StatusBadRequest
	}
	guest := &codeGuest{journal: request.Journal}
	result := workflowcode.Result{Status: workflowcode.ResultRunning}
	func() {
		defer func() {
			if r := recover(); r != nil && r != errGuestSuspend { //nolint:errorlint // sentinel identity, not a wrapped error
				panic(r)
			}
		}()
		output, _ := json.Marshal(orderWorkflow(guest))
		result = workflowcode.Result{Status: workflowcode.ResultSucceeded, Output: output}
	}()
	return workflowcode.Response{Protocol: api.WorkflowCodeProtocol, Ops: guest.ops, Result: result}, http.StatusOK
}

func TestE2E_Workflows_CodeFirstRunResumesAcrossSleepAndEvent(t *testing.T) {
	guest := &workflowGuest{answer: func(capture e2etest.RequestCapture) (any, int) {
		if capture.Init.GetRequestUri() == "/order" {
			return answerCodeWorkflow(capture)
		}
		return map[string]string{"status": "ok"}, http.StatusOK
	}}
	f := newWorkflowFixture(t, "wf-code", []api.WorkflowSpec{{Name: "order", Code: &api.WorkflowCodeSpec{Run: "order"}}}, guest)
	if f == nil {
		return
	}
	run := startWorkflowRun(t, f, "order", map[string]string{"order": "o1"})
	parked, steps := waitWorkflow(t, f, run.ID, 60*time.Second, func(r api.WorkflowRunResponse, steps map[string]string) bool {
		return steps["approval"] == state.WorkflowStepStatusAwaitingEvent || r.Status == state.WorkflowRunStatusDead || r.Status == state.WorkflowRunStatusFailed
	})
	if parked.Status != state.WorkflowRunStatusAwaitingEvent || steps["charge"] != state.WorkflowStepStatusSucceeded || steps["cool_off"] != state.WorkflowStepStatusSucceeded {
		t.Fatalf("before the event: run=%s steps=%v error=%v", parked.Status, steps, parked.LastError)
	}

	// A public caller cannot pose as the workflow engine: the edge strips
	// x-faas-* headers, so the handler sees an ordinary request and refuses.
	_, _, status := doReqHeaders(t, f.h, f.host, http.MethodPost, "/order", workflowcode.Request{Protocol: api.WorkflowCodeProtocol, RunID: run.ID, Workflow: "order"},
		map[string]string{api.WorkflowCodeModeHeader: "code", api.WorkflowRunIDHeader: run.ID, api.WorkflowStepHeader: api.WorkflowCodeDriverStep})
	if status != http.StatusForbidden {
		t.Fatalf("forged public workflow call: status=%d, want the handler's 403", status)
	}

	body, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/workflows/runs/"+run.ID+"/events",
		api.InjectWorkflowEventRequest{EventName: "approved", Payload: json.RawMessage(`{"ok":true}`)})
	if status != http.StatusOK {
		t.Fatalf("send event: status=%d body=%s", status, body)
	}
	final, steps := waitWorkflow(t, f, run.ID, 60*time.Second, func(r api.WorkflowRunResponse, _ map[string]string) bool {
		return r.Status == state.WorkflowRunStatusSucceeded || r.Status == state.WorkflowRunStatusFailed || r.Status == state.WorkflowRunStatusDead
	})
	if final.Status != state.WorkflowRunStatusSucceeded || !normalPathJSONEqual(final.Output, []byte(`{"approval":{"ok":true},"charge":{"id":"ch_1"}}`)) {
		t.Fatalf("final run=%s output=%s steps=%v error=%v", final.Status, final.Output, steps, final.LastError)
	}

	keys := map[string]bool{}
	engineCalls := 0
	for _, request := range guest.requestsFor("/order") {
		init := request.Init
		if guestHeader(init, api.WorkflowRunIDHeader) == "" {
			if guestHeader(init, api.WorkflowCodeModeHeader) != "" || guestHeader(init, api.WorkflowStepHeader) != "" {
				t.Fatalf("the public edge let workflow headers through: %v", init.GetHeaders())
			}
			continue
		}
		engineCalls++
		key := guestHeader(init, "Idempotency-Key")
		if guestHeader(init, api.WorkflowCodeModeHeader) != "code" || guestHeader(init, api.WorkflowStepHeader) != api.WorkflowCodeDriverStep || keys[key] {
			t.Fatalf("engine call %d headers %v", engineCalls, init.GetHeaders())
		}
		keys[key] = true
	}
	// charge, cool_off, the approval wait, then the finish.
	if engineCalls < 4 {
		t.Fatalf("engine called the entry handler %d times, want at least 4", engineCalls)
	}
}

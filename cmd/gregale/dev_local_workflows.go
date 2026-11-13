package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

// ADR-20261113120824 §4: `gregale dev --local` drives workflow functions on an
// in-process driver. It replays each run's journal against the local
// function under the engine's own validation (pkg/workflowcode); steps and
// wf.call really run, sleeps are scaled by --workflow-sleep-scale (default:
// they finish at once), and waits take events from `gregale dev trigger
// --local workflow FN --event NAME` or a run handle's signal().

// devLocalWorkflowMaxInvocations stops a run that never finishes.
const devLocalWorkflowMaxInvocations = 1000

type devLocalWorkflows struct {
	mu   sync.Mutex
	runs map[string]*devLocalRun
	// order lists run ids by start, for "the function's latest run".
	order []string
}

type devLocalRun struct {
	id, function, starter string
	input                 json.RawMessage

	mu     sync.Mutex
	status string
	output json.RawMessage
	err    string
	events map[string][]json.RawMessage
	notify chan struct{} // closed and replaced when an event arrives
	done   chan struct{}
}

func (d *devLocalWorkflows) add(run *devLocalRun) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.runs == nil {
		d.runs = map[string]*devLocalRun{}
	}
	d.runs[run.id] = run
	d.order = append(d.order, run.id)
}

func (d *devLocalWorkflows) get(id string) *devLocalRun {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runs[id]
}

// latest returns the function's most recent unfinished run.
func (d *devLocalWorkflows) latest(function string) *devLocalRun {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.order) - 1; i >= 0; i-- {
		run := d.runs[d.order[i]]
		if run.function == function && !run.finished() {
			return run
		}
	}
	return nil
}

func (r *devLocalRun) finished() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

func (r *devLocalRun) snapshot() api.FunctionWorkflowRunStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return api.FunctionWorkflowRunStatus{ID: r.id, Status: r.status, Done: r.status == workflowcode.ResultSucceeded || r.status == workflowcode.ResultFailed,
		Output: r.output, Error: r.err}
}

func (r *devLocalRun) signal(event string, payload json.RawMessage) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished() {
		return false
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	r.events[event] = append(r.events[event], payload)
	close(r.notify)
	r.notify = make(chan struct{})
	return true
}

// takeEvent waits up to timeout for an event of that name; ok is false on
// timeout.
func (r *devLocalRun) takeEvent(event string, timeout time.Duration) (json.RawMessage, bool) {
	deadline := time.Now().Add(timeout)
	for {
		r.mu.Lock()
		if queued := r.events[event]; len(queued) > 0 {
			r.events[event] = queued[1:]
			r.mu.Unlock()
			return queued[0], true
		}
		notify := r.notify
		r.mu.Unlock()
		left := time.Until(deadline)
		if left <= 0 {
			return nil, false
		}
		timer := time.NewTimer(left)
		select {
		case <-notify:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// startWorkflowRun starts a run of fn and drives it in the background.
func (s *devLocalServer) startWorkflowRun(fn *localFunction, starter string, input json.RawMessage) *devLocalRun {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	run := &devLocalRun{id: uuid.NewString(), function: fn.Name, starter: starter, input: input, status: "running",
		events: map[string][]json.RawMessage{}, notify: make(chan struct{}), done: make(chan struct{})}
	s.workflows.add(run)
	_, _ = fmt.Fprintf(s.out, "%s workflow run %s started\n", fn.Name, run.id)
	go s.driveWorkflowRun(fn, run)
	return run
}

func (s *devLocalServer) finishWorkflowRun(run *devLocalRun, status string, output json.RawMessage, message string) {
	run.mu.Lock()
	run.status, run.output, run.err = status, output, message
	run.mu.Unlock()
	close(run.done)
	if message != "" {
		_, _ = fmt.Fprintf(s.out, "%s workflow run %s %s: %s\n", run.function, run.id, status, message)
	} else {
		_, _ = fmt.Fprintf(s.out, "%s workflow run %s %s\n", run.function, run.id, status)
	}
}

// driveWorkflowRun is the engine's loop, in process: call the code with the
// journal, validate and record what it reports, resolve waits, repeat.
func (s *devLocalServer) driveWorkflowRun(fn *localFunction, run *devLocalRun) {
	code := fn.Workflow.Code
	limits := workflowcode.DefaultLimits(30*24*time.Hour, api.WorkflowCodeRetry(*code))
	var rows []workflowcode.Row
	index := map[string]int{}
	// callInputs keeps each call's reported input until it runs; the
	// journal descriptor does not carry it.
	callInputs := map[string]json.RawMessage{}
	for invocation := 1; invocation <= devLocalWorkflowMaxInvocations; invocation++ {
		journal, err := workflowcode.BuildJournal(rows)
		if err != nil {
			s.finishWorkflowRun(run, workflowcode.ResultFailed, nil, err.Error())
			return
		}
		response, err := s.callWorkflowCode(fn, run, invocation, journal)
		if err != nil {
			s.finishWorkflowRun(run, workflowcode.ResultFailed, nil, err.Error())
			return
		}
		plan, err := workflowcode.Validate(journal, response, limits)
		if err != nil {
			s.finishWorkflowRun(run, workflowcode.ResultFailed, nil, err.Error())
			return
		}
		for _, op := range plan.Ops {
			row := workflowcode.Row{Name: op.Name, Status: workflowcode.RowPending, Attempt: op.Attempt, Input: workflowcode.EncodeDescriptor(op.Descriptor)}
			if op.Kind == workflowcode.KindCall {
				callInputs[op.Name] = op.Input
			}
			if op.Kind == workflowcode.KindStep {
				switch {
				case op.Status == workflowcode.ResultSucceeded:
					row.Status, row.Output = workflowcode.RowSucceeded, op.Output
				case workflowcode.ShouldRetry(op):
					message := op.Error
					row.Error = &message
				default:
					message := op.Error
					row.Status, row.Error = workflowcode.RowFailed, &message
				}
			}
			if i, ok := index[op.Name]; ok {
				rows[i] = row
			} else {
				index[op.Name] = len(rows)
				rows = append(rows, row)
			}
		}
		if plan.Result.Status != workflowcode.ResultRunning {
			message := plan.Result.Error
			if plan.Result.Status == workflowcode.ResultFailed && message == "" {
				message = "workflow function failed"
			}
			s.finishWorkflowRun(run, plan.Result.Status, plan.Result.Output, message)
			return
		}
		for i := range rows {
			if rows[i].Status == workflowcode.RowPending && rows[i].Error == nil {
				s.resolveWorkflowWait(fn, run, &rows[i], callInputs[rows[i].Name])
			}
		}
	}
	s.finishWorkflowRun(run, workflowcode.ResultFailed, nil, fmt.Sprintf("workflow did not finish within %d invocations", devLocalWorkflowMaxInvocations))
}

// callWorkflowCode POSTs one entry-handler request to the local function.
func (s *devLocalServer) callWorkflowCode(fn *localFunction, run *devLocalRun, invocation int, journal workflowcode.Journal) (workflowcode.Response, error) {
	body, err := json.Marshal(workflowcode.Request{Protocol: api.WorkflowCodeProtocol, RunID: run.id, Workflow: fn.Workflow.Name,
		Input: run.input, Invocation: invocation, Journal: journal.Sent(time.Now().UTC())})
	if err != nil {
		return workflowcode.Response{}, err
	}
	req := httptest.NewRequest(http.MethodPost, api.WorkflowCodePath(*fn.Workflow.Code), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	platform := http.Header{}
	platform.Set(api.WorkflowCodeModeHeader, "code")
	platform.Set("X-Faas-Workflow-Run-Id", run.id)
	platform.Set("X-Faas-Workflow-Step", api.WorkflowCodeDriverStep)
	platform.Set("X-Faas-Workflow-Attempt", strconv.Itoa(invocation))
	rec := httptest.NewRecorder()
	s.invokeWith(rec, req, fn, "", platform)
	if rec.Code < 200 || rec.Code > 299 {
		return workflowcode.Response{}, fmt.Errorf("entry handler answered %d: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var response workflowcode.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		return workflowcode.Response{}, fmt.Errorf("entry handler answer is not the workflow protocol: %w", err)
	}
	return response, nil
}

// resolveWorkflowWait settles a pending sleep, event wait or call.
func (s *devLocalServer) resolveWorkflowWait(fn *localFunction, run *devLocalRun, row *workflowcode.Row, callInput json.RawMessage) {
	descriptor, _ := workflowcode.DecodeDescriptor(row.Input)
	switch descriptor.Kind {
	case workflowcode.KindSleep:
		time.Sleep(time.Duration(float64(descriptor.Duration()) * s.sleepScale))
		row.Status = workflowcode.RowSucceeded
	case workflowcode.KindEvent:
		timeout := descriptor.Timeout()
		if s.sleepScale > 0 {
			timeout = time.Duration(float64(timeout) * s.sleepScale)
		}
		_, _ = fmt.Fprintf(s.out, "%s workflow run %s waits for event %q (gregale dev trigger --local workflow %s --event %s)\n", fn.Name, run.id, descriptor.Event, fn.Name, descriptor.Event)
		payload, ok := run.takeEvent(descriptor.Event, timeout)
		row.Status = workflowcode.RowSucceeded
		if ok {
			row.Output = payload
		}
	case workflowcode.KindCall:
		output, message := s.localWorkflowCall(fn, run, row.Name, descriptor.Function, callInput)
		if message != "" {
			row.Status, row.Error = workflowcode.RowFailed, &message
			return
		}
		row.Status, row.Output = workflowcode.RowSucceeded, output
	}
}

// localWorkflowCall runs wf.call against a local sibling, as the platform's
// async invocation would: the callee's input check and its answer.
func (s *devLocalServer) localWorkflowCall(fn *localFunction, run *devLocalRun, op, name string, input json.RawMessage) (json.RawMessage, string) {
	target, ok := fn.Calls[name]
	if !ok {
		return nil, fmt.Sprintf("function %q is not callable from this workflow", name)
	}
	callee := s.byName[target]
	if callee == nil {
		return nil, fmt.Sprintf("function %q is a calls: app, which is not served locally", name)
	}
	if len(input) == 0 {
		input = json.RawMessage(`null`)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(input))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.FunctionCallHeader, "1")
	req.Header.Set("Idempotency-Key", "workflow/"+run.id+"/"+op)
	rec := httptest.NewRecorder()
	s.invokeCall(rec, req, callee, "async_invoke")
	body := bytes.TrimSpace(rec.Body.Bytes())
	if rec.Code < 200 || rec.Code > 299 {
		return nil, fmt.Sprintf("HTTP %d: %s", rec.Code, body)
	}
	switch {
	case len(body) == 0:
		return json.RawMessage(`null`), ""
	case json.Valid(body):
		return append(json.RawMessage(nil), body...), ""
	default:
		quoted, _ := json.Marshal(string(body))
		return quoted, ""
	}
}

// serveWorkflowRuns answers the platform's workflow-runs routes for the
// local caller (ADR-20261113120824 §3): start, read with ?wait, events.
func (s *devLocalServer) serveWorkflowRuns(w http.ResponseWriter, r *http.Request, callerName, rest string) {
	id, action, _ := strings.Cut(rest, "/")
	switch {
	case id == "" && r.Method == http.MethodPost:
		var req struct {
			Function string          `json:"function"`
			Input    json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req); err != nil || req.Function == "" {
			writeDevProblem(w, http.StatusBadRequest, api.CodeValidation, "body must be JSON with function and an optional input")
			return
		}
		caller := s.byName[callerName]
		target, ok := "", false
		if caller != nil {
			target, ok = caller.Calls[req.Function]
		}
		fn := s.byName[target]
		switch {
		case !ok:
			writeDevProblem(w, http.StatusBadRequest, api.CodeValidation, fmt.Sprintf("function %s is not one %s may call", req.Function, callerName))
		case fn == nil || fn.Workflow == nil:
			writeDevProblem(w, http.StatusNotFound, api.CodeNotFound, fmt.Sprintf("function %s is not a local workflow function", req.Function))
		default:
			run := s.startWorkflowRun(fn, callerName, req.Input)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(run.snapshot())
		}
	case id != "" && action == "" && r.Method == http.MethodGet:
		run := s.workflows.get(id)
		if run == nil || run.starter != callerName {
			writeDevProblem(w, http.StatusNotFound, api.CodeNotFound, "workflow run not found")
			return
		}
		wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
		if wait > 0 {
			timer := time.NewTimer(time.Duration(min(wait, 30)) * time.Second)
			select {
			case <-run.done:
			case <-timer.C:
			case <-r.Context().Done():
			}
			timer.Stop()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(run.snapshot())
	case id != "" && action == "events" && r.Method == http.MethodPost:
		run := s.workflows.get(id)
		if run == nil || run.starter != callerName {
			writeDevProblem(w, http.StatusNotFound, api.CodeNotFound, "workflow run not found")
			return
		}
		var req struct {
			Event   string          `json:"event"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Event == "" {
			writeDevProblem(w, http.StatusBadRequest, api.CodeValidation, "body must be JSON with event and an optional payload")
			return
		}
		if !run.signal(req.Event, req.Payload) {
			writeDevProblem(w, http.StatusConflict, api.CodeConflict, "the run no longer accepts events")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		http.NotFound(w, r)
	}
}

// serveWorkflowTrigger handles `gregale dev trigger --local workflow FN`:
// start a run and wait for it, or send an event to the latest run.
func (s *devLocalServer) serveWorkflowTrigger(w http.ResponseWriter, fn *localFunction, t devLocalTrigger) {
	if fn.Workflow == nil {
		http.Error(w, fmt.Sprintf("%s is not a workflow function (set workflow: true)", fn.Name), http.StatusBadRequest)
		return
	}
	result := devLocalTriggerResult{Status: http.StatusOK}
	if t.Event != "" {
		run := s.workflows.latest(fn.Name)
		if run == nil || !run.signal(t.Event, json.RawMessage(nonEmptyJSON(t.Body))) {
			http.Error(w, fmt.Sprintf("%s has no unfinished workflow run", fn.Name), http.StatusConflict)
			return
		}
		result.Body = fmt.Sprintf("event %q sent to run %s", t.Event, run.id)
	} else {
		run := s.startWorkflowRun(fn, "", json.RawMessage(nonEmptyJSON(t.Body)))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		select {
		case <-run.done:
		case <-ctx.Done():
		}
		raw, _ := json.Marshal(run.snapshot())
		result.Body = string(raw)
		if st := run.snapshot(); st.Done && st.Status != workflowcode.ResultSucceeded {
			result.Status = http.StatusInternalServerError
		}
	}
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func nonEmptyJSON(body string) string {
	if strings.TrimSpace(body) == "" {
		return "{}"
	}
	return body
}

func writeDevProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "code": code, "detail": detail})
}

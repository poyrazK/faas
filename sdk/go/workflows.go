// Code-first workflows (ADR-20261021204307).
//
// A workflow is a Go function that calls WorkflowStep, Workflow.Sleep and
// WorkflowWaitForEvent. Gregale calls the workflow's entry handler at every
// durable boundary with the run's journal. Recorded operations return their
// stored result; the first new one runs (a step) or is reported (a wait), and
// the function is unwound until Gregale calls again. Code outside steps must
// therefore be deterministic, must not perform I/O, and must call the
// Workflow only from the goroutine Gregale started it on: use WorkflowParallel
// to run steps concurrently.
package faas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"
)

// WorkflowCodeProtocol names the entry-handler envelope version.
const WorkflowCodeProtocol = "gregale.workflow.code/v1"

// WorkflowResponseMaxBytes bounds one call's answer, the platform's limit.
const WorkflowResponseMaxBytes = 960 * 1024

const workflowRequestMaxBytes = 8 << 20

var workflowOpName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:#/-]{0,127}$`)

// ErrUnknownWorkflow reports a request for a workflow this handler does not serve.
var ErrUnknownWorkflow = errors.New("faas: unknown workflow")

// WorkflowJournalEntry is one recorded operation sent by Gregale.
type WorkflowJournalEntry struct {
	Seq      int             `json:"seq"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Status   string          `json:"status"`
	Attempt  int             `json:"attempt,omitempty"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
	TimedOut bool            `json:"timed_out,omitempty"`
	Event    string          `json:"event,omitempty"`
}

// WorkflowInvocationRequest is the envelope Gregale POSTs to the entry handler.
type WorkflowInvocationRequest struct {
	Protocol   string                 `json:"protocol"`
	RunID      string                 `json:"run_id"`
	Workflow   string                 `json:"workflow"`
	Input      json.RawMessage        `json:"input"`
	Invocation int                    `json:"invocation"`
	Journal    []WorkflowJournalEntry `json:"journal"`
}

// WorkflowRetryPolicy is a step's total attempts (1-25) and back-off
// ("fixed" or "exponential").
type WorkflowRetryPolicy struct {
	MaxAttempts int    `json:"max_attempts"`
	Backoff     string `json:"backoff,omitempty"`
}

// WorkflowReportedOp is one operation in the handler's answer.
type WorkflowReportedOp struct {
	Seq          int                  `json:"seq"`
	Name         string               `json:"name"`
	Kind         string               `json:"kind"`
	Status       string               `json:"status,omitempty"`
	Output       json.RawMessage      `json:"output,omitempty"`
	Error        string               `json:"error,omitempty"`
	NonRetryable bool                 `json:"non_retryable,omitempty"`
	Retry        *WorkflowRetryPolicy `json:"retry,omitempty"`
	DurationMS   int64                `json:"duration_ms,omitempty"`
	Event        string               `json:"event,omitempty"`
	TimeoutMS    int64                `json:"timeout_ms,omitempty"`
}

// WorkflowResult is the workflow function's state after a call: running,
// succeeded (with Output) or failed (with Error).
type WorkflowResult struct {
	Status string          `json:"status"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// WorkflowInvocationResponse is the handler's answer.
type WorkflowInvocationResponse struct {
	Protocol string               `json:"protocol"`
	Ops      []WorkflowReportedOp `json:"ops"`
	Result   WorkflowResult       `json:"result"`
}

// WorkflowFunc is a workflow body. input is the run's JSON input.
type WorkflowFunc func(ctx context.Context, wf *Workflow, input json.RawMessage) (any, error)

// WorkflowDefinition names a workflow body; Name matches gregale.yaml.
type WorkflowDefinition struct {
	Name string
	Run  WorkflowFunc
}

// DefineWorkflow declares a code-first workflow.
func DefineWorkflow(name string, run WorkflowFunc) WorkflowDefinition {
	return WorkflowDefinition{Name: name, Run: run}
}

// WorkflowStepContext is passed to a step function.
type WorkflowStepContext struct {
	// Attempt is 1 on the first attempt.
	Attempt int
	// IdempotencyKey is workflow/<run-id>/<step>, stable across attempts.
	IdempotencyKey string
}

// WorkflowStepOption configures a step.
type WorkflowStepOption func(*WorkflowRetryPolicy)

// WithStepRetry overrides the workflow's code.retry policy for one step.
func WithStepRetry(maxAttempts int, backoff string) WorkflowStepOption {
	return func(policy *WorkflowRetryPolicy) {
		policy.MaxAttempts, policy.Backoff = maxAttempts, backoff
	}
}

// WorkflowStepError is returned by a step that exhausted its attempts.
type WorkflowStepError struct {
	Step     string
	Message  string
	Attempts int
}

func (e *WorkflowStepError) Error() string { return e.Message }

type nonRetryableError struct{ err error }

func (e nonRetryableError) Error() string { return e.err.Error() }
func (e nonRetryableError) Unwrap() error { return e.err }

// NonRetryable marks a step error that must not be retried.
func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryableError{err: err}
}

// workflowSuspend unwinds the workflow function at its first new operation.
type workflowSuspend struct{}

// Workflow is the handle a workflow function uses for durable operations.
type Workflow struct {
	ctx       context.Context
	request   WorkflowInvocationRequest
	seq       int
	names     map[string]int
	mu        sync.Mutex
	ops       []WorkflowReportedOp
	suspended bool
}

// RunID is the workflow run's id.
func (wf *Workflow) RunID() string { return wf.request.RunID }

// Name is the workflow's name.
func (wf *Workflow) Name() string { return wf.request.Workflow }

// Invocation is 1 on the first call of this run's entry handler.
func (wf *Workflow) Invocation() int { return wf.request.Invocation }

func (wf *Workflow) claim(name, kind, event string) (int, string, *WorkflowJournalEntry, bool) {
	if !workflowOpName.MatchString(name) {
		panic(fmt.Errorf("invalid workflow operation name %q: use 1-128 of A-Z a-z 0-9 _ . : # / - starting with a letter or digit", name))
	}
	wf.names[name]++
	opName := name
	if count := wf.names[name]; count > 1 {
		opName = fmt.Sprintf("%s#%d", name, count)
	}
	seq := wf.seq
	wf.seq++
	if seq >= len(wf.request.Journal) {
		return seq, opName, nil, false
	}
	entry := &wf.request.Journal[seq]
	if entry.Name != opName || entry.Kind != kind || (kind == "event" && entry.Event != event) {
		// Report what the code asked for; Gregale refuses the replay.
		return seq, opName, nil, true
	}
	return seq, opName, entry, false
}

func (wf *Workflow) report(op WorkflowReportedOp) {
	wf.mu.Lock()
	wf.ops = append(wf.ops, op)
	wf.mu.Unlock()
}

func (wf *Workflow) suspend() {
	wf.suspended = true
	panic(workflowSuspend{})
}

func (wf *Workflow) runStep(seq int, name string, attempt int, fn func(context.Context, WorkflowStepContext) (any, error), retry *WorkflowRetryPolicy) (op WorkflowReportedOp) {
	op = WorkflowReportedOp{Seq: seq, Name: name, Kind: "step", Retry: retry}
	defer func() {
		if recovered := recover(); recovered != nil {
			op.Status, op.Error, op.Output = "failed", fmt.Sprintf("step panicked: %v", recovered), nil
		}
	}()
	value, err := fn(wf.ctx, WorkflowStepContext{Attempt: attempt, IdempotencyKey: "workflow/" + wf.request.RunID + "/" + name})
	if err != nil {
		op.Status, op.Error = "failed", err.Error()
		var permanent nonRetryableError
		op.NonRetryable = errors.As(err, &permanent)
		return op
	}
	output, err := json.Marshal(value)
	if err != nil {
		op.Status, op.Error, op.NonRetryable = "failed", "step result is not JSON: "+err.Error(), true
		return op
	}
	op.Status, op.Output = "succeeded", output
	return op
}

func stepRetry(opts []WorkflowStepOption) *WorkflowRetryPolicy {
	if len(opts) == 0 {
		return nil
	}
	policy := &WorkflowRetryPolicy{}
	for _, opt := range opts {
		opt(policy)
	}
	return policy
}

// WorkflowStep runs fn once (at least once on failure) and records its JSON
// result. A recorded result is decoded into T without running fn.
func WorkflowStep[T any](wf *Workflow, name string, fn func(context.Context, WorkflowStepContext) (T, error), opts ...WorkflowStepOption) (T, error) {
	var zero T
	seq, opName, entry, conflict := wf.claim(name, "step", "")
	switch {
	case conflict:
		wf.report(WorkflowReportedOp{Seq: seq, Name: opName, Kind: "step", Status: "succeeded"})
		wf.suspend()
	case entry != nil && entry.Status == "succeeded":
		return WorkflowDecode[T](entry.Output)
	case entry != nil && entry.Status == "failed":
		return zero, &WorkflowStepError{Step: opName, Message: entry.Error, Attempts: entry.Attempt}
	}
	attempt := 1
	if entry != nil {
		attempt = entry.Attempt + 1
	}
	wf.report(wf.runStep(seq, opName, attempt, func(ctx context.Context, sc WorkflowStepContext) (any, error) {
		return fn(ctx, sc)
	}, stepRetry(opts)))
	wf.suspend()
	return zero, nil
}

// WorkflowParallelStep is one step of WorkflowParallel.
type WorkflowParallelStep struct {
	Name  string
	Run   func(context.Context, WorkflowStepContext) (any, error)
	Retry *WorkflowRetryPolicy
}

// WorkflowParallelResult is one step's recorded JSON result or error.
type WorkflowParallelResult struct {
	Output json.RawMessage
	Err    error
}

// WorkflowParallel runs steps concurrently in one call and returns their
// results in order once all of them are recorded. Decode outputs with
// WorkflowDecode.
func WorkflowParallel(wf *Workflow, steps ...WorkflowParallelStep) []WorkflowParallelResult {
	results := make([]WorkflowParallelResult, len(steps))
	type pending struct {
		index, seq, attempt int
		name                string
	}
	var todo []pending
	conflict := false
	for i, step := range steps {
		seq, opName, entry, conflicted := wf.claim(step.Name, "step", "")
		switch {
		case conflicted:
			wf.report(WorkflowReportedOp{Seq: seq, Name: opName, Kind: "step", Status: "succeeded"})
			conflict = true
		case entry != nil && entry.Status == "succeeded":
			results[i].Output = entry.Output
		case entry != nil && entry.Status == "failed":
			results[i].Err = &WorkflowStepError{Step: opName, Message: entry.Error, Attempts: entry.Attempt}
		default:
			attempt := 1
			if entry != nil {
				attempt = entry.Attempt + 1
			}
			todo = append(todo, pending{index: i, seq: seq, attempt: attempt, name: opName})
		}
	}
	if conflict {
		wf.suspend()
	}
	if len(todo) == 0 {
		return results
	}
	ops := make([]WorkflowReportedOp, len(todo))
	var wg sync.WaitGroup
	for j, item := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ops[j] = wf.runStep(item.seq, item.name, item.attempt, steps[item.index].Run, steps[item.index].Retry)
		}()
	}
	wg.Wait()
	for _, op := range ops {
		wf.report(op)
	}
	wf.suspend()
	return results
}

// WorkflowDecode decodes a recorded JSON result; an empty result is T's zero value.
func WorkflowDecode[T any](raw json.RawMessage) (T, error) {
	var value T
	if len(raw) == 0 {
		return value, nil
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("faas: decode workflow result: %w", err)
	}
	return value, nil
}

// Sleep parks the run for d (1 s up to the plan's maximum wait); the app may
// scale to zero meanwhile.
func (wf *Workflow) Sleep(name string, d time.Duration) {
	seq, opName, entry, conflict := wf.claim(name, "sleep", "")
	if entry != nil && entry.Status == "succeeded" {
		return
	}
	if entry == nil {
		ms := d.Milliseconds()
		if conflict {
			ms = 1000
		}
		wf.report(WorkflowReportedOp{Seq: seq, Name: opName, Kind: "sleep", DurationMS: ms})
	}
	wf.suspend()
}

// WorkflowWaitOptions configures WorkflowWaitForEvent.
type WorkflowWaitOptions struct {
	// Event is the event name; it defaults to the operation name.
	Event string
	// Timeout defaults to the plan's maximum wait.
	Timeout time.Duration
}

// WorkflowWaitForEvent waits for the next event of that name sent to the
// run (gregale workflows events RUN_ID NAME, or POST
// /v1/workflows/runs/{id}/events). It reports false on timeout. Each wait
// for a name consumes one event of that name, in arrival order.
func WorkflowWaitForEvent[T any](wf *Workflow, name string, opts WorkflowWaitOptions) (T, bool, error) {
	var zero T
	event := opts.Event
	if event == "" {
		event = name
	}
	seq, opName, entry, conflict := wf.claim(name, "event", event)
	if entry != nil && entry.Status == "succeeded" {
		if entry.TimedOut {
			return zero, false, nil
		}
		value, err := WorkflowDecode[T](entry.Output)
		return value, true, err
	}
	if entry == nil {
		op := WorkflowReportedOp{Seq: seq, Name: opName, Kind: "event", Event: event}
		if opts.Timeout > 0 && !conflict {
			op.TimeoutMS = opts.Timeout.Milliseconds()
		}
		wf.report(op)
	}
	wf.suspend()
	return zero, false, nil
}

// RunWorkflowInvocation runs one call of a workflow against its journal. Use
// it to serve the protocol from any framework; ServeWorkflows wraps it.
func RunWorkflowInvocation(ctx context.Context, definitions []WorkflowDefinition, body []byte) (*WorkflowInvocationResponse, error) {
	var request WorkflowInvocationRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("faas: workflow request is not JSON: %w", err)
	}
	if request.Protocol != WorkflowCodeProtocol || request.Workflow == "" || request.RunID == "" {
		return nil, fmt.Errorf("faas: expected a %s request", WorkflowCodeProtocol)
	}
	var definition *WorkflowDefinition
	for i := range definitions {
		if definitions[i].Name == request.Workflow {
			definition = &definitions[i]
			break
		}
	}
	if definition == nil {
		return nil, fmt.Errorf("%w %q", ErrUnknownWorkflow, request.Workflow)
	}
	wf := &Workflow{ctx: ctx, request: request, names: map[string]int{}}
	result := runWorkflowBody(ctx, wf, *definition)
	ops := append([]WorkflowReportedOp(nil), wf.ops...)
	sort.Slice(ops, func(i, j int) bool { return ops[i].Seq < ops[j].Seq })
	return fitWorkflowResponse(&WorkflowInvocationResponse{Protocol: WorkflowCodeProtocol, Ops: ops, Result: result}), nil
}

func runWorkflowBody(ctx context.Context, wf *Workflow, definition WorkflowDefinition) (result WorkflowResult) {
	defer func() {
		recovered := recover()
		if _, ok := recovered.(workflowSuspend); ok || wf.suspended {
			result = WorkflowResult{Status: "running"}
			return
		}
		if recovered != nil {
			result = WorkflowResult{Status: "failed", Error: fmt.Sprintf("workflow panicked: %v", recovered)}
		}
	}()
	input := wf.request.Input
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	value, err := definition.Run(ctx, wf, input)
	if wf.suspended {
		// The function swallowed the suspension; it is still waiting.
		return WorkflowResult{Status: "running"}
	}
	if err != nil {
		return WorkflowResult{Status: "failed", Error: err.Error()}
	}
	output, err := json.Marshal(value)
	if err != nil {
		return WorkflowResult{Status: "failed", Error: "workflow result is not JSON: " + err.Error()}
	}
	return WorkflowResult{Status: "succeeded", Output: output}
}

// fitWorkflowResponse fails, without retry, any step result (and the
// workflow output) that would push the answer past WorkflowResponseMaxBytes.
func fitWorkflowResponse(response *WorkflowInvocationResponse) *WorkflowInvocationResponse {
	budget := WorkflowResponseMaxBytes - 1024
	envelope, _ := json.Marshal(WorkflowInvocationResponse{Protocol: response.Protocol, Ops: []WorkflowReportedOp{}, Result: WorkflowResult{Status: "running"}})
	used := len(envelope)
	for i, op := range response.Ops {
		size := workflowJSONSize(op) + 1
		if op.Kind == "step" && op.Status == "succeeded" && used+size > budget {
			op.Output, op.Status, op.NonRetryable = nil, "failed", true
			op.Error = fmt.Sprintf("step result does not fit in one call's %d byte answer; store large data elsewhere and return its key", WorkflowResponseMaxBytes)
			response.Ops[i] = op
			size = workflowJSONSize(op) + 1
		}
		used += size
	}
	if response.Result.Status == "succeeded" && used+workflowJSONSize(response.Result) > budget {
		response.Result = WorkflowResult{Status: "failed", Error: fmt.Sprintf("workflow output does not fit in one call's %d byte answer", WorkflowResponseMaxBytes)}
	}
	return response
}

func workflowJSONSize(value any) int {
	raw, _ := json.Marshal(value)
	return len(raw)
}

// ServeWorkflowsOptions configures ServeWorkflowsWithOptions.
type ServeWorkflowsOptions struct {
	// AllowUnverifiedCallers accepts calls without Gregale's internal
	// workflow headers. Gregale strips x-faas-* headers from public
	// requests, so the default refuses callers other than the platform.
	// Enable only for local testing.
	AllowUnverifiedCallers bool
}

// ServeWorkflows returns an http.Handler serving the workflows' entry protocol.
func ServeWorkflows(definitions ...WorkflowDefinition) http.Handler {
	return ServeWorkflowsWithOptions(ServeWorkflowsOptions{}, definitions...)
}

// ServeWorkflowsWithOptions is ServeWorkflows with options.
func ServeWorkflowsWithOptions(options ServeWorkflowsOptions, definitions ...WorkflowDefinition) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeWorkflowJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if !options.AllowUnverifiedCallers && (r.Header.Get("X-Faas-Workflow-Mode") != "code" || r.Header.Get("X-Faas-Workflow-Run-Id") == "") {
			writeWorkflowJSON(w, http.StatusForbidden, map[string]string{"error": "workflow entry handlers only accept Gregale workflow calls"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, workflowRequestMaxBytes+1))
		if err != nil {
			writeWorkflowJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read workflow request"})
			return
		}
		if len(body) > workflowRequestMaxBytes {
			writeWorkflowJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "workflow request too large"})
			return
		}
		response, err := RunWorkflowInvocation(r.Context(), definitions, body)
		switch {
		case errors.Is(err, ErrUnknownWorkflow):
			writeWorkflowJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case err != nil:
			writeWorkflowJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeWorkflowJSON(w, http.StatusOK, response)
		}
	})
}

func writeWorkflowJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value) // the client may have gone away
}

// WorkflowLocalRunOptions configures RunWorkflowLocally.
type WorkflowLocalRunOptions struct {
	// Events are payloads by event name, consumed in order by waits.
	Events map[string][]any
	// Retry applies to steps without their own policy (default 3 attempts).
	Retry *WorkflowRetryPolicy
	// MaxInvocations stops a run that does not finish (default 1000).
	MaxInvocations int
	RunID          string
}

// WorkflowLocalRunResult is a finished local run.
type WorkflowLocalRunResult struct {
	Status      string
	Output      json.RawMessage
	Error       string
	Journal     []WorkflowJournalEntry
	Invocations int
}

// RunWorkflowLocally runs a workflow to completion in-process, the way
// Gregale would: sleeps finish at once, waits take the given events in order
// or time out, and failed steps retry under their policy. Use it in tests.
func RunWorkflowLocally(ctx context.Context, definition WorkflowDefinition, input any, options WorkflowLocalRunOptions) (WorkflowLocalRunResult, error) {
	rawInput, err := json.Marshal(input)
	if err != nil {
		return WorkflowLocalRunResult{}, fmt.Errorf("faas: workflow input is not JSON: %w", err)
	}
	runID := options.RunID
	if runID == "" {
		runID = "local-run"
	}
	limit := options.MaxInvocations
	if limit <= 0 {
		limit = 1000
	}
	defaultAttempts := 3
	if options.Retry != nil && options.Retry.MaxAttempts > 0 {
		defaultAttempts = options.Retry.MaxAttempts
	}
	var journal []WorkflowJournalEntry
	consumed := map[string]int{}
	for invocation := 1; invocation <= limit; invocation++ {
		body, err := json.Marshal(WorkflowInvocationRequest{
			Protocol: WorkflowCodeProtocol, RunID: runID, Workflow: definition.Name,
			Input: rawInput, Invocation: invocation, Journal: append([]WorkflowJournalEntry(nil), journal...),
		})
		if err != nil {
			return WorkflowLocalRunResult{}, err
		}
		response, err := RunWorkflowInvocation(ctx, []WorkflowDefinition{definition}, body)
		if err != nil {
			return WorkflowLocalRunResult{}, err
		}
		for _, op := range response.Ops {
			if op.Seq < len(journal) {
				if journal[op.Seq].Name != op.Name || journal[op.Seq].Kind != op.Kind {
					return WorkflowLocalRunResult{Status: "failed", Error: fmt.Sprintf("nondeterministic workflow: operation %d %q", op.Seq, op.Name), Journal: journal, Invocations: invocation}, nil
				}
			} else if op.Seq != len(journal) {
				return WorkflowLocalRunResult{Status: "failed", Error: fmt.Sprintf("nondeterministic workflow: operation %d %q", op.Seq, op.Name), Journal: journal, Invocations: invocation}, nil
			}
			entry := localWorkflowEntry(op, journal, defaultAttempts, options.Events, consumed)
			if op.Seq < len(journal) {
				journal[op.Seq] = entry
			} else {
				journal = append(journal, entry)
			}
		}
		switch response.Result.Status {
		case "succeeded":
			return WorkflowLocalRunResult{Status: "succeeded", Output: response.Result.Output, Journal: journal, Invocations: invocation}, nil
		case "failed":
			return WorkflowLocalRunResult{Status: "failed", Error: response.Result.Error, Journal: journal, Invocations: invocation}, nil
		}
		if len(response.Ops) == 0 {
			return WorkflowLocalRunResult{Status: "failed", Error: "workflow made no progress", Journal: journal, Invocations: invocation}, nil
		}
	}
	return WorkflowLocalRunResult{Status: "failed", Error: fmt.Sprintf("workflow did not finish within %d invocations", limit), Journal: journal, Invocations: limit}, nil
}

func localWorkflowEntry(op WorkflowReportedOp, journal []WorkflowJournalEntry, defaultAttempts int, events map[string][]any, consumed map[string]int) WorkflowJournalEntry {
	entry := WorkflowJournalEntry{Seq: op.Seq, Name: op.Name, Kind: op.Kind}
	switch op.Kind {
	case "step":
		previous := 0
		if op.Seq < len(journal) {
			previous = journal[op.Seq].Attempt
		}
		entry.Attempt = previous + 1
		maxAttempts := defaultAttempts
		if op.Retry != nil && op.Retry.MaxAttempts > 0 {
			maxAttempts = op.Retry.MaxAttempts
		}
		switch {
		case op.Status == "succeeded":
			entry.Status, entry.Output = "succeeded", op.Output
		case !op.NonRetryable && entry.Attempt < maxAttempts:
			entry.Status, entry.Error = "retrying", op.Error
		default:
			entry.Status, entry.Error = "failed", op.Error
		}
	case "sleep":
		entry.Status = "succeeded"
	default:
		event := op.Event
		if event == "" {
			event = op.Name
		}
		index := consumed[event]
		consumed[event] = index + 1
		entry.Event, entry.Status = event, "succeeded"
		if payloads := events[event]; index < len(payloads) {
			if raw, err := json.Marshal(payloads[index]); err == nil {
				entry.Output = raw
			}
		} else {
			entry.TimedOut = true
		}
	}
	return entry
}

// Package workflowcode implements the engine side of the code-first workflow
// protocol (ADR-20261021204307): it turns a run's step rows into the journal
// sent to the entry handler and validates the operations the handler reports
// back. It holds no state and performs no I/O; pkg/sched applies the result.
package workflowcode

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// Kind is an operation kind.
type Kind string

const (
	KindStep   Kind = "step"
	KindSleep  Kind = "sleep"
	KindEvent  Kind = "event"
	KindDriver Kind = "driver"
)

// Journal entry statuses sent to the SDK.
const (
	EntrySucceeded = "succeeded"
	EntryFailed    = "failed"
	// EntryRetrying is a step whose last attempt failed and which the SDK
	// must run again.
	EntryRetrying = "retrying"
	// EntryScheduled is a retrying step whose back-off has not elapsed. The
	// SDK leaves it suspended; the engine calls again when it is due. Only
	// the request carries it (Journal.Sent); the journal itself keeps
	// EntryRetrying, so an SDK that runs the step early is still accepted.
	EntryScheduled = "scheduled"
	// EntryWaiting is a sleep or event wait that has not resolved.
	EntryWaiting = "waiting"
)

// Sent returns the entries for an entry-handler request at now: retrying
// steps whose back-off has not elapsed are marked EntryScheduled, so another
// operation settling does not run them before their retry time.
func (j Journal) Sent(now time.Time) []Entry {
	entries := make([]Entry, len(j.Entries))
	copy(entries, j.Entries)
	for i, entry := range entries {
		if entry.Status != EntryRetrying {
			continue
		}
		if retryAt := j.Rows[entry.Name].Row.NextRetry; retryAt != nil && now.Before(*retryAt) {
			entries[i].Status = EntryScheduled
		}
	}
	return entries
}

// Result statuses reported by the SDK.
const (
	ResultRunning   = "running"
	ResultSucceeded = "succeeded"
	ResultFailed    = "failed"
)

// Step row statuses, mirroring pkg/state's workflow step vocabulary.
const (
	RowPending       = "pending"
	RowRunning       = "running"
	RowAwaitingEvent = "awaiting_event"
	RowSucceeded     = "succeeded"
	RowFailed        = "failed"
	RowDead          = "dead"
	RowSkipped       = "skipped"
)

var (
	// ErrNondeterministic marks a response that contradicts the recorded journal.
	ErrNondeterministic = errors.New("nondeterministic workflow")
	// ErrProtocol marks a malformed envelope; the invocation is retried.
	ErrProtocol = errors.New("invalid workflow code response")
	// ErrLimit marks a run that outgrew a code-workflow limit.
	ErrLimit = errors.New("workflow code limit exceeded")
	// ErrJournal marks step rows that do not form a valid journal.
	ErrJournal = errors.New("invalid workflow code journal")
)

// Descriptor is stored in a step row's input under api.WorkflowCodeOpKey.
type Descriptor struct {
	Seq        int    `json:"seq"`
	Kind       Kind   `json:"kind"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Event      string `json:"event,omitempty"`
	TimeoutMS  int64  `json:"timeout_ms,omitempty"`
	// EventID is the event a resolved wait consumed; a timed-out wait has
	// none, so it never takes an event a later wait is owed.
	EventID string                 `json:"event_id,omitempty"`
	Retry   *api.WorkflowRetrySpec `json:"retry,omitempty"`
	// Failures counts consecutive failed entry calls on the $driver row.
	Failures int `json:"failures,omitempty"`
	// Seen is how many operations were resolved when the $driver row's
	// latest call began; more resolved operations mean the code must run.
	Seen int `json:"seen,omitempty"`
}

// Duration is a sleep's length.
func (d Descriptor) Duration() time.Duration { return time.Duration(d.DurationMS) * time.Millisecond }

// Timeout is an event wait's timeout.
func (d Descriptor) Timeout() time.Duration { return time.Duration(d.TimeoutMS) * time.Millisecond }

// EncodeDescriptor wraps d for a step row's input column.
func EncodeDescriptor(d Descriptor) json.RawMessage {
	raw, _ := json.Marshal(map[string]Descriptor{api.WorkflowCodeOpKey: d})
	return raw
}

// DecodeDescriptor reads a descriptor from a step row's input.
func DecodeDescriptor(raw json.RawMessage) (Descriptor, bool) {
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(raw, &wrapper) != nil || wrapper[api.WorkflowCodeOpKey] == nil {
		return Descriptor{}, false
	}
	var d Descriptor
	if json.Unmarshal(wrapper[api.WorkflowCodeOpKey], &d) != nil {
		return Descriptor{}, false
	}
	return d, true
}

// Row is the subset of a workflow step row the protocol needs.
type Row struct {
	Name       string
	Status     string
	Attempt    int
	Input      json.RawMessage
	Output     json.RawMessage
	Error      *string
	StartedAt  *time.Time
	FinishedAt *time.Time
	NextRetry  *time.Time
}

// Entry is one journal operation as the SDK sees it.
type Entry struct {
	Seq      int             `json:"seq"`
	Name     string          `json:"name"`
	Kind     Kind            `json:"kind"`
	Status   string          `json:"status"`
	Attempt  int             `json:"attempt,omitempty"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
	TimedOut bool            `json:"timed_out,omitempty"`
	// Event is the awaited event name, so the SDK can check replays.
	Event string `json:"event,omitempty"`
}

// Request is the envelope POSTed to the entry handler.
type Request struct {
	Protocol   string          `json:"protocol"`
	RunID      string          `json:"run_id"`
	Workflow   string          `json:"workflow"`
	Input      json.RawMessage `json:"input"`
	Invocation int             `json:"invocation"`
	Journal    []Entry         `json:"journal"`
}

// ReportedOp is one operation in the handler's response.
type ReportedOp struct {
	Seq  int    `json:"seq"`
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Step attempt outcome: succeeded or failed.
	Status       string                 `json:"status,omitempty"`
	Output       json.RawMessage        `json:"output,omitempty"`
	Error        string                 `json:"error,omitempty"`
	NonRetryable bool                   `json:"non_retryable,omitempty"`
	Retry        *api.WorkflowRetrySpec `json:"retry,omitempty"`
	DurationMS   int64                  `json:"duration_ms,omitempty"`
	Event        string                 `json:"event,omitempty"`
	TimeoutMS    int64                  `json:"timeout_ms,omitempty"`
}

// Result is the workflow function's state after an invocation.
type Result struct {
	Status string          `json:"status"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Response is the entry handler's answer.
type Response struct {
	Protocol string       `json:"protocol"`
	Ops      []ReportedOp `json:"ops"`
	Result   Result       `json:"result"`
}

// Limits bounds a run; values come from pkg/api's limits table and the
// account plan.
type Limits struct {
	MaxOps          int
	JournalMaxBytes int64
	ErrorMaxBytes   int
	OutputMaxBytes  int64
	MaxWait         time.Duration
	DefaultRetry    api.WorkflowRetrySpec
}

// DefaultLimits returns the table limits with the given plan wait ceiling.
func DefaultLimits(maxWait time.Duration, defaultRetry api.WorkflowRetrySpec) Limits {
	return Limits{
		MaxOps:          api.WorkflowCodeMaxOps,
		JournalMaxBytes: api.WorkflowCodeJournalMaxBytes,
		ErrorMaxBytes:   api.WorkflowCodeErrorMaxBytes,
		OutputMaxBytes:  api.WorkflowCodeResponseMaxBytes,
		MaxWait:         maxWait,
		DefaultRetry:    defaultRetry,
	}
}

// Journal is a run's decoded operations plus its $driver row.
type Journal struct {
	Entries []Entry
	// Rows maps an operation name to its row and descriptor.
	Rows   map[string]JournalRow
	Driver *JournalRow
	Bytes  int64
}

// JournalRow pairs a row with its decoded descriptor.
type JournalRow struct {
	Row        Row
	Descriptor Descriptor
}

// BuildJournal orders a run's operation rows by sequence number and maps
// their row status to the SDK's journal vocabulary. Rows without a
// descriptor are ignored; a gap or duplicate in the sequence is an error.
func BuildJournal(rows []Row) (Journal, error) {
	journal := Journal{Rows: make(map[string]JournalRow, len(rows))}
	ops := make([]JournalRow, 0, len(rows))
	for _, row := range rows {
		descriptor, ok := DecodeDescriptor(row.Input)
		if !ok {
			continue
		}
		item := JournalRow{Row: row, Descriptor: descriptor}
		if row.Name == api.WorkflowCodeDriverStep {
			journal.Driver = &item
			continue
		}
		ops = append(ops, item)
		journal.Rows[row.Name] = item
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Descriptor.Seq < ops[j].Descriptor.Seq })
	for i, item := range ops {
		if item.Descriptor.Seq != i {
			return Journal{}, fmt.Errorf("%w: operation %q has sequence %d, want %d", ErrJournal, item.Row.Name, item.Descriptor.Seq, i)
		}
		entry := Entry{Seq: i, Name: item.Row.Name, Kind: item.Descriptor.Kind, Attempt: item.Row.Attempt, Event: item.Descriptor.Event}
		switch item.Row.Status {
		case RowSucceeded:
			entry.Status = EntrySucceeded
			entry.Output = item.Row.Output
			entry.TimedOut = item.Descriptor.Kind == KindEvent && len(item.Row.Output) == 0
		case RowFailed, RowDead:
			entry.Status = EntryFailed
			if item.Row.Error != nil {
				entry.Error = *item.Row.Error
			}
		default:
			entry.Status = EntryWaiting
			if item.Descriptor.Kind == KindStep {
				entry.Status = EntryRetrying
				if item.Row.Error != nil {
					entry.Error = *item.Row.Error
				}
			}
		}
		journal.Bytes += int64(len(entry.Output) + len(entry.Error))
		journal.Entries = append(journal.Entries, entry)
	}
	return journal, nil
}

// PlannedOp is a validated operation to record.
type PlannedOp struct {
	ReportedOp
	New        bool
	Descriptor Descriptor
	// Attempt is the step attempt being recorded (0 for waits).
	Attempt int
	// Retry is the effective policy for a failed step.
	Retry api.WorkflowRetrySpec
}

// Plan is a validated response.
type Plan struct {
	Ops    []PlannedOp
	Result Result
}

// Validate checks a response against the journal it answered. Errors wrap
// ErrProtocol (retry the invocation), ErrNondeterministic or ErrLimit (the
// run cannot continue).
func Validate(journal Journal, response Response, limits Limits) (Plan, error) {
	if response.Protocol != api.WorkflowCodeProtocol {
		return Plan{}, fmt.Errorf("%w: protocol %q, want %q", ErrProtocol, response.Protocol, api.WorkflowCodeProtocol)
	}
	switch response.Result.Status {
	case ResultRunning, ResultSucceeded, ResultFailed:
	default:
		return Plan{}, fmt.Errorf("%w: result status %q", ErrProtocol, response.Result.Status)
	}
	if response.Result.Status == ResultSucceeded && len(response.Result.Output) > 0 {
		if !json.Valid(response.Result.Output) {
			return Plan{}, fmt.Errorf("%w: workflow output is not JSON", ErrProtocol)
		}
		if int64(len(response.Result.Output)) > limits.OutputMaxBytes {
			return Plan{}, fmt.Errorf("%w: workflow output is %d bytes, limit %d", ErrLimit, len(response.Result.Output), limits.OutputMaxBytes)
		}
	}
	plan := Plan{Result: response.Result}
	plan.Result.Error = truncate(plan.Result.Error, limits.ErrorMaxBytes)
	next := len(journal.Entries)
	seen := map[int]bool{}
	names := map[string]bool{}
	bytes := journal.Bytes
	for _, op := range response.Ops {
		planned, err := validateOp(journal, op, limits, next, seen, names)
		if err != nil {
			return Plan{}, err
		}
		if planned.New {
			next++
		}
		bytes += int64(len(op.Output) + len(planned.Error))
		plan.Ops = append(plan.Ops, planned)
	}
	if next > limits.MaxOps {
		return Plan{}, fmt.Errorf("%w: %d operations, limit %d", ErrLimit, next, limits.MaxOps)
	}
	if bytes > limits.JournalMaxBytes {
		return Plan{}, fmt.Errorf("%w: journal is %d bytes, limit %d", ErrLimit, bytes, limits.JournalMaxBytes)
	}
	if plan.Result.Status != ResultRunning {
		return plan, nil
	}
	if !progresses(journal, plan) {
		return Plan{}, fmt.Errorf("%w: a running workflow reported no operation and nothing is pending", ErrProtocol)
	}
	return plan, nil
}

func validateOp(journal Journal, op ReportedOp, limits Limits, next int, seen map[int]bool, names map[string]bool) (PlannedOp, error) {
	if !api.ValidWorkflowCodeOpName(op.Name) {
		return PlannedOp{}, fmt.Errorf("%w: invalid operation name %q", ErrProtocol, op.Name)
	}
	if seen[op.Seq] {
		return PlannedOp{}, fmt.Errorf("%w: sequence %d reported twice", ErrProtocol, op.Seq)
	}
	seen[op.Seq] = true
	planned := PlannedOp{ReportedOp: op, Descriptor: Descriptor{Seq: op.Seq, Kind: op.Kind}}
	planned.Error = truncate(op.Error, limits.ErrorMaxBytes)
	switch {
	case op.Seq < 0:
		return PlannedOp{}, fmt.Errorf("%w: negative sequence", ErrProtocol)
	case op.Seq < len(journal.Entries):
		recorded := journal.Entries[op.Seq]
		if recorded.Name != op.Name || recorded.Kind != op.Kind {
			return PlannedOp{}, fmt.Errorf("%w: operation %d is %s %q in the journal, the code asked for %s %q", ErrNondeterministic, op.Seq, recorded.Kind, recorded.Name, op.Kind, op.Name)
		}
		if op.Kind != KindStep || recorded.Status != EntryRetrying {
			return PlannedOp{}, fmt.Errorf("%w: operation %q is already %s", ErrNondeterministic, op.Name, recorded.Status)
		}
		row := journal.Rows[op.Name]
		planned.Descriptor = row.Descriptor
		planned.Attempt = row.Row.Attempt + 1
	case op.Seq == next:
		if _, exists := journal.Rows[op.Name]; exists || names[op.Name] {
			return PlannedOp{}, fmt.Errorf("%w: operation name %q is already used", ErrNondeterministic, op.Name)
		}
		planned.New = true
		planned.Attempt = 1
	default:
		return PlannedOp{}, fmt.Errorf("%w: operation %q has sequence %d, want %d", ErrNondeterministic, op.Name, op.Seq, next)
	}
	names[op.Name] = true
	switch op.Kind {
	case KindStep:
		return validateStep(planned, limits)
	case KindSleep:
		if !planned.New {
			return PlannedOp{}, fmt.Errorf("%w: sleep %q re-reported", ErrProtocol, op.Name)
		}
		duration := time.Duration(op.DurationMS) * time.Millisecond
		if duration < time.Second || duration > limits.MaxWait {
			return PlannedOp{}, fmt.Errorf("%w: sleep %q is %s; it must be between 1s and %s", ErrLimit, op.Name, duration, limits.MaxWait)
		}
		planned.Attempt = 0
		planned.Descriptor.DurationMS = op.DurationMS
	case KindEvent:
		if err := validateEventName(op.Event); err != nil {
			return PlannedOp{}, err
		}
		timeout := time.Duration(op.TimeoutMS) * time.Millisecond
		if op.TimeoutMS == 0 {
			timeout = limits.MaxWait
		}
		if timeout < time.Second || timeout > limits.MaxWait {
			return PlannedOp{}, fmt.Errorf("%w: wait %q timeout is %s; it must be between 1s and %s", ErrLimit, op.Name, timeout, limits.MaxWait)
		}
		planned.Attempt = 0
		planned.Descriptor.Event = op.Event
		planned.Descriptor.TimeoutMS = timeout.Milliseconds()
	default:
		return PlannedOp{}, fmt.Errorf("%w: unknown operation kind %q", ErrProtocol, op.Kind)
	}
	return planned, nil
}

func validateStep(planned PlannedOp, limits Limits) (PlannedOp, error) {
	op := planned.ReportedOp
	switch op.Status {
	case ResultSucceeded:
		if len(op.Output) > 0 && !json.Valid(op.Output) {
			return PlannedOp{}, fmt.Errorf("%w: step %q output is not JSON", ErrProtocol, op.Name)
		}
		if int64(len(op.Output)) > limits.OutputMaxBytes {
			return PlannedOp{}, fmt.Errorf("%w: step %q output is %d bytes, limit %d", ErrLimit, op.Name, len(op.Output), limits.OutputMaxBytes)
		}
	case ResultFailed:
		if op.Retry != nil && (op.Retry.MaxAttempts < 1 || op.Retry.MaxAttempts > api.WorkflowRetryMaxAttempts ||
			(op.Retry.Backoff != "" && op.Retry.Backoff != "fixed" && op.Retry.Backoff != "exponential")) {
			return PlannedOp{}, fmt.Errorf("%w: step %q retry policy is invalid", ErrProtocol, op.Name)
		}
		planned.Retry = limits.DefaultRetry
		if op.Retry != nil {
			planned.Retry = *op.Retry
		}
		if planned.Error == "" {
			planned.Error = "step failed"
		}
	default:
		return PlannedOp{}, fmt.Errorf("%w: step %q status %q", ErrProtocol, op.Name, op.Status)
	}
	if op.Retry != nil {
		planned.Descriptor.Retry = op.Retry
	}
	return planned, nil
}

func validateEventName(name string) error {
	if strings.TrimSpace(name) == "" || len(name) > api.WorkflowWebhookEventMaxBytes || !utf8.ValidString(name) {
		return fmt.Errorf("%w: event name must be 1-%d bytes", ErrProtocol, api.WorkflowWebhookEventMaxBytes)
	}
	if api.IsWorkflowCallbackEventName(name) {
		return fmt.Errorf("%w: event name %q uses a reserved prefix", ErrProtocol, name)
	}
	return nil
}

// progresses reports whether a running result leaves the run something to
// wait for: a new operation, a scheduled retry, or a pending wait.
func progresses(journal Journal, plan Plan) bool {
	if len(plan.Ops) > 0 {
		return true
	}
	for _, entry := range journal.Entries {
		// A retrying step the SDK left suspended (EntryScheduled in the
		// request) runs when its back-off elapses.
		if entry.Status == EntryWaiting || entry.Status == EntryRetrying {
			return true
		}
	}
	return false
}

// ShouldRetry applies a failed step's policy to its attempt number.
func ShouldRetry(op PlannedOp) bool {
	return !op.NonRetryable && op.Attempt < op.Retry.MaxAttempts
}

// RetryDelay follows the declarative engine's back-off: 1 s fixed, or
// 1 s doubling per attempt up to 5 minutes.
func RetryDelay(retry api.WorkflowRetrySpec, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	if retry.Backoff == "exponential" {
		shift := min(attempt-1, 8)
		delay = time.Duration(1<<shift) * time.Second
	}
	return min(delay, 5*time.Minute)
}

// DriverRetryDelay spaces consecutive entry-handler failures: 5 s doubling
// up to 5 minutes, long enough to ride out a deploy or a cold boot.
func DriverRetryDelay(failures int) time.Duration {
	shift := min(max(failures-1, 0), 6)
	return min(time.Duration(5<<shift)*time.Second, 5*time.Minute)
}

// truncate bounds s to max bytes on a rune boundary.
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

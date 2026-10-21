package internal

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/api"
)

var errBackgroundCapacity = errors.New("runner: background task capacity exceeded")

// BackgroundRuntime is trusted runner wiring, shared by every invocation in
// one runner process. It is deliberately not enabled by a manifest, environment
// variable or request header: host-side VM lifetime protection must be wired and
// qualified before production runners opt into this completion contract.
type BackgroundRuntime struct {
	timeout time.Duration
	// maxTasks bounds one invocation's in-flight registrations (api.TailCapMax).
	maxTasks int
	capacity int
	// admission is shared with the runtime this one replaced after a plan
	// change, so tasks admitted under the earlier plan still count.
	admission *backgroundAdmission
	// Observe must not block. Events contain identifiers and closed-set states,
	// never promise values, exception messages, SQL or request payloads.
	Observe func(BackgroundEvent)
}

type BackgroundEvent struct {
	InvocationID string
	Task         int
	Phase        string
	Outcome      string
}

// NewBackgroundRuntime derives every admission bound from the central plan
// table. Call once per runner, not once per request.
func NewBackgroundRuntime(plan api.Plan) (*BackgroundRuntime, error) {
	if _, known := api.LimitsFor(plan); !known || !plan.TailEnabled() {
		return nil, errors.New("runner: background work requires a known enabled plan")
	}
	return &BackgroundRuntime{
		timeout:  time.Duration(plan.TailTimeoutSeconds()) * time.Second,
		maxTasks: plan.TailCapMax(), capacity: plan.ConcurrentTailsPerInstance(),
		admission: &backgroundAdmission{},
	}, nil
}

// backgroundAdmission counts one runner's outstanding background tasks.
type backgroundAdmission struct {
	mu     sync.Mutex
	active int
}

// replace returns the runtime for a changed plan. It keeps counting tasks the
// earlier plan admitted, which release into the same admission when they settle.
func (r *BackgroundRuntime) replace(plan api.Plan) (*BackgroundRuntime, error) {
	next, err := NewBackgroundRuntime(plan)
	if err != nil {
		return nil, err
	}
	next.admission, next.Observe = r.admission, r.Observe
	return next, nil
}

// Active counts accepted tasks whose execution has not settled. A timeout or
// disconnected HTTP reader does not release admission before cleanup finishes.
func (r *BackgroundRuntime) Active() int {
	r.admission.mu.Lock()
	defer r.admission.mu.Unlock()
	return r.admission.active
}

func (r *BackgroundRuntime) reserve() bool {
	r.admission.mu.Lock()
	defer r.admission.mu.Unlock()
	if r.admission.active >= r.capacity {
		return false
	}
	r.admission.active++
	return true
}

func (r *BackgroundRuntime) release() {
	r.admission.mu.Lock()
	r.admission.active--
	r.admission.mu.Unlock()
}

func (r *BackgroundRuntime) emit(e BackgroundEvent) {
	slog.Info("function background", "invocation_id", e.InvocationID, "task", e.Task, "phase", e.Phase, "outcome", e.Outcome)
	if r.Observe != nil {
		r.Observe(e)
	}
}

// BackgroundPolicy crosses only the trusted runner/adapter envelope seam.
type BackgroundPolicy struct {
	TimeoutMS int64 `json:"timeout_ms"`
	MaxTasks  int   `json:"max_tasks"`
}

type BackgroundFrame struct {
	Task    int    `json:"task"`
	Event   string `json:"event"`
	Outcome string `json:"outcome,omitempty"`
}

type backgroundTask struct{ active, timedOut bool }

// invocationBackground has one reader at a time: the HTTP owner, followed by
// the detached completion reader. Cleanup takes ownership only after Close.
type invocationBackground struct {
	runtime      *BackgroundRuntime
	invocationID string
	tasks        map[int]*backgroundTask
	complete     bool
	lastTaskID   int
}

func (r *BackgroundRuntime) invocation(id string) *invocationBackground {
	return &invocationBackground{runtime: r, invocationID: id, tasks: make(map[int]*backgroundTask)}
}

func (b *invocationBackground) emit(id int, phase, outcome string) {
	b.runtime.emit(BackgroundEvent{InvocationID: b.invocationID, Task: id, Phase: phase, Outcome: outcome})
}

func (b *invocationBackground) control(f *BackgroundFrame) error {
	if f == nil || f.Task < 1 || b.complete {
		return errors.New("runner: invalid background task frame")
	}
	task := b.tasks[f.Task]
	switch f.Event {
	case "register":
		if task != nil || f.Task <= b.lastTaskID || f.Outcome != "" {
			return errors.New("runner: duplicate or invalid background registration")
		}
		if len(b.tasks) >= b.runtime.maxTasks || !b.runtime.reserve() {
			return errBackgroundCapacity
		}
		b.tasks[f.Task] = &backgroundTask{active: true}
		b.lastTaskID = f.Task
		b.emit(f.Task, "registered", "")
	case "timeout":
		if task == nil || !task.active || task.timedOut || f.Outcome != "" {
			return errors.New("runner: invalid background timeout")
		}
		task.timedOut = true
		b.emit(f.Task, "timeout", "timeout")
	case "settled":
		if task == nil || !task.active {
			return errors.New("runner: background settlement without active task")
		}
		if f.Outcome != "completed" && f.Outcome != "failed" && f.Outcome != "timeout" {
			return errors.New("runner: invalid background outcome")
		}
		if task.timedOut != (f.Outcome == "timeout") {
			return errors.New("runner: inconsistent background timeout outcome")
		}
		task.active = false
		delete(b.tasks, f.Task)
		b.runtime.release()
		b.emit(f.Task, "settled", f.Outcome)
	default:
		return errors.New("runner: invalid background event")
	}
	return nil
}

func (b *invocationBackground) finish() error {
	if b.complete {
		return errors.New("runner: duplicate execution completion")
	}
	for _, task := range b.tasks {
		if task.active {
			return errors.New("runner: execution completed with active background tasks")
		}
	}
	b.complete = true
	return nil
}

// interrupt runs only after the worker acknowledged complete cancellation or
// was retired. Outstanding tasks stay admitted throughout cancellation cleanup.
func (b *invocationBackground) interrupt() {
	for id, task := range b.tasks {
		if task.active {
			task.active = false
			delete(b.tasks, id)
			b.runtime.release()
			b.emit(id, "interrupted", "interrupted")
		}
	}
}

func (b *invocationBackground) timeoutPending() {
	for id, task := range b.tasks {
		if task.active && !task.timedOut {
			task.timedOut = true
			b.emit(id, "timeout", "timeout")
		}
	}
}

// next consumes control frames without exposing them as HTTP response data.
func (b *invocationBackground) next(ctx context.Context, read func(context.Context) ([]byte, error)) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, err := read(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := decodeResponse(line)
		if err != nil {
			return nil, err
		}
		switch resp.Frame {
		case FrameBackground:
			if err := b.control(resp.Background); err != nil {
				return nil, err
			}
		case FrameComplete:
			if err := b.finish(); err != nil {
				return nil, err
			}
			return line, nil
		default:
			return line, nil
		}
	}
}

func (inv *invocation) trackBackground(ctx context.Context, lines *workerpool.Lines, runtime *BackgroundRuntime, id string) {
	b := runtime.invocation(id)
	inv.background = b
	inv.nextContext = func(ctx context.Context) ([]byte, error) { return b.next(ctx, lines.NextContext) }
	inv.next = func() ([]byte, error) { return inv.nextContext(ctx) }
	inv.close = func(healthy bool) {
		lines.Close(healthy)
		select {
		case <-lines.Released():
			b.interrupt()
		default:
			go func() { <-lines.Released(); b.interrupt() }()
		}
	}
}

func (inv *invocation) detachBackground(parent context.Context, runtime *BackgroundRuntime) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), runtime.timeout)
	go func() {
		defer cancel()
		healthy := false
		defer func() { inv.close(healthy) }()
		line, err := inv.nextContext(ctx)
		if err == nil {
			var resp Response
			resp, err = decodeResponse(line)
			if err == nil && resp.Frame != FrameComplete {
				err = errors.New("runner: expected execution completion")
			}
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				inv.background.timeoutPending()
			}
			slog.Error("function background completion failed", "invocation_id", inv.background.invocationID, "error", err)
			return
		}
		healthy = true
	}()
}

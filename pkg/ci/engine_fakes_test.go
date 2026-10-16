package ci

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var errFakeTransport = errors.New("fake backend transport failed after accepting Start")

// This backend only stores synthetic results;
// it never executes a command, fetches source, starts a VM or accesses a registry.
type fakeBackend struct {
	mu         sync.Mutex
	attempts   map[string]*fakeTask
	scenarios  map[string]fakeScenario
	startOrder []string
	onStart    func(StartRequest)
}

type fakeScenario struct {
	fail         bool
	neverFinish  bool
	loseStartAck bool
	cancelDelay  int
	logs         []byte
	artifacts    []ArtifactRef
	modify       func(*Observation)
}

type fakeTask struct {
	key         AttemptKey
	request     *StartRequest
	observed    int
	cancelled   bool
	cancelPolls int
	terminal    bool
	lostAck     bool
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{attempts: make(map[string]*fakeTask), scenarios: make(map[string]fakeScenario)}
}

func (b *fakeBackend) task(key AttemptKey) (*fakeTask, error) {
	a := b.attempts[key.AttemptID]
	if a == nil {
		a = &fakeTask{key: key}
		b.attempts[key.AttemptID] = a
	}
	if a.key.AccountID != key.AccountID || a.key.ExecutionID != key.ExecutionID || a.key.JobName != key.JobName {
		return nil, ErrNotFound
	}
	if key.Fence < a.key.Fence {
		return nil, ErrLeaseLost
	}
	a.key.Fence = key.Fence
	return a, nil
}

func (b *fakeBackend) Start(ctx context.Context, req StartRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	a, err := b.task(req.Key)
	if err == nil && !a.cancelled {
		if a.request == nil {
			copyReq := clone(req)
			a.request = &copyReq
			b.startOrder = append(b.startOrder, req.Key.JobName)
		} else if a.request.Repository != req.Repository || a.request.CommitSHA != req.CommitSHA || !reflect.DeepEqual(a.request.Job, req.Job) || !a.request.Deadline.Equal(req.Deadline) {
			err = fmt.Errorf("attempt identity reused with changed inputs")
		}
	}
	if err == nil && b.scenarios[req.Key.JobName].loseStartAck && !a.lostAck {
		a.lostAck, err = true, errFakeTransport
	}
	hook := b.onStart
	b.mu.Unlock()
	if hook != nil {
		hook(req)
	}
	return err
}

func (b *fakeBackend) Cancel(ctx context.Context, key AttemptKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a, err := b.task(key)
	if err != nil {
		return err
	}
	if !a.terminal {
		a.cancelled = true
	}
	return nil
}

func (b *fakeBackend) Observe(ctx context.Context, req ObserveRequest) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a, err := b.task(req.Key)
	if err != nil {
		return Observation{}, err
	}
	scenario := b.scenarios[req.Key.JobName]
	a.observed++
	out := Observation{Status: StatusRunning, LogOffset: req.LogOffset}
	if a.request != nil {
		out.Steps = make([]StepResult, len(a.request.Job.Steps))
		for i, step := range a.request.Job.Steps {
			out.Steps[i] = StepResult{Name: step.Name, Status: StatusQueued}
		}
		out.Steps[0].Status = StatusRunning
	}
	switch {
	case a.cancelled:
		a.cancelPolls++
		if a.cancelPolls > scenario.cancelDelay {
			out.Status, out.Steps = StatusCancelled, nil
		}
	case a.request == nil:
		return Observation{}, fmt.Errorf("attempt has not started")
	case (a.observed >= 2 && !scenario.neverFinish) || a.terminal:
		out.Status, out.ExitCode = StatusSucceeded, intPtr(0)
		for i := range out.Steps {
			out.Steps[i].Status, out.Steps[i].ExitCode = StatusSucceeded, intPtr(0)
		}
		if scenario.fail {
			out.Status, out.ExitCode = StatusFailed, intPtr(1)
			out.Steps[0].Status, out.Steps[0].ExitCode = StatusFailed, intPtr(1)
			for i := 1; i < len(out.Steps); i++ {
				out.Steps[i].Status, out.Steps[i].ExitCode = StatusSkipped, nil
			}
		}
	}
	if req.LogOffset > len(scenario.logs) {
		return Observation{}, fmt.Errorf("backend log cursor out of range")
	}
	end := min(len(scenario.logs), req.LogOffset+req.LogLimit)
	out.Logs = append([]byte(nil), scenario.logs[req.LogOffset:end]...)
	out.MoreLogs = end < len(scenario.logs)
	if terminal(out.Status) {
		a.terminal = true
		out.Artifacts = clone(scenario.artifacts)
	}
	if scenario.modify != nil {
		scenario.modify(&out)
	}
	return out, nil
}

func intPtr(value int) *int { return &value }

var _ Backend = (*fakeBackend)(nil)

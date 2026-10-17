// Package rehearsalrecorder owns the host-side closing barrier for supported
// rehearsal relay executions (ADR-1351). It has no provider or guest authority.
package rehearsalrecorder

import (
	"context"
	"errors"
	"sync"
	"time"
)

// These are bounds on the recorder evidence protocol, not account quotas.
const (
	Version          = 1
	MaxSessions      = 256
	MaxRequests      = 256
	operationTimeout = 5 * time.Second
	pollInterval     = 250 * time.Millisecond
)

var ErrClosing = errors.New("rehearsal recorder is closing")

// Identity is fixed before the guest starts. Store implementations check it
// against retained enrollment and the scheduler-owned instance/deployment rows.
type Identity struct {
	AccountID, AppID, RunID, InstanceID, DeploymentID string
}

type Store interface {
	StartScenarioRecorder(context.Context, Identity) (string, error)
	BeginScenarioRecorderRequest(context.Context, Identity, string) (string, error)
	SettleScenarioRecorder(context.Context, Identity, string, bool, bool) (string, error)
}

// Session must not be reconstructed from an old database row after a restart.
// Only the owner that observed execution may acknowledge its closing barrier.
type Session struct {
	command                 bool
	mu                      sync.Mutex
	store                   Store
	identity                Identity
	id                      string
	closing, failed, active bool
	retiring, retired       bool
	suspendID               string
	suspended               bool
	checkpointID            string
	checkpointConnection    *Connection
	checkpointResumed       bool
	checkpointContext       context.Context
	workerFaultID           string
	workerCheckpointRelease chan struct{}
	connections             map[*Connection]func()
	done                    chan struct{}
	wake                    chan struct{}
	cancel                  context.CancelFunc
}

type Connection struct{ session *Session }

func Start(ctx, lifecycle context.Context, store Store, identity Identity) (*Session, error) {
	id, err := store.StartScenarioRecorder(ctx, identity)
	if err != nil {
		return nil, err
	}
	s := &Session{store: store, identity: identity, id: id, connections: make(map[*Connection]func()), done: make(chan struct{}), wake: make(chan struct{}, 1)}
	lifecycle, s.cancel = context.WithCancel(lifecycle)
	go s.run(lifecycle)
	return s, nil
}

// Activate follows successful VM publication; a partial boot cannot close as a
// successful execution. Fail also activates settlement for an aborted boot.
func (s *Session) Activate() { s.mu.Lock(); s.active = true; s.mu.Unlock(); s.signal() }
func (s *Session) Fail() {
	s.mu.Lock()
	if s.retired {
		s.mu.Unlock()
		return
	}
	s.retiring = false
	s.suspendID, s.suspended = "", false
	s.checkpointID, s.checkpointConnection, s.checkpointResumed = "", nil, false
	s.checkpointContext = nil
	s.failed, s.active, s.closing = true, true, true
	s.mu.Unlock()
	s.signal()
}
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Stop() {
	s.mu.Lock()
	retired := s.retired
	s.mu.Unlock()
	if !retired {
		s.Fail()
	}
	s.cancel()
}

func (s *Session) MarkCommand() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Execution kind remains available for cleanup even if lifecycle failure
	// races the caller immediately after Start.
	s.command = true
}
func (s *Session) CommandIdentity() (Identity, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.identity, s.command
}

type CommandStore interface {
	FenceScenarioCommandNetwork(context.Context, Identity) error
	RetireScenarioCommandRecorder(context.Context, Identity, string, string) error
}

func (s *Session) Retiring() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.retiring }

// PrepareRetirement fences this command's relay before VM destruction. It
// grants no durable completion: only FinishRetirement, after cleanup, can do so.
func (s *Session) PrepareRetirement(ctx context.Context) error {
	s.mu.Lock()
	if _, ok := s.store.(CommandStore); !ok || s.failed || s.closing || !s.active {
		s.mu.Unlock()
		return errors.New("command recorder cannot retire")
	}
	s.retiring, s.closing = true, true
	store := s.store.(CommandStore)
	var stops []func()
	for _, stop := range s.connections {
		stops = append(stops, stop)
	}
	s.mu.Unlock()
	if err := store.FenceScenarioCommandNetwork(ctx, s.identity); err != nil {
		s.Fail()
		return err
	}
	joined := make(chan struct{})
	go func() {
		var group sync.WaitGroup
		for _, stop := range stops {
			group.Go(stop)
		}
		group.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-ctx.Done():
		s.Fail()
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || len(s.connections) != 0 {
		return errors.New("command recorder did not settle")
	}
	return nil
}

func (s *Session) FinishRetirement(ctx context.Context, result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, ok := s.store.(CommandStore)
	if !ok || !s.retiring || s.failed || len(s.connections) != 0 {
		return errors.New("command recorder cannot acknowledge retirement")
	}
	if err := store.RetireScenarioCommandRecorder(ctx, s.identity, s.id, result); err != nil {
		return err
	}
	s.retiring, s.retired = false, true
	s.cancel()
	return nil
}

// Attach registers the HTTP parser as well as its handlers. stop must shut down
// idle connections and join active parsing/handlers before it returns.
func (s *Session) Attach(stop func()) (*Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return nil, ErrSuspended
	}
	if s.closing {
		if s.retiring {
			s.failed = true
		}
		return nil, ErrClosing
	}
	if len(s.connections) >= MaxRequests {
		s.failed = true
		s.signal()
		return nil, errors.New("rehearsal recorder connection bound exceeded")
	}
	c := &Connection{session: s}
	s.connections[c] = stop
	return c, nil
}

func (c *Connection) Detach(failed bool) {
	s := c.session
	s.mu.Lock()
	delete(s.connections, c)
	s.failed = s.failed || failed
	s.mu.Unlock()
	s.signal()
}

// Begin persists an obligation before any broker connection or bytes are sent.
// A failed write poisons the owner in memory; if the daemon also crashes, its
// previously persisted open session still cannot be acknowledged by a new one.
func (s *Session) Begin(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended {
		return "", ErrSuspended
	}
	if s.closing {
		if s.retiring {
			s.failed = true
		}
		return "", ErrClosing
	}
	id, err := s.store.BeginScenarioRecorderRequest(ctx, s.identity, s.id)
	if errors.Is(err, ErrClosing) {
		s.closing = true
	} else if err != nil {
		s.failed = true
	}
	return id, err
}

func (s *Session) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Session) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			retired := s.retired
			s.mu.Unlock()
			if retired {
				return
			}
			s.Fail()
			s.settle(context.WithoutCancel(ctx))
			return
		case <-ticker.C:
		case <-s.wake:
		}
		if s.settle(ctx) {
			return
		}
	}
}

func (s *Session) settle(parent context.Context) bool {
	s.mu.Lock()
	if s.retiring || s.retired || s.suspendID != "" || s.checkpointID != "" {
		s.mu.Unlock()
		return false
	}
	if !s.active {
		s.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithTimeout(parent, operationTimeout)
	status, err := s.store.SettleScenarioRecorder(ctx, s.identity, s.id, s.failed, false)
	cancel()
	if err != nil && !s.closing {
		s.mu.Unlock()
		return false
	}
	if err == nil && status != "recording" {
		s.closing = true
	}
	var stops []func()
	if s.closing {
		for _, stop := range s.connections {
			stops = append(stops, stop)
		}
	}
	s.mu.Unlock()
	// Shutdown must run without the session lock: handlers may be entering Begin.
	var joined sync.WaitGroup
	for _, stop := range stops {
		joined.Go(stop)
	}
	joined.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closing || len(s.connections) != 0 {
		return false
	}
	if err != nil {
		return false
	}
	if status == "closed" || status == "failed" {
		return true
	}
	ctx, cancel = context.WithTimeout(parent, operationTimeout)
	defer cancel()
	status, err = s.store.SettleScenarioRecorder(ctx, s.identity, s.id, s.failed, true)
	return err == nil && (status == "closed" || status == "failed")
}

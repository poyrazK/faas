package agentd

// Browser actions (ADR-20261117065818 §2). apid records a gregale_browser
// action as a pending row; agentd, which holds the session, sends it on the
// session's stream once the session's vmmd announced actions, one at a time
// per session, and records its end under the session's lease. One query a
// second per replica finds the next action of every session it holds.

import (
	"context"
	"sync"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// SandboxActionStore is the sandbox_actions surface agentd writes;
// *agentstore.Store implements it.
type SandboxActionStore interface {
	NextSandboxActions(ctx context.Context, sessionIDs []string) ([]agentstore.SandboxAction, error)
	StartSandboxAction(ctx context.Context, id, token string) (bool, error)
	FinishSandboxAction(ctx context.Context, id, token string, exitCode *int, output []byte) error
	EndOpenSandboxActions(ctx context.Context, sessionID, token string, states ...string) error
}

// sandboxActionSlack is the time past an action's own timeout agentd waits
// for its end before it records the action as never ended.
const sandboxActionSlack = 30 * time.Second

// actionSession is a held session that takes actions, and the action it
// runs, if any.
type actionSession struct {
	ss       *session
	send     func(*scheddpb.SandboxRunRequest) error
	running  string
	deadline time.Time
}

// sandboxActions is a replica's sessions that take actions.
type sandboxActions struct {
	mu       sync.Mutex
	sessions map[string]*actionSession
}

func (a *sandboxActions) add(ss *session, send func(*scheddpb.SandboxRunRequest) error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]*actionSession{}
	}
	a.sessions[ss.claim.ID] = &actionSession{ss: ss, send: send}
}

func (a *sandboxActions) remove(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, id)
}

// actionStore is the replica's action store, or nil when its store keeps
// no actions.
func (s *Sandboxes) actionStore() SandboxActionStore {
	st, _ := s.store.(SandboxActionStore)
	return st
}

// pumpActions sends held sessions their next actions until ctx ends.
func (s *Sandboxes) pumpActions(ctx context.Context) {
	tick := time.NewTicker(s.scfg.Tick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if err := s.PumpActionsOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("agentd: sandbox action pump failed", "err", err)
		}
	}
}

// PumpActionsOnce sends each held session that runs no action its next
// pending one, and ends the actions vmmd never answered.
func (s *Sandboxes) PumpActionsOnce(ctx context.Context) error {
	store := s.actionStore()
	if store == nil {
		return nil
	}
	now := s.now()
	var idle []string
	var overdue []*actionSession
	s.actions.mu.Lock()
	for id, as := range s.actions.sessions {
		switch {
		case as.running == "":
			idle = append(idle, id)
		case now.After(as.deadline):
			overdue = append(overdue, as)
		}
	}
	s.actions.mu.Unlock()
	for _, as := range overdue {
		s.endAction(ctx, as, as.running, nil, []byte("the browser action did not finish within its timeout"))
	}
	if len(idle) == 0 {
		return nil
	}
	next, err := store.NextSandboxActions(ctx, idle)
	if err != nil {
		return err
	}
	for _, action := range next {
		s.actions.mu.Lock()
		as := s.actions.sessions[action.SessionID]
		s.actions.mu.Unlock()
		if as != nil {
			s.startAction(ctx, store, as, action)
		}
	}
	return nil
}

// startAction marks an action running under the session's lease and sends
// it to the session's VM.
func (s *Sandboxes) startAction(ctx context.Context, store SandboxActionStore, as *actionSession, a agentstore.SandboxAction) {
	plan := api.Plan(as.ss.claim.Plan)
	if !plan.Valid() {
		return
	}
	timeout := time.Duration(api.SandboxBrowserActionTimeoutSec[plan.PlanIndex()]) * time.Second
	started, err := store.StartSandboxAction(ctx, a.ID, as.ss.claim.LeaseToken)
	if err != nil || !started {
		if err != nil {
			s.log.Warn("agentd: start sandbox action", "sandbox_id", as.ss.claim.ID, "action_id", a.ID, "err", err)
		}
		return
	}
	s.actions.mu.Lock()
	as.running, as.deadline = a.ID, s.now().Add(timeout+sandboxActionSlack)
	s.actions.mu.Unlock()
	maxChars := a.Args.MaxChars
	if maxChars <= 0 {
		maxChars = api.SandboxBrowserSnapshotDefaultChars
	}
	frame := &scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Action{Action: &scheddpb.SandboxRunAction{
		ActionId: a.ID, Kind: a.Kind, Url: a.Args.URL, Ref: int32(a.Args.Ref), Text: a.Args.Text, Submit: a.Args.Submit,
		Key: a.Args.Key, Direction: a.Args.Direction, MaxChars: int32(maxChars), TimeoutMs: timeout.Milliseconds(),
		MaxOutputBytes: int64(api.SandboxTaskOutputMaxBytes)}}}
	if err := as.send(frame); err != nil {
		s.endAction(ctx, as, a.ID, nil, []byte("the session's VM is gone"))
	}
}

// actionDone records the end vmmd reported. A command that could not run
// is exit code -1 with its reason as the output.
func (s *Sandboxes) actionDone(ctx context.Context, ss *session, d *scheddpb.SandboxRunActionDone) {
	s.actions.mu.Lock()
	as := s.actions.sessions[ss.claim.ID]
	s.actions.mu.Unlock()
	if as == nil {
		return
	}
	code, output := int(d.GetExitCode()), d.GetOutput()
	if d.GetError() != "" {
		code, output = -1, []byte(d.GetError())
	} else if d.GetTimedOut() {
		code, output = -1, []byte("the browser action timed out")
	}
	s.endAction(ctx, as, d.GetActionId(), &code, output)
}

// endAction records an action's end once and frees its session for the
// next. Only the action the session runs ends; a late end of an action
// already ended is dropped.
func (s *Sandboxes) endAction(ctx context.Context, as *actionSession, id string, code *int, output []byte) {
	s.actions.mu.Lock()
	if as.running != id {
		s.actions.mu.Unlock()
		return
	}
	as.running = ""
	s.actions.mu.Unlock()
	if store := s.actionStore(); store != nil {
		if err := store.FinishSandboxAction(context.WithoutCancel(ctx), id, as.ss.claim.LeaseToken, code, output); err != nil {
			s.log.Warn("agentd: finish sandbox action", "sandbox_id", as.ss.claim.ID, "action_id", id, "err", err)
		}
	}
}

// endOpenActions ends a session's actions in states: running ones as a
// fresh claim starts, since their VM is gone, and every open one as the
// session ends.
func (s *Sandboxes) endOpenActions(ctx context.Context, claim agentstore.SandboxClaim, states ...string) {
	store := s.actionStore()
	if store == nil || !claim.Browser || claim.Task != nil {
		return
	}
	if err := store.EndOpenSandboxActions(context.WithoutCancel(ctx), claim.ID, claim.LeaseToken, states...); err != nil {
		s.log.Warn("agentd: end sandbox actions", "sandbox_id", claim.ID, "err", err)
	}
}

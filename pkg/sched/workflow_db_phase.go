package sched

import "context"

// Workflow dispatch slots wait on customer handlers for up to a plan's step
// timeout, so schedd runs many (api.WorkflowDispatchSlots), while its
// database pool is small and shared with wakes. A goroutine executing a
// dispatch slot holds one of api.WorkflowDispatchDBPhaseSlots tokens while it
// reads or writes workflow state, and gives it back while it waits on a
// handler call or on its child goroutines (ADR-20261025232118). No holder
// waits on another goroutine while it holds a token, so the tokens cannot
// deadlock.

type workflowDBPhase struct {
	sem chan struct{}
}

func newWorkflowDBPhase(slots int) *workflowDBPhase {
	return &workflowDBPhase{sem: make(chan struct{}, slots)}
}

// workflowDBPhaseToken belongs to one goroutine and is never shared.
type workflowDBPhaseToken struct {
	phase *workflowDBPhase
	held  bool
}

type workflowDBPhaseKey struct{}

func (t *workflowDBPhaseToken) leave() {
	if t.held {
		t.held = false
		<-t.phase.sem
	}
}

// enterDBPhase gives the calling goroutine its own token.
func (o *WorkflowOrchestrator) enterDBPhase(ctx context.Context) (context.Context, func(), error) {
	if o.dbPhase == nil {
		return ctx, func() {}, nil
	}
	select {
	case o.dbPhase.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx, func() {}, ctx.Err()
	}
	token := &workflowDBPhaseToken{phase: o.dbPhase, held: true}
	return context.WithValue(ctx, workflowDBPhaseKey{}, token), token.leave, nil
}

// childDBPhase gives a goroutine started by a token holder its own token, so
// the parent's token is never released twice. Goroutines started outside a
// dispatch slot stay untracked.
func (o *WorkflowOrchestrator) childDBPhase(ctx context.Context) (context.Context, func()) {
	if parent, _ := ctx.Value(workflowDBPhaseKey{}).(*workflowDBPhaseToken); parent == nil {
		return ctx, func() {}
	}
	child, leave, err := o.enterDBPhase(ctx)
	if err != nil {
		return context.WithValue(ctx, workflowDBPhaseKey{}, (*workflowDBPhaseToken)(nil)), func() {}
	}
	return child, leave
}

// outsideDBPhase runs wait without the calling goroutine's token, then takes
// one again for the database work that follows. The token is retaken even if
// ctx is cancelled, because results are still recorded after cancellation;
// holders only keep a token for bounded database work, so the wait is short.
func outsideDBPhase(ctx context.Context, wait func()) {
	token, _ := ctx.Value(workflowDBPhaseKey{}).(*workflowDBPhaseToken)
	if token == nil || !token.held {
		wait()
		return
	}
	token.leave()
	defer func() {
		token.phase.sem <- struct{}{}
		token.held = true
	}()
	wait()
}

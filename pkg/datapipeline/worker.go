package datapipeline

import (
	"context"
	"errors"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type Worker struct {
	Store      Store
	Engine     *Engine
	Identities []*age.X25519Identity
	Allowed    func(context.Context, string) bool
}

// Tick reconciles one bounded source. Only metadata crosses the control API;
// native PostgreSQL handles the initial copy and ongoing row movement.
func (w *Worker) Tick(ctx context.Context) error {
	p, err := w.Store.Claim(ctx, uuid.NewString())
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	op, cancel := context.WithTimeout(ctx, api.DataPipelineOperationTimeout)
	defer cancel()
	c, err := w.Store.Connection(op, p.AccountID, p.SourceConnectionID)
	if err != nil {
		return w.Store.Finish(ctx, p, Observation{State: "blocked", Reason: "credential_unavailable", Verification: p.Verification})
	}
	raw, err := openConnection(w.Identities, c)
	if err != nil {
		return w.Store.Finish(ctx, p, Observation{State: "blocked", Reason: "credential_unavailable", Verification: p.Verification})
	}
	intent := p
	admitted := w.Allowed != nil && w.Allowed(op, p.AccountID)
	if !admitted && intent.DesiredState == "running" {
		intent.DesiredState = "paused"
	}
	ob := w.Engine.Reconcile(op, intent, raw, func(ctx context.Context) error {
		current, err := w.Store.Get(ctx, p.AccountID, p.ID)
		if err != nil {
			return err
		}
		if current.Revision != p.Revision || current.LeaseToken != p.LeaseToken || !current.LeaseUntil.After(time.Now()) {
			return ErrConflict
		}
		return nil
	})
	if !admitted && p.DesiredState == "running" && ob.State == "paused" {
		ob.State, ob.Reason = "blocked", "admission_closed"
	}
	return w.Store.Finish(ctx, p, ob)
}
func (w *Worker) Run(ctx context.Context) error {
	if w.Store == nil || w.Engine == nil || len(w.Identities) == 0 {
		return ErrInvalid
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := w.Tick(ctx); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

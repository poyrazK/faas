package datapipeline

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func (e *Engine) finishEvolution(ctx context.Context, p Pipeline, target *pgx.Conn, owner string, ob *Observation, current func(context.Context) error) error {
	if p.Serving == nil || p.Serving.Version != 2 {
		return nil
	}
	if err := reconcileRelationships(ctx, p, target, owner, current); err != nil {
		return &blocked{"relationship_contract_unavailable", false}
	}
	if p.Serving.AuthOrganizations != nil {
		until, _ := time.Parse(time.RFC3339Nano, tenantAccess(p).ValidUntil)
		if !until.After(time.Now()) {
			ob.State, ob.Reason = "blocked", "auth_membership_unconfirmed"
			return nil
		}
	}
	if ready, err := e.refreshRelationships(ctx, p, target, ob, current); err != nil || !ready {
		return err
	}
	c := p.Evolution
	if c == nil || c.State != "approved" || c.Generation != p.Generation {
		return nil
	}
	if c.Kind == "projection" {
		change := ob.SchemaChange
		if change == nil {
			change = p.SchemaChange
		}
		if change == nil || change.State != "complete" {
			return nil
		}
	}
	ob.EvolutionApplied = true
	return nil
}

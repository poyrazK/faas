// Package commit connects a customer PostgreSQL transaction to durable Gregale work.
package commit

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// Schema is the supported customer outbox schema. Installation is an explicit
// owner action; inserting an event never performs DDL or contacts Gregale.
//
//go:embed schema.sql
var Schema string

// UpgradeSchema explicitly upgrades the earlier internal invocation-only outbox.
// Only the database owner executes this DDL; the relay never changes its schema.
//
//go:embed schema_operations_upgrade.sql
var UpgradeSchema string

// RoutingUpgradeSchema is an explicit owner upgrade for version 2 routing.
//
//go:embed schema_routing_upgrade.sql
var RoutingUpgradeSchema string

type Event struct {
	ID      string             `json:"id"`
	Type    string             `json:"type"`
	Data    json.RawMessage    `json:"data"`
	Routing *api.CommitRouting `json:"routing,omitempty"`
}

func (e Event) Validate() error {
	if _, err := uuid.Parse(e.ID); err != nil {
		return errors.New("commit: event ID must be a UUID")
	}
	if !utf8.ValidString(e.Type) || utf8.RuneCountInString(e.Type) < 1 || utf8.RuneCountInString(e.Type) > 256 {
		return errors.New("commit: event type must contain 1-256 characters")
	}
	if !json.Valid(e.Data) {
		return errors.New("commit: event data must be valid JSON")
	}
	if _, err := NormalizeRouting(e.Routing); err != nil {
		return err
	}
	return nil
}

// Insert writes through the existing business transaction. The caller owns
// commit/rollback. Reusing an ID is an error, rather than silently dropping a
// potentially different business event.
func Insert(ctx context.Context, tx pgx.Tx, e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if tx == nil {
		return errors.New("commit: an existing transaction is required")
	}
	if e.Routing == nil {
		_, err := tx.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES ($1::uuid,$2,$3::jsonb)`, e.ID, e.Type, []byte(e.Data))
		return err
	}
	routing, err := NormalizeRouting(e.Routing)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload,routing) VALUES ($1::uuid,$2,$3::jsonb,$4::jsonb)`, e.ID, e.Type, []byte(e.Data), routing)
	return err
}

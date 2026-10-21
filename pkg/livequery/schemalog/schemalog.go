// Package schemalog prepares a database for live queries
// (ADR-20261016151947). pgoutput carries no DDL, so an event trigger logs
// every DDL command into gregale_live.ddl_log, which the engine reads when a
// command commits; and the publication the engine's slot decodes must exist.
//
// It has no dependency on the SQL parser, so apid and the providers that
// prepare a database for live queries can use it without linking the parser.
package schemalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Publication is the publication live queries decode by default.
const Publication = "gregale_live"

// Slot is the replication slot live queries decode a database with, named
// for its ID. Slot names are unique per PostgreSQL server, not per database,
// and databases may share a server (a Neon project's, for one), so each
// database needs its own. apid and livequeryd must agree on it.
func Slot(databaseID string) string {
	slot := "gregale_live_" + strings.ReplaceAll(strings.ToLower(databaseID), "-", "_")
	return slot[:min(len(slot), 63)]
}

// SQL creates the schema-change log and its event triggers. It needs the
// right to create event triggers (a superuser on stock PostgreSQL) and is
// idempotent.
const SQL = `
CREATE SCHEMA IF NOT EXISTS gregale_live;
CREATE TABLE IF NOT EXISTS gregale_live.ddl_log (
	id bigserial PRIMARY KEY,
	at timestamptz NOT NULL DEFAULT now(),
	tag text NOT NULL,
	objects oid[] NOT NULL,
	broad boolean NOT NULL
);
CREATE OR REPLACE FUNCTION gregale_live.record_ddl() RETURNS event_trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
	objs oid[] := '{}';
	wide boolean := false;
	seen boolean := false;
	r record;
BEGIN
	-- Commands on the log's own schema (Install replacing this function)
	-- change no live query.
	IF TG_EVENT = 'sql_drop' THEN
		FOR r IN SELECT classid, objid, schema_name FROM pg_event_trigger_dropped_objects() LOOP
			CONTINUE WHEN r.schema_name IS NOT DISTINCT FROM 'gregale_live';
			seen := true;
			IF r.classid = 'pg_class'::regclass THEN
				objs := objs || r.objid;
			ELSE
				wide := true;
			END IF;
		END LOOP;
	ELSE
		FOR r IN SELECT classid, objid, schema_name FROM pg_event_trigger_ddl_commands() LOOP
			CONTINUE WHEN r.schema_name IS NOT DISTINCT FROM 'gregale_live';
			seen := true;
			IF r.classid = 'pg_class'::regclass THEN
				objs := objs || r.objid;
			ELSE
				wide := true;
			END IF;
		END LOOP;
	END IF;
	IF NOT seen THEN
		RETURN;
	END IF;
	-- The rows only need to reach the slot; old ones serve no one.
	DELETE FROM gregale_live.ddl_log WHERE at < now() - interval '1 hour';
	INSERT INTO gregale_live.ddl_log (tag, objects, broad) VALUES (TG_TAG, objs, wide);
END
$$;
DROP EVENT TRIGGER IF EXISTS gregale_live_ddl;
DROP EVENT TRIGGER IF EXISTS gregale_live_drop;
CREATE EVENT TRIGGER gregale_live_ddl ON ddl_command_end EXECUTE FUNCTION gregale_live.record_ddl();
CREATE EVENT TRIGGER gregale_live_drop ON sql_drop EXECUTE FUNCTION gregale_live.record_ddl();`

// Install creates the schema-change log in tx.
func Install(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, SQL); err != nil {
		return fmt.Errorf("schemalog: install schema-change log: %w", err)
	}
	return nil
}

// Querier runs SQL; a pgx.Tx, *pgx.Conn or *pgxpool.Pool is one.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// EnsurePublication creates the publication FOR ALL TABLES when it is
// missing. Creating one FOR ALL TABLES needs a superuser on stock PostgreSQL.
func EnsurePublication(ctx context.Context, q Querier, name string) error {
	var exists bool
	if err := q.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)", name).Scan(&exists); err != nil {
		return fmt.Errorf("schemalog: look up publication: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := q.Exec(ctx, "CREATE PUBLICATION "+quoteIdent(name)+" FOR ALL TABLES"); err != nil {
		return fmt.Errorf("schemalog: create publication: %w", err)
	}
	return nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

package livequery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema changes (ADR-1384). pgoutput does not carry DDL, so Install adds an
// event trigger that logs every DDL command into gregale_live.ddl_log. The
// table is published like any other, so a command's log row is decoded in
// the round where the command commits. That round recompiles every plan
// group reading a table the command touched, or every group when it touched
// something else (a function, a type, a schema). A group that no longer
// compiles closes its subscriptions with schema_changed; one whose result
// columns changed starts over with a full result; any other is re-read at
// the round snapshot and diffed, so subscribers see ordinary changes.
//
// The round's ACCESS SHARE locks keep DDL on live tables out of a round in
// progress, so the catalog a round recompiles against is the one its
// snapshot sees.

// QueryError codes.
const (
	// SchemaChanged closes a query that no longer compiles.
	SchemaChanged = "schema_changed"
	// LimitExceeded closes a query whose result outgrew its limit.
	LimitExceeded = "live_query_limit"
)

const installSQL = `
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

// Install creates the schema-change log and its event triggers. It needs a
// superuser, is idempotent, and must run before the engine's first round
// for schema changes to be noticed.
func Install(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("livequery: install: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, installSQL); err != nil {
		return fmt.Errorf("livequery: install schema-change log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("livequery: install: %w", err)
	}
	return nil
}

// QueryError says why a subscription ended.
type QueryError struct {
	Code    string
	Message string
	// Limit and Observed describe a live_query_limit error.
	Limit, Observed int64
}

// schemaChange is what a round's DDL touched.
type schemaChange struct {
	tables map[uint32]bool
	broad  bool
}

// schemaChanges reads the round's DDL log rows, or returns nil.
func (e *Engine) schemaChanges(txns []*Txn) *schemaChange {
	if e.ddlLog == 0 {
		return nil
	}
	var sc *schemaChange
	for _, txn := range txns {
		for _, ch := range txn.Changes {
			if ch.Relation != e.ddlLog || ch.New == nil {
				continue
			}
			if sc == nil {
				sc = &schemaChange{tables: map[uint32]bool{}}
			}
			if v := ch.New["broad"]; v == nil || *v != "f" {
				sc.broad = true
			}
			objects := ch.New["objects"]
			if objects == nil {
				sc.broad = true
				continue
			}
			for _, raw := range strings.Split(strings.Trim(*objects, "{}"), ",") {
				if raw == "" {
					continue
				}
				oid, err := strconv.ParseUint(raw, 10, 32)
				if err != nil {
					sc.broad = true
					continue
				}
				sc.tables[uint32(oid)] = true
			}
		}
	}
	return sc
}

// applySchemaChange recompiles the plan groups the change touched and
// returns the deltas that close subscriptions whose query no longer
// compiles.
func (e *Engine) applySchemaChange(ctx context.Context, tx pgx.Tx, sc *schemaChange) ([]Delta, error) {
	var closed []Delta
	for _, g := range slices.Clone(e.groups) {
		touched := sc.broad
		for oid := range g.plan.TableNames {
			touched = touched || sc.tables[oid]
		}
		if !touched {
			continue
		}
		e.Stats.Recompiles++
		plan, err := recompile(ctx, tx, g.plan.SQL)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			closed = append(closed, e.closeGroup(g, &QueryError{Code: SchemaChanged, Message: err.Error()})...)
			continue
		}
		bad, err := g.rerender(ctx, tx, plan)
		if err != nil {
			return nil, err
		}
		for _, m := range bad {
			closed = append(closed, e.closeMember(m, &QueryError{Code: SchemaChanged, Message: "the query's arguments no longer fit its parameter types"})...)
		}
		if len(g.members) == 0 {
			continue
		}
		unlocked := false
		for oid := range plan.TableNames {
			unlocked = unlocked || !e.locked[oid]
		}
		outcome := "re-read and diffed"
		switch {
		case unlocked:
			outcome = "deferred to the next round"
			// A table this round did not lock: start over next round, which
			// locks it before its snapshot.
			g.plan = plan
			g.skip = true
			for _, m := range g.members {
				if err := m.reset(); err != nil {
					return nil, err
				}
			}
		case !sameShape(g.plan, plan):
			outcome = "reset"
			g.plan = plan
			for _, m := range g.members {
				if err := m.reset(); err != nil {
					return nil, err
				}
			}
		default:
			g.pendingPlan = plan
		}
		if e.Trace != nil {
			e.Trace(fmt.Sprintf("round %d recompiled %.80q: %s", e.round, g.plan.SQL, outcome))
		}
	}
	return closed, nil
}

// recompile compiles sql inside a savepoint: a failed statement must not
// abort the round transaction.
func recompile(ctx context.Context, tx pgx.Tx, sql string) (*Plan, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("livequery: savepoint: %w", err)
	}
	plan, err := Compile(ctx, sp.Conn(), sql)
	if err != nil {
		_ = sp.Rollback(ctx)
		return nil, err
	}
	if err := sp.Commit(ctx); err != nil {
		return nil, fmt.Errorf("livequery: release savepoint: %w", err)
	}
	return plan, nil
}

// rerender renders the members' arguments for the new plan's parameter
// types and returns the members whose arguments no longer fit.
func (g *planGroup) rerender(ctx context.Context, tx pgx.Tx, plan *Plan) ([]*member, error) {
	if slices.Equal(plan.ParamTypes, g.plan.ParamTypes) {
		return nil, nil
	}
	var bad []*member
	for _, m := range g.members {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("livequery: savepoint: %w", err)
		}
		text, err := argText(ctx, sp.Conn(), plan.ParamTypes, m.args)
		if err != nil {
			_ = sp.Rollback(ctx)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			bad = append(bad, m)
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			return nil, fmt.Errorf("livequery: release savepoint: %w", err)
		}
		m.argText = text
	}
	g.byArgs = map[string]*member{}
	for _, m := range g.members {
		g.byArgs[encodeValues(m.argText)] = m
	}
	return bad, nil
}

// sameShape reports whether subscribers can keep their results across the
// recompile: the same columns, ordering and parameters.
func sameShape(a, b *Plan) bool {
	return slices.Equal(a.Columns, b.Columns) && slices.Equal(a.ParamTypes, b.ParamTypes) &&
		(a.Order == nil) == (b.Order == nil) && (a.Order == nil || a.Order.Ordered == b.Order.Ordered)
}

// reset discards a member's state so the next round reads its result from
// scratch and sends it whole.
func (m *member) reset() error {
	m.ready = false
	m.result, m.input, m.join, m.semi, m.groups, m.agg = nil, nil, nil, nil, nil, nil
	m.win = nil
	if o := m.group.plan.Order; o != nil {
		w, err := newWindow(o, m.argText)
		if err != nil {
			return err
		}
		m.win = w
	}
	return nil
}

// closeGroup ends every subscription of a group.
func (e *Engine) closeGroup(g *planGroup, qe *QueryError) []Delta {
	var out []Delta
	for _, m := range slices.Clone(g.members) {
		out = append(out, e.closeMember(m, qe)...)
	}
	return out
}

// closeMember ends a member's subscriptions and removes it.
func (e *Engine) closeMember(m *member, qe *QueryError) []Delta {
	var out []Delta
	for _, id := range slices.Clone(m.ids) {
		out = append(out, Delta{Query: id, Closed: qe})
		e.unregister(id)
	}
	return out
}

// Unregister ends a subscription.
func (e *Engine) Unregister(id string) error {
	if _, ok := e.byID[id]; !ok {
		return fmt.Errorf("livequery: no subscription %s", id)
	}
	e.unregister(id)
	return nil
}

func (e *Engine) unregister(id string) {
	m := e.byID[id]
	if m == nil {
		return
	}
	delete(e.byID, id)
	m.ids = slices.DeleteFunc(m.ids, func(s string) bool { return s == id })
	if len(m.ids) > 0 {
		return
	}
	g := m.group
	g.members = slices.DeleteFunc(g.members, func(x *member) bool { return x == m })
	g.byArgs = map[string]*member{}
	for i, x := range g.members {
		x.idx = int32(i)
		g.byArgs[encodeValues(x.argText)] = x
	}
	if len(g.members) > 0 {
		return
	}
	e.groups = slices.DeleteFunc(e.groups, func(x *planGroup) bool { return x == g })
	delete(e.bySQL, g.plan.SQL)
}

// errSchemaLog is returned when the schema-change log cannot be resolved.
var errSchemaLog = errors.New("livequery: resolve gregale_live.ddl_log")

// resolveSchemaLog finds the DDL log table, which Install creates.
func (e *Engine) resolveSchemaLog(ctx context.Context, tx pgx.Tx) error {
	if e.ddlLog != 0 {
		return nil
	}
	var oid uint32
	if err := tx.QueryRow(ctx, "SELECT coalesce(to_regclass('gregale_live.ddl_log')::oid, 0)").Scan(&oid); err != nil {
		return fmt.Errorf("%w: %w", errSchemaLog, err)
	}
	e.ddlLog = oid
	return nil
}

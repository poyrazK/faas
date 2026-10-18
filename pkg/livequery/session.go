package livequery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Session is who a subscription reads as (ADR-20261016151947 RLS mode): every
// statement of its plan group runs as Role, with Claims as
// request.jwt.claims, so PostgreSQL's row-level security policies filter
// every row of every delta exactly as the Data API does. Subscriptions share
// a plan group only when their query and session are identical.
type Session struct {
	Role   string
	Claims string // a JSON object
}

func (s *Session) key() string {
	if s == nil {
		return ""
	}
	return "\x01" + s.Role + "\x01" + s.Claims
}

func (s *Session) validate() error {
	if s.Role == "" || strings.ContainsRune(s.Role, 0) {
		return fmt.Errorf("livequery: session role %q", s.Role)
	}
	var claims map[string]any
	if err := json.Unmarshal([]byte(s.Claims), &claims); err != nil {
		return fmt.Errorf("livequery: session claims must be a JSON object: %w", err)
	}
	return nil
}

// enter switches the round to the session; leave switches back. Both are
// local to the round's transaction.
const (
	enterSession = `SELECT set_config('role', $1, true), set_config('request.jwt.claims', $2, true)`
	leaveSession = `SELECT set_config('role', 'none', true), set_config('request.jwt.claims', '', true)`
)

func (s *Session) enter(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, enterSession, s.Role, s.Claims); err != nil {
		return fmt.Errorf("livequery: enter session %s: %w", s.Role, err)
	}
	return nil
}

func leave(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, leaveSession); err != nil {
		return fmt.Errorf("livequery: leave session: %w", err)
	}
	return nil
}

// checkRole verifies the engine's login may become role, and that role is
// subject to row-level security: live queries never read as a superuser or
// a role that bypasses it.
func checkRole(ctx context.Context, conn *pgx.Conn, role string) error {
	var member, privileged bool
	// From PostgreSQL 16 a membership may withhold SET ROLE; ask for it.
	err := conn.QueryRow(ctx, `SELECT CASE WHEN current_setting('server_version_num')::int >= 160000
		THEN pg_has_role(current_user, r.oid, 'SET') ELSE pg_has_role(current_user, r.oid, 'MEMBER') END,
		r.rolsuper OR r.rolbypassrls FROM pg_roles r WHERE r.rolname = $1`, role).Scan(&member, &privileged)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("livequery: no role %q", role)
	}
	if err != nil {
		return fmt.Errorf("livequery: check role %s: %w", role, err)
	}
	if privileged {
		return fmt.Errorf("livequery: role %q bypasses row-level security; live queries never read as it", role)
	}
	if !member {
		return fmt.Errorf("livequery: the live-query login cannot act as role %q; grant it", role)
	}
	return nil
}

// policyDeps is what the row-level security policies on a plan's tables
// read besides those tables. A change there can change which rows a role
// sees without changing any row the query reads, so a session group is
// re-read when one of these tables changes, or on every change when a
// policy calls a function whose reads are unknown (opaque).
type policyDeps struct {
	tables map[uint32]bool
	opaque bool
}

// maxPolicyDepth bounds the walk through policies, functions and views.
const maxPolicyDepth = 8

// readPolicyDeps walks the policies on tables, the tables and functions they
// reference, those tables' own policies, and what those functions read.
// PostgreSQL records a policy's direct references in pg_depend. A SQL
// function with a string body records none, so a probe (a BEGIN ATOMIC copy,
// created and rolled back) makes PostgreSQL record them; a function in any
// other language is opaque.
func readPolicyDeps(ctx context.Context, conn *pgx.Conn, tables map[uint32]bool) (policyDeps, error) {
	deps := policyDeps{tables: map[uint32]bool{}}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return deps, fmt.Errorf("livequery: read policies: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	w := &policyWalk{ctx: ctx, tx: tx, deps: &deps, seenRel: map[uint32]bool{}, seenFn: map[uint32]bool{}}
	for oid := range tables {
		w.seenRel[oid] = true
	}
	for oid := range tables {
		if err := w.relation(oid, 0, false); err != nil {
			return deps, err
		}
	}
	return deps, nil
}

type policyWalk struct {
	ctx             context.Context
	tx              pgx.Tx
	deps            *policyDeps
	seenRel, seenFn map[uint32]bool
	probes          int
}

// relation adds what reading oid under its policies depends on. A table the
// query does not itself read (dep) is recorded as a dependency.
func (w *policyWalk) relation(oid uint32, depth int, dep bool) error {
	if depth > maxPolicyDepth {
		w.deps.opaque = true
		return nil
	}
	var kind string
	if err := w.tx.QueryRow(w.ctx, `SELECT relkind FROM pg_class WHERE oid = $1`, oid).Scan(&kind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("livequery: read policies: %w", err)
	}
	if kind == "v" {
		// A view's rows come from what its rewrite rule reads.
		return w.references(`SELECT d.refclassid = 'pg_class'::regclass, d.refobjid FROM pg_rewrite r
			JOIN pg_depend d ON d.classid = 'pg_rewrite'::regclass AND d.objid = r.oid
			WHERE r.ev_class = $1 AND d.refobjid <> $1 AND d.refclassid IN ('pg_class'::regclass, 'pg_proc'::regclass)`, oid, depth)
	}
	if dep {
		w.deps.tables[oid] = true
	}
	return w.references(`SELECT d.refclassid = 'pg_class'::regclass, d.refobjid FROM pg_policy p
		JOIN pg_depend d ON d.classid = 'pg_policy'::regclass AND d.objid = p.oid
		WHERE p.polrelid = $1 AND d.refobjid <> $1 AND d.refclassid IN ('pg_class'::regclass, 'pg_proc'::regclass)`, oid, depth)
}

// references follows the relations and functions a catalog query names.
func (w *policyWalk) references(query string, oid uint32, depth int) error {
	rows, err := w.tx.Query(w.ctx, query, oid)
	if err != nil {
		return fmt.Errorf("livequery: read policies: %w", err)
	}
	type ref struct {
		rel bool
		oid uint32
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.rel, &r.oid); err != nil {
			rows.Close()
			return fmt.Errorf("livequery: read policies: %w", err)
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("livequery: read policies: %w", err)
	}
	for _, r := range refs {
		var err error
		if r.rel {
			if w.seenRel[r.oid] {
				continue
			}
			w.seenRel[r.oid] = true
			err = w.relation(r.oid, depth+1, true)
		} else {
			err = w.function(r.oid, depth+1)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// function adds what a function called by a policy reads.
func (w *policyWalk) function(oid uint32, depth int) error {
	if w.seenFn[oid] || w.deps.opaque {
		return nil
	}
	w.seenFn[oid] = true
	if depth > maxPolicyDepth {
		w.deps.opaque = true
		return nil
	}
	var (
		builtin, atomic bool
		lang, src, args string
		result          string
		config          []string
	)
	err := w.tx.QueryRow(w.ctx, `SELECT n.nspname IN ('pg_catalog', 'information_schema'), l.lanname, p.prosqlbody IS NOT NULL,
		p.prosrc, pg_get_function_arguments(p.oid), pg_get_function_result(p.oid), coalesce(p.proconfig, '{}')
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_language l ON l.oid = p.prolang
		WHERE p.oid = $1`, oid).Scan(&builtin, &lang, &atomic, &src, &args, &result, &config)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("livequery: read policy function: %w", err)
	}
	switch {
	case builtin || lang == "internal" || lang == "c":
		return nil // reads no customer table
	case lang != "sql":
		w.deps.opaque = true
		return nil
	case atomic:
		return w.functionRefs(oid, depth)
	}
	probe, err := w.probe(src, args, result, config)
	if err != nil {
		return err
	}
	if probe == 0 {
		w.deps.opaque = true
		return nil
	}
	return w.functionRefs(probe, depth)
}

func (w *policyWalk) functionRefs(oid uint32, depth int) error {
	return w.references(`SELECT d.refclassid = 'pg_class'::regclass, d.refobjid FROM pg_depend d
		WHERE d.classid = 'pg_proc'::regclass AND d.objid = $1 AND d.refobjid <> $1
		AND d.refclassid IN ('pg_class'::regclass, 'pg_proc'::regclass)`, oid, depth)
}

// probe creates a BEGIN ATOMIC copy of a SQL function so PostgreSQL records
// what its body references. It returns 0 when the body cannot be copied
// (the function is then opaque). The copy is only created, never called,
// in a transaction that is rolled back; the extended protocol refuses a body
// that would smuggle in a second statement.
func (w *policyWalk) probe(src, args, result string, config []string) (uint32, error) {
	w.probes++
	body := strings.TrimRight(strings.TrimSpace(src), "; \t\n")
	if body == "" {
		return 0, nil
	}
	savepoint := fmt.Sprintf("lq_probe_%d", w.probes)
	if _, err := w.tx.Exec(w.ctx, "SAVEPOINT "+savepoint); err != nil {
		return 0, fmt.Errorf("livequery: probe policy function: %w", err)
	}
	for _, setting := range config {
		if name, value, ok := strings.Cut(setting, "="); ok && name == "search_path" {
			if _, err := w.tx.Exec(w.ctx, `SELECT set_config('search_path', $1, true)`, value); err != nil {
				return 0, fmt.Errorf("livequery: probe policy function: %w", err)
			}
		}
	}
	name := fmt.Sprintf("lq_probe_%d", w.probes)
	var oid uint32
	_, err := w.tx.Exec(w.ctx, fmt.Sprintf("CREATE FUNCTION pg_temp.%s(%s) RETURNS %s LANGUAGE sql BEGIN ATOMIC %s; END", name, args, result, body))
	if err == nil {
		err = w.tx.QueryRow(w.ctx, `SELECT oid FROM pg_proc WHERE proname = $1 AND pronamespace = pg_my_temp_schema()`, name).Scan(&oid)
	}
	var pgErr *pgconn.PgError
	if err != nil && !errors.As(err, &pgErr) {
		return 0, fmt.Errorf("livequery: probe policy function: %w", err)
	}
	if err != nil {
		oid = 0 // the body did not copy: opaque
		if _, err := w.tx.Exec(w.ctx, "ROLLBACK TO SAVEPOINT "+savepoint); err != nil {
			return 0, fmt.Errorf("livequery: probe policy function: %w", err)
		}
	}
	return oid, nil
}

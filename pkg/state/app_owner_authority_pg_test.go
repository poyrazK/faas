//go:build !no_pg

// adr: 20261110210000
package state_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// ownerAuthorityLogin opens a pool that logs in as a fresh LOGIN role shaped
// like a runtime role: row DML on every table except the owner-authority
// tables (as runtime-roles.sql leaves faas_runtime). With authority it is
// also a member of faas_owner_authority, like the control plane's schedd.
// The trigger keys on session_user, so this must be a real login, not SET
// ROLE; the test skips where the cluster does not admit one.
func ownerAuthorityLogin(t *testing.T, owner *pgxpool.Pool, authority bool) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	name := "faas_oa_" + hex.EncodeToString(raw[:5])
	password := hex.EncodeToString(raw)
	role := pgx.Identifier{name}.Sanitize()
	statements := []string{
		"CREATE ROLE " + role + " LOGIN PASSWORD '" + password + "'",
		"GRANT USAGE ON SCHEMA public TO " + role,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + role,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO " + role,
		"REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON app_owner_authority FROM " + role,
		"REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON app_owner_writes FROM " + role,
	}
	if authority {
		statements = append(statements,
			`DO $$ BEGIN
				IF to_regrole('faas_owner_authority') IS NULL THEN CREATE ROLE faas_owner_authority NOLOGIN; END IF;
			EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $$`,
			"GRANT faas_owner_authority TO "+role)
	}
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DROP OWNED BY "+role)
		_, _ = owner.Exec(context.Background(), "DROP ROLE "+role)
	})
	for _, stmt := range statements {
		if _, err := owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	cfg := owner.Config().Copy()
	cfg.ConnConfig.User = name
	cfg.ConnConfig.Password = password
	delete(cfg.ConnConfig.RuntimeParams, "role")
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cluster does not admit a fresh login role (%v); the owner-authority trigger needs a real session_user", err)
	}
	return pool, name
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

type ownerWrite struct {
	from, to, login string
	authorized      bool
	mode            string
}

func lastOwnerWrite(t *testing.T, pool *pgxpool.Pool, appID string) (ownerWrite, int) {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `select count(*) from app_owner_writes where app_id = $1`, appID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return ownerWrite{}, 0
	}
	var w ownerWrite
	var from *string
	if err := pool.QueryRow(t.Context(), `
		select from_node_id::text, to_node_id::text, login_role, authorized, mode
		  from app_owner_writes where app_id = $1 order by id desc limit 1`, appID).
		Scan(&from, &w.to, &w.login, &w.authorized, &w.mode); err != nil {
		t.Fatal(err)
	}
	if from != nil {
		w.from = *from
	}
	return w, n
}

func TestAppOwnerAuthorityTrigger(t *testing.T) {
	store, owner := lifecycleCompletionPGStore(t)
	ctx := t.Context()
	compute, computeLogin := ownerAuthorityLogin(t, owner, false)
	control, controlLogin := ownerAuthorityLogin(t, owner, true)
	app, _ := completionApplication(t, store)
	a := completionNode(t, store, state.NodeLifecycleActive)
	b := completionNode(t, store, state.NodeLifecycleActive)

	setMode := func(mode string) {
		t.Helper()
		if _, err := owner.Exec(ctx, `update app_owner_authority set mode = $1, updated_at = now()`, mode); err != nil {
			t.Fatal(err)
		}
	}
	stamp := func(p *pgxpool.Pool, node string) error {
		_, err := p.Exec(ctx, `update apps set node_id = nullif($2, '')::uuid where id = $1`, app, node)
		return err
	}
	ownerOf := func() string {
		t.Helper()
		got, err := store.AppByID(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		return got.NodeID
	}

	// Default: any login may set the owner, and nothing is recorded.
	var mode string
	if err := owner.QueryRow(ctx, `select mode from app_owner_authority`).Scan(&mode); err != nil || mode != "any" {
		t.Fatalf("default mode = %q, %v; want any", mode, err)
	}
	if err := stamp(compute, a.ID); err != nil {
		t.Fatalf("any: compute owner write refused: %v", err)
	}
	if _, n := lastOwnerWrite(t, owner, app); n != 0 {
		t.Fatalf("any recorded %d writes", n)
	}

	// observe: every owner change is admitted and recorded with its login.
	setMode("observe")
	if err := stamp(compute, b.ID); err != nil {
		t.Fatalf("observe: compute owner write refused: %v", err)
	}
	w, _ := lastOwnerWrite(t, owner, app)
	if w != (ownerWrite{from: a.ID, to: b.ID, login: computeLogin, authorized: false, mode: "observe"}) {
		t.Fatalf("observe record for the compute write = %+v", w)
	}
	if err := stamp(control, a.ID); err != nil {
		t.Fatal(err)
	}
	if w, _ := lastOwnerWrite(t, owner, app); !w.authorized || w.login != controlLogin {
		t.Fatalf("observe record for the control-plane write = %+v", w)
	}
	// The control plane's metric reads the mode and the unauthorized count.
	status, err := store.AppOwnerAuthorityStatus(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if status != (state.AppOwnerAuthorityStatus{Mode: "observe", Unauthorized: 1}) {
		t.Fatalf("status = %+v, want observe with 1 unauthorized write", status)
	}
	// An unchanged owner is not a write.
	_, before := lastOwnerWrite(t, owner, app)
	if _, err := control.Exec(ctx, `update apps set node_id = node_id where id = $1`, app); err != nil {
		t.Fatal(err)
	}
	if _, after := lastOwnerWrite(t, owner, app); after != before {
		t.Fatalf("a no-op owner update was recorded (%d -> %d)", before, after)
	}

	// enforce: a compute login cannot move the owner; the control plane can.
	setMode("enforce")
	if err := stamp(compute, b.ID); sqlState(err) != "42501" {
		t.Fatalf("enforce: compute owner move = %v, want SQLSTATE 42501", err)
	}
	if got := ownerOf(); got != a.ID {
		t.Fatalf("refused move still changed the owner to %s", got)
	}
	if err := stamp(control, b.ID); err != nil {
		t.Fatalf("enforce: control-plane owner move refused: %v", err)
	}
	// Clearing hands the app back to the control plane's claim.
	if err := stamp(compute, ""); err != nil {
		t.Fatalf("enforce: clearing the owner refused: %v", err)
	}
	if err := stamp(compute, a.ID); sqlState(err) != "42501" {
		t.Fatalf("enforce: compute claim of an unplaced app = %v, want SQLSTATE 42501", err)
	}

	// A temporary table cannot stand in for the mode.
	conn, err := compute.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `create temporary table app_owner_authority (singleton boolean, mode text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `insert into app_owner_authority values (true, 'any')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `update apps set node_id = $2 where id = $1`, app, a.ID); sqlState(err) != "42501" {
		t.Fatalf("a temporary app_owner_authority bypassed enforce: %v", err)
	}

	// A compute login can neither switch the check off nor erase the record.
	if _, err := compute.Exec(ctx, `update app_owner_authority set mode = 'any'`); sqlState(err) != "42501" {
		t.Fatalf("compute changed the mode: %v", err)
	}
	if _, err := compute.Exec(ctx, `delete from app_owner_writes`); sqlState(err) != "42501" {
		t.Fatalf("compute deleted owner records: %v", err)
	}
	if _, err := compute.Exec(ctx, `insert into app_owner_writes (app_id, to_node_id, login_role, authorized, mode) values ($1, $2, 'x', true, 'observe')`, app, a.ID); sqlState(err) != "42501" {
		t.Fatalf("compute forged an owner record: %v", err)
	}

	// A compute login cannot give its own name to the owner's node row
	// (the control plane maps an mTLS certificate's name to a node id).
	if _, err := compute.Exec(ctx, `update compute_nodes set name = $2 where id = $1`, a.ID, "renamed-"+a.Name); sqlState(err) != "42501" {
		t.Fatalf("enforce: compute renamed a node: %v", err)
	}
	if _, err := compute.Exec(ctx, `update compute_nodes set last_heartbeat_at = now() where id = $1`, a.ID); err != nil {
		t.Fatalf("enforce: an ordinary node update was refused: %v", err)
	}
	if _, err := control.Exec(ctx, `update compute_nodes set name = $2 where id = $1`, b.ID, b.Name+"-renamed"); err != nil {
		t.Fatalf("enforce: control-plane rename refused: %v", err)
	}

	// The control plane's own claim path still works under enforce.
	if err := state.NewPgStore(control).SetAppNodeID(ctx, app, a.ID); err != nil {
		t.Fatalf("control-plane SetAppNodeID under enforce: %v", err)
	}
	if got := ownerOf(); got != a.ID {
		t.Fatalf("owner = %s, want %s", got, a.ID)
	}
}

// Release requests on Postgres: one open request per app, refreshed by a
// repeat, listed oldest first, and deleted only while unchanged.
func TestAppOwnerReleaseRequestsPostgres(t *testing.T) {
	store, _ := lifecycleCompletionPGStore(t)
	ctx := t.Context()
	first, _ := completionApplication(t, store)
	second, _ := completionApplication(t, store)
	node := completionNode(t, store, state.NodeLifecycleActive)

	for _, app := range []string{first, second} {
		if err := store.RequestAppOwnerRelease(ctx, app, node.ID); err != nil {
			t.Fatal(err)
		}
	}
	reqs, err := store.ListAppOwnerReleaseRequests(ctx, 10)
	if err != nil || len(reqs) != 2 || reqs[0].AppID != first || reqs[1].AppID != second || reqs[0].FromNodeID != node.ID {
		t.Fatalf("requests = %+v, %v", reqs, err)
	}
	if limited, _ := store.ListAppOwnerReleaseRequests(ctx, 1); len(limited) != 1 {
		t.Fatalf("limit 1 returned %d", len(limited))
	}
	stale := reqs[0]
	if err := store.RequestAppOwnerRelease(ctx, first, node.ID); err != nil {
		t.Fatal(err)
	}
	// The refreshed request survives a delete of the version read before it.
	if err := store.DeleteAppOwnerReleaseRequest(ctx, first, stale.RequestedAt); err != nil {
		t.Fatal(err)
	}
	reqs, _ = store.ListAppOwnerReleaseRequests(ctx, 10)
	if len(reqs) != 2 {
		t.Fatalf("a stale delete removed the refreshed request: %+v", reqs)
	}
	for _, r := range reqs {
		if err := store.DeleteAppOwnerReleaseRequest(ctx, r.AppID, r.RequestedAt); err != nil {
			t.Fatal(err)
		}
	}
	if reqs, _ := store.ListAppOwnerReleaseRequests(ctx, 10); len(reqs) != 0 {
		t.Fatalf("requests left after delete: %+v", reqs)
	}
}

//go:build !no_pg

package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestAPIKeyScopeSchemaParityAndQueueConsumeUpgrade(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE api_keys(scopes text[] NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	read := func(name string) (string, string) {
		t.Helper()
		raw, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		up, down, ok := strings.Cut(string(raw), "-- +goose Down")
		if !ok {
			t.Fatal("missing Down migration")
		}
		return up, down
	}
	old, _ := read("20261029085359401_api_key_agent_scope.sql")
	if _, err := pool.Exec(ctx, old); err != nil {
		t.Fatal(err)
	}
	insert := func(scope string) error {
		_, err := pool.Exec(ctx, `INSERT INTO api_keys(scopes) VALUES($1)`, []string{scope})
		return err
	}
	constraintFailure := func(err error) {
		t.Helper()
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatalf("want CHECK rejection, got %v", err)
		}
	}
	constraintFailure(insert(ScopeQueuesConsume))
	up, down := read("20261114190000001_queue_consume_api_key_scope.sql")
	for range 2 {
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatal(err)
		}
	}
	// v5 (ADR-20261117232109) adds data:read on top of v4.
	constraintFailure(insert(ScopeDataRead))
	upV5, downV5 := read("20261119170402100_api_key_data_read_scope.sql")
	for range 2 {
		if _, err := pool.Exec(ctx, upV5); err != nil {
			t.Fatal(err)
		}
	}
	for scope := range validScopes {
		if err := insert(scope); err != nil {
			t.Errorf("API accepts %s but migrated DB rejects it: %v", scope, err)
		}
	}
	constraintFailure(insert("not:a:scope"))
	if _, err := pool.Exec(ctx, `INSERT INTO api_keys(scopes) VALUES('{}')`); err == nil {
		t.Fatal("empty scope set accepted")
	}
	if _, err := pool.Exec(ctx, downV5); err == nil {
		t.Fatal("rollback allowed active data:read keys")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM api_keys WHERE scopes && ARRAY['data:read']`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, downV5); err != nil {
		t.Fatal(err)
	}
	constraintFailure(insert(ScopeDataRead))
	if _, err := pool.Exec(ctx, down); err == nil {
		t.Fatal("rollback allowed active consume keys")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM api_keys WHERE scopes && ARRAY['queues:consume']`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	constraintFailure(insert(ScopeQueuesConsume))
	if err := insert(ScopeAgent); err != nil {
		t.Fatal("rollback lost previous agent scope", err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := insert(ScopeQueuesConsume); err != nil {
		t.Fatal(err)
	}
}

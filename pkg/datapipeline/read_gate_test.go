package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPipelineReadHookDistinguishesUnavailableEmptyAndUnauthorized(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	role := "pipeline_gate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(ctx, "CREATE ROLE "+ident(role)+" LOGIN NOINHERIT"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), "DROP OWNED BY "+ident(role)+";DROP ROLE "+ident(role))
	})
	p := Pipeline{AccountID: uuid.NewString(), DataPipeline: api.DataPipeline{ID: uuid.NewString(), Generation: 1, Tables: []string{"orders", "customers"}, ReadSubjects: []string{"alice"}}}
	_, err := pool.Exec(ctx, `CREATE SCHEMA gregale_pipeline;
 CREATE TABLE gregale_pipeline.mirrors(pipeline_id uuid PRIMARY KEY,generation bigint NOT NULL,tables text[],readable boolean DEFAULT false,retiring boolean DEFAULT false);
 CREATE TABLE public.orders(id integer PRIMARY KEY);
 CREATE TABLE public.customers(id integer PRIMARY KEY);
 ALTER TABLE public.orders ENABLE ROW LEVEL SECURITY;
 ALTER TABLE public.customers ENABLE ROW LEVEL SECURITY;
 CREATE POLICY gregale_pipeline_read ON public.orders FOR SELECT USING(false);
 CREATE POLICY gregale_pipeline_read ON public.customers FOR SELECT USING(true)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range p.Tables {
		if _, err = pool.Exec(ctx, "COMMENT ON TABLE "+tableIdent(table)+" IS "+literal(marker(p))+";GRANT SELECT ON "+tableIdent(table)+" TO "+ident(role)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `INSERT INTO gregale_pipeline.mirrors(pipeline_id,generation,tables) VALUES($1,$2,$3)`, p.ID, p.Generation, p.Tables); err != nil {
		t.Fatal(err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if err = ensurePipelineReadGate(ctx, p, connection.Conn()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, subject, path string
		unavailable         bool
	}{
		{"authorized_empty_gated", "alice", "/orders", true},
		{"unauthorized_gated", "bob", "/orders", false},
		{"healthy_empty_table", "alice", "/customers", false},
		{"metadata", "alice", "/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := connection.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err = tx.Exec(ctx, "SET TRANSACTION READ ONLY;SET LOCAL ROLE "+ident(role)); err != nil {
				t.Fatal(err)
			}
			claims, _ := json.Marshal(map[string]any{"sub": tc.subject, "gregale_pipeline_request": map[string]any{"version": 1, "previous_hook": ""}})
			if _, err = tx.Exec(ctx, `SELECT set_config('request.jwt.claims',$1,true),set_config('request.path',$2,true)`, string(claims), tc.path); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `SELECT gregale_data_api.check_pipeline_request()`)
			if tc.unavailable {
				var pg *pgconn.PgError
				if !errors.As(err, &pg) || pg.Code != "PGRST" || !strings.Contains(pg.Message, `"code":"mirror_unavailable"`) || !strings.Contains(pg.Detail, `"status":503`) {
					t.Fatal("missing availability error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var rows int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("RLS changed", rows, err)
			}
			if _, err = tx.Exec(ctx, `SELECT subjects FROM gregale_pipeline.reader_access`); err == nil {
				t.Fatal("serving role gained private receipt access")
			}
		})
	}
	if _, err = pool.Exec(ctx, "ALTER POLICY gregale_pipeline_read ON public.orders USING ("+readerPredicate(p.ReadSubjects)+")"); err != nil {
		t.Fatal(err)
	}
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(role)+`;SELECT set_config('request.jwt.claims','{"sub":"alice","gregale_pipeline_request":{"version":1,"previous_hook":""}}',true),set_config('request.path','/orders',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT gregale_data_api.check_pipeline_request()`); err != nil {
		t.Fatal("completed recovery still unavailable", err)
	}
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	if _, err = writer.Exec(ctx, `SET LOCAL lock_timeout='50ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = writer.Exec(ctx, `ALTER POLICY gregale_pipeline_read ON public.orders USING(false)`)
	var lockError *pgconn.PgError
	if !errors.As(err, &lockError) || lockError.Code != "55P03" {
		t.Fatal("policy changed after admission while the read transaction was open", err)
	}
}

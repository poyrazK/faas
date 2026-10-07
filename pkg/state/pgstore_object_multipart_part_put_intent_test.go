//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func partPutMigrationParts(t *testing.T) []string {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261007125239082_object_multipart_part_put_intent.sql")
	if err != nil {
		t.Fatal(err)
	}
	return strings.SplitN(string(raw), "-- +goose Down", 2)
}
func TestPgObjectMultipartPartPutIntentRestartGuardsDowngrade(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, u := activeTrackedUpload(t, st, "put-restart")
	if err := st.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "original", 1, 3, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	i := state.ObjectMultipartPartPutIntent{Schema: 1, DestinationKey: u.Key, ProviderUploadID: u.ProviderUploadID, ExpectedSize: 3}
	r, err := st.DispatchObjectMultipartPartPutMutation(ctx, b, u.ID, 1, "original", i)
	if err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	if got, err := restarted.ReadObjectMultipartPartPutIntent(ctx, r); err != nil || got.BodySHA256 != "" || got.ExpectedSHA256 != "" {
		t.Fatal("unsigned attempt adopted identity", got, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE object_multipart_part_writers SET body_sha256=$2 WHERE id=$1`, r.ID, partBodySHA("abc")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.ReadObjectMultipartPartPutIntent(ctx, r); err != nil || got.BodySHA256 != "" {
		t.Fatal("rolled-back observation persisted", got, err)
	}
	if err = restarted.ObserveObjectMultipartPartBody(ctx, r, partBodySHA("abc")); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE object_multipart_part_writers SET put_intent=NULL WHERE id=$1`,
		`UPDATE object_multipart_part_writers SET put_intent=jsonb_set(put_intent,'{expected_size}','4') WHERE id=$1`,
		`UPDATE object_multipart_part_writers SET body_sha256='' WHERE id=$1`,
		`UPDATE object_multipart_part_writers SET body_sha256=repeat('0',64) WHERE id=$1`,
		`UPDATE object_multipart_part_writers SET body_sha256=repeat('0',64),settled=true WHERE id=$1`,
	} {
		_, err = pool.Exec(ctx, q, r.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatal("raw PUT evidence rewrite", q, err)
		}
	}
	parts := partPutMigrationParts(t)
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("busy downgrade discarded PUT identity")
	}
	if err = restarted.FinishObjectMultipartPartMutation(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, err := state.NewPgStore(pool).ReadObjectMultipartPartPutIntent(ctx, r); err != nil || got.BodySHA256 != partBodySHA("abc") {
		t.Fatal("restart lost body identity", got, err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var adopted bool
	if err = tx.QueryRow(ctx, `SELECT put_intent IS NOT NULL OR body_sha256<>'' FROM object_multipart_part_writers WHERE id=$1`, r.ID).Scan(&adopted); err != nil || adopted {
		t.Fatal("migration invented legacy PUT content", adopted, err)
	}
}

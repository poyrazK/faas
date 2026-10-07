//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgObjectMultipartPartCopyIntentRollbackRestartGuards(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, u := activeTrackedUpload(t, st, "copy-restart")
	if err := st.BeginObjectMultipartPart(ctx, b.AccountID, b.ID, u.ID, "original", 1, 30, 100, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	i := partCopyIntent(b, u)
	data, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE object_multipart_part_writers SET dispatched=true,copy_intent=$2 WHERE upload_id=$1`, u.ID, data); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := st.DispatchObjectMultipartPartCopyMutation(ctx, b, u.ID, 1, "original", i)
	if err != nil {
		t.Fatal("rollback lost claim", err)
	}
	restarted := state.NewPgStore(pool)
	got, err := restarted.ReadObjectMultipartPartCopyIntent(ctx, r)
	if err != nil || !reflect.DeepEqual(got, i) {
		t.Fatal("restart lost intent", got, err)
	}
	for _, q := range []string{
		`UPDATE object_multipart_part_writers SET copy_intent=NULL WHERE upload_id=$1`,
		`UPDATE object_multipart_part_writers SET copy_intent=jsonb_set(copy_intent,'{source_version_id}','"replacement"') WHERE upload_id=$1`,
		`UPDATE object_multipart_part_writers SET copy_intent=jsonb_set(copy_intent,'{expected_size}','29'),settled=true WHERE upload_id=$1`,
	} {
		_, err = pool.Exec(ctx, q, u.ID)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatal("raw intent rewrite", q, err)
		}
	}
	raw, err := migrations.FS.ReadFile("20261007100924000_object_multipart_part_copy_intent.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("busy downgrade discarded intent")
	}
	if err = restarted.FinishObjectMultipartPartMutation(ctx, r); err != nil {
		t.Fatal(err)
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
	// Reinstallation cannot invent historical copy identity.
	var present bool
	if err = tx.QueryRow(ctx, `SELECT copy_intent IS NOT NULL FROM object_multipart_part_writers WHERE id=$1`, r.ID).Scan(&present); err != nil || present {
		t.Fatal("migration adopted historical writer", present, err)
	}
}

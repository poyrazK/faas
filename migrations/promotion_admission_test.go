//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPromotionAdmissionMigrationRoundTripAndFencing(t *testing.T) {
	pool := openMigrated(t)
	ctx := t.Context()
	files, err := filepath.Glob("*_promotion_admission.sql")
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err = pool.Exec(ctx, down); err != nil {
		t.Fatal("empty downgrade", err)
	}
	if _, err = pool.Exec(ctx, up); err != nil {
		t.Fatal("reinstall", err)
	}
	account := seedAccount(t, ctx, pool)
	// Projects bind to the account's personal org (ADR-202610171908).
	seedPersonalOrg(t, ctx, pool, account)
	project, id := uuid.NewString(), uuid.NewString()
	if _, err = pool.Exec(ctx, "INSERT INTO projects(id,account_id,slug) VALUES($1,$2,'admission-roundtrip')", project, account); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "INSERT INTO project_environment_promotions(id,account_id,project_id,project_slug,from_environment,to_environment,promotion_hash,idempotency_key) VALUES($1,$2,$3,'admission-roundtrip','staging','production',$4,'roundtrip')", id, account, project, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	deadline := now.Add(15 * time.Minute)
	payload := map[string]any{"phase": "queued", "generation": 1, "policy": map[string]any{}, "started_at": now, "deadline": deadline, "admission": map[string]any{"queued_at": now, "wait_deadline": deadline, "reason": "fleet_capacity"}}
	b, _ := json.Marshal(payload)
	if _, err = pool.Exec(ctx, "INSERT INTO project_environment_promotion_health(promotion_id,generation,phase,payload) VALUES($1,1,'queued',$2)", id, b); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE project_environment_promotion_health SET payload=payload-'admission' WHERE promotion_id=$1",
		"UPDATE project_environment_promotion_health SET phase='preflight',generation=2,payload=jsonb_set(jsonb_set(payload,'{phase}', '\"preflight\"'),'{generation}','2') WHERE promotion_id=$1",
		"UPDATE project_environment_promotion_health SET payload=jsonb_set(payload,'{admission,wait_deadline}',to_jsonb((clock_timestamp()+interval '1 hour')::text)) WHERE promotion_id=$1",
	} {
		if _, err = pool.Exec(ctx, query, id); err == nil {
			t.Fatal("unvalidated writer crossed queue fence", query)
		}
	}
	if _, err = pool.Exec(ctx, down); err == nil {
		t.Fatal("downgrade lost admission history")
	}
	if _, err = pool.Exec(ctx, "UPDATE project_environment_promotion_health SET phase='failed',generation=2,payload=jsonb_set(jsonb_set(payload,'{phase}','\"failed\"'),'{generation}','2') WHERE promotion_id=$1", id); err != nil {
		t.Fatal("terminal transition", err)
	}
	if _, err = pool.Exec(ctx, down); err == nil {
		t.Fatal("terminal queue receipt allowed downgrade")
	}
}

//go:build !no_pg

package state

// adr: 430

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPgBaseProducerLifecycle(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	baseProducerLifecycle(t, NewPgStore(pool))
}
func TestPgBaseProducerRefusals(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	baseProducerRefusals(t, NewPgStore(pool))
}
func TestPgBaseProducerFencesAndGuards(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := NewPgStore(pool)
	in := baseProducerFixture(t, "base/fenced.ext4", "content")
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('gregale.base-producer.' || $1,0))`, in.Artifact.StorageKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.PublishBaseImageProducer(ctx, in); !errors.Is(err, ErrApplicationStandardReviewBusy) {
		t.Fatalf("busy fence waited or bypassed: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err := s.PublishBaseImageProducer(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`UPDATE base_image_producers SET input_hash=repeat('0',64) WHERE id=$1`,
		`DELETE FROM base_image_producers WHERE id=$1`,
		`INSERT INTO base_image_producers SELECT $2,storage_key,parent_producer_id,input_snapshot,input_hash,published_at FROM base_image_producers WHERE id=$1`,
		`UPDATE base_image_producer_current SET producer_id=$1 WHERE storage_key='base/fenced.ext4'`,
		`DELETE FROM base_image_producer_current WHERE producer_id=$1`,
	}
	for _, stmt := range statements {
		var err error
		if len(stmt) > 6 && stmt[:6] == "INSERT" {
			_, err = pool.Exec(t.Context(), stmt, stored.ID, uuid.NewString())
		} else {
			_, err = pool.Exec(t.Context(), stmt, stored.ID)
		}
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "base_image_producer_immutable" {
			t.Fatalf("immutable guard bypassed: %v", err)
		}
	}
}

func TestPgRegistryRootfsBaseBinding(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	registryRootfsBaseBinding(t, NewPgStore(pool))
}

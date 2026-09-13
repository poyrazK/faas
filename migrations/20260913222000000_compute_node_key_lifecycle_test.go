//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestComputeNodeKeyLifecycleBoundsTrustedRows(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	nodeID := insertComputeNode(ctx, t, pool, "key-life-"+uuid.NewString()[:8])

	currentID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	overlapID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := pool.Exec(ctx, `
		insert into compute_node_keys
		       (compute_node_id, key_id, public_key_pem, key_state)
		values ($1, $2, $3, 'current')
	`, nodeID, currentID, canonicalKeyPEM); err != nil {
		t.Fatalf("insert current: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into compute_node_keys
		       (compute_node_id, key_id, public_key_pem, key_state, valid_until)
		values ($1, $2, $3, 'overlap', $4)
	`, nodeID, overlapID, canonicalKeyPEM, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("insert overlap: %v", err)
	}

	_, err := pool.Exec(ctx, `
		insert into compute_node_keys
		       (compute_node_id, key_id, public_key_pem, key_state)
		values ($1, $2, $3, 'current')
	`, nodeID, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", canonicalKeyPEM)
	if err == nil {
		t.Fatal("second current key inserted; want unique-index violation")
	}

	_, err = pool.Exec(ctx, `
		insert into compute_node_keys
		       (compute_node_id, key_id, public_key_pem, key_state, valid_until)
		values ($1, $2, $3, 'overlap', null)
	`, nodeID, "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", canonicalKeyPEM)
	if err == nil {
		t.Fatal("overlap key without expiry inserted; want check violation")
	}
}

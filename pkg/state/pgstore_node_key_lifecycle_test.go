//go:build !no_pg

package state_test

import (
	"testing"
)

const nodeKeyLifecyclePEM = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEfakedFAKEfakedFAKEfakedFAKE
fakedFAKEfakedFAKEfakedFAKEfakedFAKEfakedFAKEfakedFAKEfaked==
-----END PUBLIC KEY-----
`

func TestPgStoreNodeKeyRotationBoundsAndExpiresTrust(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, store)
	keys := []string{
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333333333333333333333333333",
	}
	for _, keyID := range keys {
		if err := store.UpsertNodeKey(ctx, nodeID, keyID, nodeKeyLifecyclePEM); err != nil {
			t.Fatalf("UpsertNodeKey(%s): %v", keyID, err)
		}
	}

	rows, err := pool.Query(ctx, `
		select key_id, key_state, valid_until is not null
		  from compute_node_keys
		 where compute_node_id = $1
		 order by key_state, key_id
	`, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type keyRow struct {
		id         string
		state      string
		hasExpires bool
	}
	var got []keyRow
	for rows.Next() {
		var row keyRow
		if err := rows.Scan(&row.id, &row.state, &row.hasExpires); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("trusted rows = %+v, want current plus one overlap", got)
	}
	states := map[string]keyRow{}
	for _, row := range got {
		states[row.state] = row
	}
	if row := states["current"]; row.id != keys[2] || row.hasExpires {
		t.Errorf("current row = %+v, want third key without expiry", row)
	}
	if row := states["overlap"]; row.id != keys[1] || !row.hasExpires {
		t.Errorf("overlap row = %+v, want second key with expiry", row)
	}
}

func TestPgStoreNodeKeyCurrentReinsertIsNoOp(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, store)
	keyID := "4444444444444444444444444444444444444444444444444444444444444444"
	if err := store.UpsertNodeKey(ctx, nodeID, keyID, nodeKeyLifecyclePEM); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodeKey(ctx, nodeID, keyID, nodeKeyLifecyclePEM+"ignored"); err != nil {
		t.Fatalf("idempotent reinsert: %v", err)
	}
	var pem string
	if err := pool.QueryRow(ctx, `
		select public_key_pem from compute_node_keys
		 where compute_node_id = $1 and key_id = $2
	`, nodeID, keyID).Scan(&pem); err != nil {
		t.Fatal(err)
	}
	if pem != nodeKeyLifecyclePEM {
		t.Fatal("idempotent reinsert changed the stored public key")
	}
}

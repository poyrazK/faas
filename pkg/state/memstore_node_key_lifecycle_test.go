package state

import (
	"context"
	"testing"
)

func TestMemStoreNodeKeyRotationKeepsOnlyCurrentAndOneOverlap(t *testing.T) {
	t.Parallel()
	store := NewMemStore()
	ctx := context.Background()
	const nodeID = "node-1"

	for _, key := range []string{"key-1", "key-2", "key-3"} {
		if err := store.UpsertNodeKey(ctx, nodeID, key, "pem-"+key); err != nil {
			t.Fatalf("UpsertNodeKey(%s): %v", key, err)
		}
	}
	if _, ok := store.LookupNodeKey(ctx, nodeID, "key-1"); ok {
		t.Fatal("oldest key remains trusted after a second rotation")
	}
	if _, ok := store.LookupNodeKey(ctx, nodeID, "key-2"); !ok {
		t.Fatal("rotation-overlap key is not trusted")
	}
	if _, ok := store.LookupNodeKey(ctx, nodeID, "key-3"); !ok {
		t.Fatal("current key is not trusted")
	}
}

func TestMemStoreNodeKeyRotationIsScopedPerNode(t *testing.T) {
	t.Parallel()
	store := NewMemStore()
	ctx := context.Background()
	if err := store.UpsertNodeKey(ctx, "node-1", "key-1", "pem-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodeKey(ctx, "node-2", "key-2", "pem-2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.LookupNodeKey(ctx, "node-1", "key-2"); ok {
		t.Fatal("node-2 key resolved for node-1")
	}
	if _, ok := store.LookupNodeKey(ctx, "node-2", "key-1"); ok {
		t.Fatal("node-1 key resolved for node-2")
	}
}

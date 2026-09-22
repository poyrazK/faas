// adr: 206
package state

import (
	"context"
	"testing"
)

func TestMemStoreServiceCallerKeys(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()

	if keys, err := store.ListServiceCallerKeys(ctx); err != nil || len(keys) != 0 {
		t.Fatalf("empty store = %v, %v; want no keys and no error", keys, err)
	}

	pem := "-----BEGIN PUBLIC KEY-----\nAAA\n-----END PUBLIC KEY-----\n"
	for _, node := range []string{"node-b", "node-a"} {
		if err := store.PublishServiceCallerKey(ctx, ServiceCallerKey{
			NodeID: node, KeyID: "kid-" + node, PublicKeyPEM: pem,
		}); err != nil {
			t.Fatalf("publish %s: %v", node, err)
		}
	}

	keys, err := store.ListServiceCallerKeys(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Ordering is part of the contract: a verifier refresh must be
	// deterministic so a diff between two refreshes is readable.
	if len(keys) != 2 || keys[0].NodeID != "node-a" || keys[1].NodeID != "node-b" {
		t.Fatalf("keys = %+v, want node-a then node-b", keys)
	}

	// Rotation replaces the node's row rather than accumulating history.
	if err := store.PublishServiceCallerKey(ctx, ServiceCallerKey{
		NodeID: "node-a", KeyID: "kid-rotated", PublicKeyPEM: pem,
	}); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	keys, err = store.ListServiceCallerKeys(ctx)
	if err != nil {
		t.Fatalf("list after rotate: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("after rotation there are %d keys, want 2 (one per node)", len(keys))
	}
	if keys[0].KeyID != "kid-rotated" {
		t.Errorf("node-a kid = %q, want kid-rotated", keys[0].KeyID)
	}
}

func TestMemStoreServiceCallerKeyValidation(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	pem := "-----BEGIN PUBLIC KEY-----\nAAA\n-----END PUBLIC KEY-----\n"

	tests := []struct {
		name string
		key  ServiceCallerKey
	}{
		{"no node", ServiceCallerKey{KeyID: "k", PublicKeyPEM: pem}},
		{"no kid", ServiceCallerKey{NodeID: "n", PublicKeyPEM: pem}},
		{"no pem", ServiceCallerKey{NodeID: "n", KeyID: "k"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.PublishServiceCallerKey(ctx, tc.key); err == nil {
				t.Error("publish accepted an incomplete key")
			}
		})
	}
}

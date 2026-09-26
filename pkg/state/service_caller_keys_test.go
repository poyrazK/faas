// adr: 206
package state

import (
	"context"
	"testing"
	"time"
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

	// Rotation retains the previous public key for the assertion maximum TTL
	// so requests minted just before rotation remain verifiable.
	if err := store.PublishServiceCallerKey(ctx, ServiceCallerKey{
		NodeID: "node-a", KeyID: "kid-rotated", PublicKeyPEM: pem,
	}); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	keys, err = store.ListServiceCallerKeys(ctx)
	if err != nil {
		t.Fatalf("list after rotate: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("after rotation there are %d keys, want current + previous node-a + node-b", len(keys))
	}
	seen := map[string]bool{}
	for _, key := range keys {
		seen[key.KeyID] = true
	}
	for _, keyID := range []string{"kid-node-a", "kid-node-b", "kid-rotated"} {
		if !seen[keyID] {
			t.Errorf("rotated key set is missing %q: %+v", keyID, keys)
		}
	}

	store.mu.Lock()
	history := store.serviceCallerKeyHistory["kid-node-a"]
	history.retireAt = time.Now().Add(-time.Second)
	store.serviceCallerKeyHistory["kid-node-a"] = history
	store.mu.Unlock()
	keys, err = store.ListServiceCallerKeys(ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("expired history = %+v, %v; want only current keys", keys, err)
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

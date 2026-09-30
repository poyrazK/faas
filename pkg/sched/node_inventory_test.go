// adr: 381 — destructive inventory evidence is signed separately from capacity.
package sched

import (
	"crypto/ecdsa"
	"errors"
	"testing"
	"time"
)

func TestNodeInventorySignatureBindsIdentityTimeCompletenessAndIDs(t *testing.T) {
	key, keyID := generateTestP256(t)
	inventory := NodeInstanceInventory{NodeID: "node-a", NodeKeyID: keyID, SampledAt: time.Now(), Complete: true, InstanceIDs: []string{"vm-a", "vm-b"}}
	sig, err := SignNodeInventory(key, inventory)
	if err != nil {
		t.Fatal(err)
	}
	inventory.Signature = sig
	keys := &stubKeyLookup{owner: "node-a", keys: map[string]*ecdsa.PublicKey{keyID: &key.PublicKey}}
	if err := VerifyNodeInventory(inventory, keys); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*NodeInstanceInventory)
	}{
		{"node", func(r *NodeInstanceInventory) { r.NodeID = "node-b" }},
		{"time", func(r *NodeInstanceInventory) { r.SampledAt = r.SampledAt.Add(time.Second) }},
		{"complete", func(r *NodeInstanceInventory) { r.Complete = false }},
		{"ids", func(r *NodeInstanceInventory) { r.InstanceIDs = []string{"vm-b"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inventory
			tc.mutate(&r)
			if VerifyNodeInventory(r, keys) == nil {
				t.Fatal("tampered inventory accepted")
			}
		})
	}
	inventory.InstanceIDs = []string{"vm-b", "vm-a"}
	if err := VerifyNodeInventory(inventory, keys); err != nil {
		t.Fatalf("inventory order changed signature: %v", err)
	}
	inventory.Signature = nil
	if !errors.Is(VerifyNodeInventory(inventory, keys), ErrEmptySignature) {
		t.Fatal("unsigned inventory accepted")
	}
}

// adr: 419 — destructive inventory evidence is signed separately from capacity.
package sched_test

import (
	"crypto/ecdsa"
	"errors"
	"github.com/onebox-faas/faas/pkg/sched"
	"testing"
	"time"
)

func TestNodeInventorySignatureBindsIdentityTimeCompletenessAndIDs(t *testing.T) {
	key, keyID := generateTestP256(t)
	inventory := sched.NodeInstanceInventory{NodeID: "node-a", NodeKeyID: keyID, SampledAt: time.Now(), Complete: true, InstanceIDs: []string{"vm-a", "vm-b"}}
	sig, err := sched.SignNodeInventory(key, inventory)
	if err != nil {
		t.Fatal(err)
	}
	inventory.Signature = sig
	keys := &stubKeyLookup{owner: "node-a", keys: map[string]*ecdsa.PublicKey{keyID: &key.PublicKey}}
	if err := sched.VerifyNodeInventory(inventory, keys); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*sched.NodeInstanceInventory)
	}{
		{"node", func(r *sched.NodeInstanceInventory) { r.NodeID = "node-b" }},
		{"time", func(r *sched.NodeInstanceInventory) { r.SampledAt = r.SampledAt.Add(time.Second) }},
		{"complete", func(r *sched.NodeInstanceInventory) { r.Complete = false }},
		{"ids", func(r *sched.NodeInstanceInventory) { r.InstanceIDs = []string{"vm-b"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inventory
			tc.mutate(&r)
			if sched.VerifyNodeInventory(r, keys) == nil {
				t.Fatal("tampered inventory accepted")
			}
		})
	}
	inventory.InstanceIDs = []string{"vm-b", "vm-a"}
	if err := sched.VerifyNodeInventory(inventory, keys); err != nil {
		t.Fatalf("inventory order changed signature: %v", err)
	}
	inventory.Signature = nil
	if !errors.Is(sched.VerifyNodeInventory(inventory, keys), sched.ErrEmptySignature) {
		t.Fatal("unsigned inventory accepted")
	}
}

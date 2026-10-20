// adr: 419 — destructive inventory evidence is signed separately from capacity.
package nodereport_test

import (
	"crypto/ecdsa"
	"errors"
	"github.com/onebox-faas/faas/pkg/nodereport"
	"testing"
	"time"
)

func TestNodeInventorySignatureBindsIdentityTimeCompletenessAndIDs(t *testing.T) {
	key, keyID := generateTestP256(t)
	inventory := nodereport.Inventory{NodeID: "node-a", NodeKeyID: keyID, SampledAt: time.Now(), Complete: true, InstanceIDs: []string{"vm-a", "vm-b"}}
	sig, err := nodereport.SignInventory(key, inventory)
	if err != nil {
		t.Fatal(err)
	}
	inventory.Signature = sig
	keys := &stubKeyLookup{owner: "node-a", keys: map[string]*ecdsa.PublicKey{keyID: &key.PublicKey}}
	if err := nodereport.VerifyInventory(inventory, keys); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*nodereport.Inventory)
	}{
		{"node", func(r *nodereport.Inventory) { r.NodeID = "node-b" }},
		{"time", func(r *nodereport.Inventory) { r.SampledAt = r.SampledAt.Add(time.Second) }},
		{"complete", func(r *nodereport.Inventory) { r.Complete = false }},
		{"ids", func(r *nodereport.Inventory) { r.InstanceIDs = []string{"vm-b"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inventory
			tc.mutate(&r)
			if nodereport.VerifyInventory(r, keys) == nil {
				t.Fatal("tampered inventory accepted")
			}
		})
	}
	inventory.InstanceIDs = []string{"vm-b", "vm-a"}
	if err := nodereport.VerifyInventory(inventory, keys); err != nil {
		t.Fatalf("inventory order changed signature: %v", err)
	}
	inventory.Signature = nil
	if !errors.Is(nodereport.VerifyInventory(inventory, keys), nodereport.ErrEmptySignature) {
		t.Fatal("unsigned inventory accepted")
	}
}

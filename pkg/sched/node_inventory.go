package sched

import (
	"crypto/ecdsa"

	"github.com/onebox-faas/faas/pkg/nodereport"
)

// NodeInstanceInventory is an explicit, authenticated vmmd process inventory
// (nodereport.Inventory). Metrics are never an inventory: a failed cgroup
// read must not fail live VMs.
type NodeInstanceInventory = nodereport.Inventory

// SignNodeInventory signs an inventory with the node key
// (nodereport.SignInventory).
func SignNodeInventory(key *ecdsa.PrivateKey, r NodeInstanceInventory) ([]byte, error) {
	return nodereport.SignInventory(key, r)
}

// VerifyNodeInventory checks an inventory's signature
// (nodereport.VerifyInventory).
func VerifyNodeInventory(r NodeInstanceInventory, keys nodeKeyLookup) error {
	return nodereport.VerifyInventory(r, keys)
}

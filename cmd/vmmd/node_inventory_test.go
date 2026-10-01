// adr: 419 — inventory survives a failed optional metrics collection.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
)

type inventoryCounts struct {
	ids      []string
	complete bool
}

func (c inventoryCounts) LiveCount() int                      { return len(c.ids) }
func (c inventoryCounts) LeasedCount() int                    { return len(c.ids) }
func (c inventoryCounts) InstanceInventory() ([]string, bool) { return c.ids, c.complete }

type inventoryPublicKey struct {
	key   *ecdsa.PublicKey
	keyID string
}

func (k inventoryPublicKey) PublicKeyForNode(nodeID, keyID string) (*ecdsa.PublicKey, bool) {
	return k.key, nodeID == "node-a" && keyID == k.keyID
}

func TestCapacityPublishesSignedInventoryWhenMetricsFail(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := sched.KeyIDForPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	signer := &signedBufconnStreamerNoDial{key: key, keyID: keyID}
	metrics := func(context.Context) (*vmmdpb.StatsResponse, error) { return nil, errors.New("metrics unavailable") }
	for _, tc := range []inventoryCounts{{complete: true}, {ids: []string{"vm-a"}, complete: true}, {complete: false}} {
		got := buildCapacityReport(context.Background(), signer, tc, "node-a", ComputeNodeConfig{MemMB: 1024}, noResident, silentLogger(), metrics)
		if got.InstanceInventory == nil || got.InstanceInventory.Complete != tc.complete || len(got.InstanceInventory.InstanceIds) != len(tc.ids) {
			t.Fatalf("inventory=%v", got.InstanceInventory)
		}
		want := sched.NodeInstanceInventory{NodeID: "node-a", NodeKeyID: keyID, SampledAt: time.UnixMilli(got.SampledAtUnixMs), Complete: tc.complete, InstanceIDs: tc.ids}
		want.Signature = got.InstanceInventory.NodeSignature
		if err := sched.VerifyNodeInventory(want, inventoryPublicKey{key: &key.PublicKey, keyID: keyID}); err != nil {
			t.Fatalf("inventory signature rejected: %v", err)
		}
	}
}

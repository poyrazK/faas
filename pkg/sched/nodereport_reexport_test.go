package sched_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/leaser"
	"github.com/onebox-faas/faas/pkg/nodereport"
	"github.com/onebox-faas/faas/pkg/sched"
)

type reexportKeys struct{ pub *ecdsa.PublicKey }

func (k reexportKeys) PublicKeyForNode(nodeID, keyID string) (*ecdsa.PublicKey, bool) {
	return k.pub, nodeID == "node-a"
}

// sched's node report and lease names are the leaf packages' values, so
// schedd (which verifies through sched) and vmmd (which signs through
// pkg/nodereport and implements pkg/leaser) agree on payloads and errors.
func TestNodeReportAndLeaseReexports(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	report := sched.CapacityReport{NodeID: "node-a", SampledAt: time.UnixMilli(1_700_000_000_000), LiveCount: 3}
	sig, err := nodereport.SignCapacity(key, report)
	if err != nil {
		t.Fatal(err)
	}
	if err := sched.VerifyNodeSignature(report, sig, reexportKeys{&key.PublicKey}); err != nil {
		t.Fatalf("sched verifies a nodereport signature: %v", err)
	}
	report.NodeID = "node-b"
	if err := sched.VerifyNodeSignature(report, sig, reexportKeys{&key.PublicKey}); !errors.Is(err, nodereport.ErrUnknownNodeKey) {
		t.Fatalf("got %v, want nodereport.ErrUnknownNodeKey", err)
	}
	inventory := sched.NodeInstanceInventory{NodeID: "node-a", SampledAt: time.UnixMilli(1), Complete: true}
	if inventory.Signature, err = nodereport.SignInventory(key, inventory); err != nil {
		t.Fatal(err)
	}
	if err := sched.VerifyNodeInventory(inventory, reexportKeys{&key.PublicKey}); err != nil {
		t.Fatalf("sched verifies a nodereport inventory: %v", err)
	}
	for _, pair := range [][2]error{
		{sched.ErrSignatureMismatch, nodereport.ErrSignatureMismatch},
		{sched.ErrUnknownNodeKey, nodereport.ErrUnknownNodeKey},
		{sched.ErrEmptySignature, nodereport.ErrEmptySignature},
		{sched.ErrLeaseNotFound, leaser.ErrNotFound},
		{sched.ErrLeaseExpired, leaser.ErrExpired},
		{sched.ErrLeaseHeldByOther, leaser.ErrHeldByOther},
		{sched.ErrInvalidLeasePolicy, leaser.ErrInvalidPolicy},
	} {
		if !errors.Is(pair[0], pair[1]) {
			t.Errorf("%v is not the leaf package's error", pair[0])
		}
	}
	var _ leaser.Leaser[any] = sched.Leaser[any](nil)
	if err := (sched.LeasePolicy{}).Validate(); !errors.Is(err, leaser.ErrInvalidPolicy) {
		t.Fatalf("zero policy: got %v, want leaser.ErrInvalidPolicy", err)
	}
}

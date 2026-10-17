package object

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func promoteOwnedSwitchover(t *testing.T, follower, endpoint string) {
	t.Helper()
	source, err := objectnode.NewHTTPReplicationSource(endpoint, "us-east-1", "node-key", "node-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.FenceReplicaPrimary(t.Context(), follower, source); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		progress, err := objectnode.CatchUpReplica(t.Context(), follower, source, objectnode.CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
		if err != nil {
			t.Fatal(err)
		}
		if progress.CaughtUp {
			if _, err = objectnode.PromoteReplica(t.Context(), follower, source); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("handoff did not reach the sealed checkpoint")
}

func checkOwnedSwitchoverCapacity(t *testing.T, st *state.MemStore, registry *objectstorage.Registry, bucket state.ObjectBucket, pending, current bool) {
	t.Helper()
	if err := st.ClaimObjectInventory(t.Context(), bucket.ID, "after-switchover"); err != nil {
		t.Fatal(err)
	}
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	if err := svc.ScanObjectInventory(t.Context(), st, bucket, "after-switchover"); err != nil {
		t.Fatal(err)
	}
	usage, err := st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
	wantBytes, wantKeys := int64(0), int64(0)
	if current {
		wantBytes, wantKeys = 5, 1
	}
	if err != nil || len(usage.Buckets) != 1 || usage.Buckets[0].ObservedBytes != wantBytes || usage.Buckets[0].ObservedKeys != wantKeys {
		t.Fatal("handoff inventory changed logical capacity", usage, err)
	}
	if pending && usage.Buckets[0].GrantedBytes < 8 {
		t.Fatal("inventory released an uncertain pre-handoff write hold", usage)
	}
}

func prepareOwnedSwitchover(t *testing.T, node *objectnode.Store, directory string) (*objectnode.Store, string) {
	t.Helper()
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := objectnode.EnableReplication(t.Context(), directory); err != nil {
		t.Fatal(err)
	}
	backup, follower := filepath.Join(t.TempDir(), "seed"), filepath.Join(t.TempDir(), "follower")
	if _, err := objectnode.Backup(t.Context(), directory, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := objectnode.BootstrapReplica(t.Context(), backup, follower); err != nil {
		t.Fatal(err)
	}
	primary, err := objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	return primary, follower
}

func configureOwnedReplica(t *testing.T, node *objectnode.Store, directory, follower string) *objectnode.Store {
	t.Helper()
	local, err := objectnode.ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = objectnode.ConfigureDurability(t.Context(), directory, objectnode.DurabilityReplica, local.NodeID); err != nil {
		t.Fatal(err)
	}
	primary, err := objectnode.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	return primary
}

func confirmOwnedReplica(t *testing.T, follower, endpoint string) {
	t.Helper()
	source, err := objectnode.NewHTTPReplicationSource(endpoint, "us-east-1", "node-key", "node-secret")
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		progress, err := objectnode.CatchUpReplica(t.Context(), follower, source, objectnode.CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
		if err != nil {
			t.Fatal(err)
		}
		if progress.CaughtUp && progress.ConfirmedSequence != nil {
			return
		}
	}
	t.Fatal("replica did not confirm the complete gateway history")
}

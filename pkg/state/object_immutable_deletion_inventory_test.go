package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 548
func TestImmutableDeletionInventoryMem(t *testing.T) {
	immutableDeletionInventory(t, state.NewMemStore(), nil)
}
func TestImmutableDeletionInventoryPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	immutableDeletionInventory(t, st, func() accountingStore { return state.NewPgStore(pool) })
}
func immutableDeletionInventory(t *testing.T, st accountingStore, restart func() accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(ctx, b.AccountID, b.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "private-version"}})
	if err != nil {
		t.Fatal(err)
	}
	input := state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "key", Selector: refs[0].ID}, AccountID: b.AccountID, AppID: b.AppID, Token: "delete"}
	capacity := st.(state.ObjectCapacityStore)
	job, err := capacity.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = capacity.ClaimObjectCapacityReconciliation(ctx, job.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, job.ID, job.Token, "private-cursor", []state.ObjectVersionInventoryRecord{versionRecord("first", 10)})
	if err != nil {
		t.Fatal(err)
	}
	if restart != nil {
		st = restart()
	}
	d := st.(state.ObjectDeletionStore)
	if _, _, err = d.BeginObjectDeletion(ctx, input, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("delete invalidated waiting scan cursor", err)
	}
	capacity = st.(state.ObjectCapacityStore)
	job, err = capacity.ClaimObjectCapacityReconciliation(ctx, job.ID, "second")
	if err != nil || job.InventoryCursor != "private-cursor" {
		t.Fatal(job, err)
	}
	if _, _, err = d.BeginObjectDeletion(ctx, input, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("delete passed scanning fence", err)
	}
	job, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, job.ID, job.Token, "", []state.ObjectVersionInventoryRecord{versionRecord("second", 20)})
	if err != nil || job.AfterBytes != 30 || job.AfterKeys != 2 {
		t.Fatal(job, err)
	}
	before, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Immutable cleanup needs neither a versioning cutover nor spare budgets.
	j, created, err := d.BeginObjectDeletion(ctx, input, api.ObjectStoragePolicy{})
	if err != nil || !created || j.TargetProviderVersionID != "private-version" || j.ReservedBytes != 0 || j.ProviderStatus != "" {
		t.Fatal(j, created, err)
	}
	if _, err = capacity.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("inventory passed admitted delete", err)
	}
	if err = capacity.BeginObjectWrite(ctx, b.AccountID, b.ID, uuid.NewString(), "new", 1, accountingPolicy()); !errors.Is(err, state.ErrConflict) {
		t.Fatal("write passed delete", err)
	}
	j, err = d.DispatchObjectDeletion(ctx, j.ID, j.Token, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	forged := j
	forged.State = "completed"
	forged.ProviderVersionID = "different-private-version"
	forged.VersionID = j.Selector
	if _, err = d.FinishObjectDeletion(ctx, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatal("foreign completion settled delete", err)
	}
	j.State = "completed"
	j.ProviderVersionID = j.TargetProviderVersionID
	j.VersionID = j.Selector
	j.DeleteMarker = true
	j, err = d.FinishObjectDeletion(ctx, j)
	if err != nil || j.VersionID != refs[0].ID {
		t.Fatal(j, err)
	}
	input.Token = "replay"
	replay, created, err := d.BeginObjectDeletion(ctx, input, api.ObjectStoragePolicy{})
	if err != nil || created || replay.State != "completed" || !replay.DeleteMarker {
		t.Fatal(replay, created, err)
	}
	raw, _ := json.Marshal(replay)
	if strings.Contains(string(raw), "private-version") {
		t.Fatal("private selector exposed", string(raw))
	}
	after, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || after.Buckets[0].BaselineBytes != before.Buckets[0].BaselineBytes || after.Buckets[0].GrantedBytes != before.Buckets[0].GrantedBytes {
		t.Fatal("delete refunded capacity", after, err)
	}
	if _, err = capacity.RequestObjectCapacityReconciliation(ctx, b.AccountID, b.AppID, b.ID); err != nil {
		t.Fatal("completed delete retained inventory fence", err)
	}
}

func TestImmutableDeletionInventoryAdmissionRaceMem(t *testing.T) {
	immutableDeletionInventoryRace(t, state.NewMemStore())
}
func TestImmutableDeletionInventoryAdmissionRacePG(t *testing.T) {
	st, _ := pgStore(t)
	immutableDeletionInventoryRace(t, st)
}
func immutableDeletionInventoryRace(t *testing.T, st accountingStore) {
	b, _ := seedAccounting(t, st)
	refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), b.AccountID, b.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "native"}})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, _, e := st.(state.ObjectDeletionStore).BeginObjectDeletion(t.Context(), state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "key", Selector: refs[0].ID}, AccountID: b.AccountID, AppID: b.AppID, Token: "delete"}, accountingPolicy())
		results <- e
	}()
	go func() {
		<-start
		_, e := st.(state.ObjectCapacityStore).RequestObjectCapacityReconciliation(t.Context(), b.AccountID, b.AppID, b.ID)
		results <- e
	}()
	close(start)
	wins := 0
	for range 2 {
		e := <-results
		if e == nil {
			wins++
		} else if !errors.Is(e, state.ErrConflict) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatal("inventory and deletion admitted together", wins)
	}
}

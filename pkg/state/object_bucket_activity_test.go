// adr: 20261110151628

package state_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectBucketActivity pins ADR-20261110151628 step 3: a source writer
// beginning or ending, a deletion and a listing published beside an
// outstanding writer stamp the bucket's mutated_at; stamps are coarse; and
// active_since moves only when a quiet bucket turns active.
func TestObjectBucketActivityMem(t *testing.T) {
	m := state.NewMemStore()
	objectBucketActivitySuite(t, m, m.AgeObjectBucketActivityForTest)
}

func TestObjectBucketActivityPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	objectBucketActivitySuite(t, st, func(bucket string, d time.Duration) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE object_bucket_activity SET mutated_at = mutated_at - $2::interval, active_since = active_since - $2::interval WHERE bucket_id = $1`, bucket, fmt.Sprintf("%d seconds", int(d/time.Second))); err != nil {
			t.Fatal(err)
		}
	})
}

func objectBucketActivitySuite(t *testing.T, st accountingStore, age func(string, time.Duration)) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	usage := func() state.ObjectBucketUsage {
		t.Helper()
		snapshot, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range snapshot.Buckets {
			if u.Bucket.ID == b.ID {
				return u
			}
		}
		t.Fatal("bucket missing from its account's usage")
		return state.ObjectBucketUsage{}
	}
	recent := func(at time.Time) bool { return time.Since(at).Abs() < time.Minute }
	if u := usage(); !u.MutatedAt.IsZero() || !u.ActiveSince.IsZero() {
		t.Fatal("a bucket never mutated has activity", u.MutatedAt, u.ActiveSince)
	}
	writers := st.(state.ObjectBucketMutationStore)
	first, err := writers.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	stamped := usage()
	if !recent(stamped.MutatedAt) || !stamped.ActiveSince.Equal(stamped.MutatedAt) {
		t.Fatal("a writer's start was not stamped", stamped.MutatedAt, stamped.ActiveSince)
	}
	if err = writers.FinishObjectBucketMutation(ctx, first); err != nil {
		t.Fatal(err)
	}
	if u := usage(); !u.MutatedAt.Equal(stamped.MutatedAt) {
		t.Fatal("a stamp inside the granularity rewrote the row", stamped.MutatedAt, u.MutatedAt)
	}
	// seedAccounting listed the bucket just now, so activity aged past it
	// reads as quiet, and the next writer turns the bucket active.
	age(b.ID, 10*time.Minute)
	outstanding, err := writers.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	if u := usage(); !recent(u.MutatedAt) || !u.ActiveSince.Equal(u.MutatedAt) {
		t.Fatal("a writer on a quiet bucket: mutated_at and active_since move to now", u.MutatedAt, u.ActiveSince)
	}

	// A listing published while a writer is outstanding leaves the bucket
	// active, whenever that writer was stamped.
	age(b.ID, 10*time.Minute)
	token := uuid.NewString()
	if err = st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, token, 0, 0); err != nil {
		t.Fatal(err)
	}
	listed := usage()
	if !recent(listed.MutatedAt) || listed.MutatedAt.Before(listed.ObservedAt) {
		t.Fatal("a listing published beside an outstanding writer left the bucket quiet", listed.ObservedAt, listed.MutatedAt)
	}
	if err = writers.FinishObjectBucketMutation(ctx, outstanding); err != nil {
		t.Fatal(err)
	}

	// Quiet since that listing, the bucket turns active at its next
	// mutation, a deletion.
	age(b.ID, 10*time.Minute)
	if u := usage(); !u.MutatedAt.Before(u.ObservedAt.Add(-api.ObjectInventoryMutationStampGranularity)) {
		t.Fatal("aged activity is not older than the listing", u.ObservedAt, u.MutatedAt)
	}
	deletions := st.(state.ObjectDeletionStore)
	in := deletionCaptureInput(b)
	if _, _, err = deletions.BeginObjectDeletion(ctx, in, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if u := usage(); !recent(u.MutatedAt) || !u.ActiveSince.Equal(u.MutatedAt) {
		t.Fatal("a deletion of a quiet bucket did not turn it active", u.MutatedAt, u.ActiveSince)
	}
}

// TestObjectBucketActivityTriggersPG lists the tables whose writes stamp a
// bucket's activity. A new journal of content mutations, or a stamp dropped
// from one of these, fails here until the ADR's contract is updated.
func TestObjectBucketActivityTriggersPG(t *testing.T) {
	_, pool, ctx := pgStoreWithPool(t)
	rows, err := pool.Query(ctx, `SELECT c.relname, pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		WHERE NOT t.tgisinternal AND t.tgfoid = 'object_bucket_activity_stamp'::regproc ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var table, def string
		if err := rows.Scan(&table, &def); err != nil {
			t.Fatal(err)
		}
		got[table] = def
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"object_bucket_mutations":                 "AFTER INSERT OR DELETE",
		"object_deletions":                        "AFTER INSERT OR UPDATE OF state",
		"object_bucket_versioning":                "AFTER INSERT OR UPDATE",
		"object_bucket_object_lock":               "AFTER INSERT OR UPDATE",
		"object_storage_capacity_reconciliations": "AFTER INSERT OR UPDATE OF state",
	}
	if len(got) != len(want) {
		t.Fatalf("stamped tables %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	for table, events := range want {
		if !strings.Contains(got[table], " "+events+" ON ") || !strings.HasSuffix(got[table], "."+table+" FOR EACH ROW EXECUTE FUNCTION object_bucket_activity_stamp()") {
			t.Errorf("%s: %q, want %s", table, got[table], events)
		}
	}
	var source string
	if err := pool.QueryRow(ctx, `SELECT prosrc FROM pg_proc WHERE proname = 'object_bucket_mark_mutated'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	granularity := fmt.Sprintf("interval '%d seconds'", int(api.ObjectInventoryMutationStampGranularity/time.Second))
	if strings.Count(source, granularity) != 2 {
		t.Fatalf("object_bucket_mark_mutated does not stamp at %s:\n%s", granularity, source)
	}
}

// TestObjectBucketActivityMemParity: the MemStore stamps wherever it changes
// what the Postgres triggers watch. Source writers go through
// putObjectMutationLocked/dropObjectMutationLocked; every write of a
// deletion, capacity job, versioning or Object Lock record is followed by a
// stamp.
func TestObjectBucketActivityMemParity(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	writer := regexp.MustCompile(`m\.objectMutations\[[^\]]+\] = |delete\(m\.objectMutations,`)
	journal := regexp.MustCompile(`^\s*m\.(objectDeletions|objectCapacityJobs|objectBucketVersioning|objectBucketObjectLock)\[[^\]]+\] = `)
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "memstore_object_bucket_activity.go" {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(source), "\n")
		for i, line := range lines {
			if writer.MatchString(line) {
				t.Errorf("%s:%d writes the source-writer map directly; use putObjectMutationLocked or dropObjectMutationLocked", name, i+1)
			}
			if journal.MatchString(line) {
				checked++
				if i+1 >= len(lines) || !strings.Contains(lines[i+1], "m.markObjectBucketMutatedLocked(") {
					t.Errorf("%s:%d changes a watched record without stamping the bucket's activity", name, i+1)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no watched writes: the parity check no longer matches the MemStore")
	}
}

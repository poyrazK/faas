// adr: 712
package durableentity

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

type qualificationStore struct {
	ObjectStore
	prefix  string
	reads   atomic.Int64
	writes  atomic.Int64
	lists   atomic.Int64
	deletes atomic.Int64
	lose    atomic.Bool
}

func (s *qualificationStore) ListEntityObjects(ctx context.Context, prefix, cursor string, limit int32) (CleanupObjects, error) {
	s.lists.Add(1)
	store, ok := s.ObjectStore.(CleanupStore)
	if !ok {
		return CleanupObjects{}, ErrUnsupported
	}
	page, err := store.ListEntityObjects(ctx, s.prefix+prefix, cursor, limit)
	sizes := make(map[string]int64, len(page.Keys))
	for i, key := range page.Keys {
		if !strings.HasPrefix(key, s.prefix) {
			return CleanupObjects{}, ErrCorrupt
		}
		page.Keys[i] = strings.TrimPrefix(key, s.prefix)
		if size, known := page.Sizes[key]; known {
			sizes[page.Keys[i]] = size
		}
	}
	page.Sizes = sizes
	return page, err
}

func (s *qualificationStore) DeleteEntityObject(ctx context.Context, key string) error {
	s.deletes.Add(1)
	store, ok := s.ObjectStore.(CleanupStore)
	if !ok {
		return ErrUnsupported
	}
	return store.DeleteEntityObject(ctx, s.prefix+key)
}

func (s *qualificationStore) Get(ctx context.Context, key string, limit int64) ([]byte, string, error) {
	s.reads.Add(1)
	return s.ObjectStore.Get(ctx, s.prefix+key, limit)
}

func (s *qualificationStore) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	s.writes.Add(1)
	next, err := s.ObjectStore.Put(ctx, s.prefix+key, body, version)
	var published struct {
		Version uint64 `json:"state_version"`
	}
	if err == nil && strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &published) == nil && published.Version == 2 && s.lose.Swap(false) {
		// The native provider has accepted the write. Discard its acknowledgement
		// at the platform boundary to exercise real stored uncertain outcomes.
		return "", errors.New("qualification discarded successful commit response")
	}
	return next, err
}

func (s *qualificationStore) ListEntityPrefixes(ctx context.Context, prefix, cursor string, limit int32) (EntityPrefixPage, error) {
	s.lists.Add(1)
	lister, ok := s.ObjectStore.(EntityPrefixLister)
	if !ok {
		return EntityPrefixPage{}, ErrUnsupported
	}
	page, err := lister.ListEntityPrefixes(ctx, s.prefix+prefix, cursor, limit)
	for i, value := range page.Prefixes {
		if !strings.HasPrefix(value, s.prefix) {
			return EntityPrefixPage{}, ErrCorrupt
		}
		page.Prefixes[i] = strings.TrimPrefix(value, s.prefix)
	}
	return page, err
}

func liveQualificationStore(t *testing.T, prefix string) *qualificationStore {
	t.Helper()
	bucket, endpoint, region := os.Getenv("GREGALE_ENTITY_BUCKET"), os.Getenv("GREGALE_ENTITY_ENDPOINT"), os.Getenv("GREGALE_ENTITY_REGION")
	if bucket == "" || endpoint == "" || region == "" {
		t.Fatal("live qualification requires a dedicated private test bucket, endpoint and region")
	}
	provider, err := objectstorage.NewS3(objectstorage.BackendConfig{Endpoint: endpoint, S3Region: region, PathStyle: true, AccessKeyEnv: "AWS_ACCESS_KEY_ID", SecretKeyEnv: "AWS_SECRET_ACCESS_KEY", SessionTokenEnv: "AWS_SESSION_TOKEN"}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	conditional, ok := provider.(objectstorage.ConditionalStateProvider)
	if !ok {
		t.Fatal(ErrUnsupported)
	}
	store, err := NewProviderStore(conditional, bucket)
	if err != nil {
		t.Fatal(err)
	}
	return &qualificationStore{ObjectStore: store, prefix: prefix}
}

// TestLiveS3DurableEntityQualification is deliberately opt-in. It writes unique
// retained prefixes and never touches production entities. Cleanup requires a
// separate explicit opt-in and deletes only unused objects in the unique prefix.
// Run the same binary with a child marker to kill an actual owner process.
func TestLiveS3DurableEntityQualification(t *testing.T) {
	if os.Getenv("GREGALE_ENTITY_QUALIFY") != "1" {
		t.Skip("set GREGALE_ENTITY_QUALIFY=1 with a dedicated private bucket")
	}
	if prefix := os.Getenv("GREGALE_ENTITY_QUALIFY_CHILD_PREFIX"); prefix != "" {
		qualificationChild(t, prefix)
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	prefix := "gregale/entity-qualification/" + uuid.NewString() + "/"
	store := liveQualificationStore(t, prefix)
	started := time.Now()
	m, err := Open(ctx, store, Options{LeaseDuration: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	id := ID{AccountID: "qualification", AppID: "counter", EnvironmentID: "isolated", Namespace: "counters", Key: "restart"}
	first, err := m.Invoke(ctx, id, "first-process", request("one"), increment)
	if err != nil || first.Version != 1 {
		t.Fatalf("first commit = %+v %v", first, err)
	}
	restarted, err := Open(ctx, store, Options{LeaseDuration: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.Invoke(ctx, id, "second-process", request("one"), increment)
	if err != nil || !replayed.Replayed || string(first.Value) != string(replayed.Value) {
		t.Fatalf("restart replay = %+v %v", replayed, err)
	}
	store.lose.Store(true)
	if _, err := restarted.Invoke(ctx, id, "second-process", request("two"), increment); !errors.Is(err, ErrUncertain) {
		t.Fatalf("lost response = %v", err)
	}
	result, err := restarted.Invoke(ctx, id, "third-process", request("two"), increment)
	if err != nil || !result.Replayed || result.Version != 2 {
		t.Fatalf("uncertain outcome recovery = %+v %v", result, err)
	}
	qualificationFence(t, ctx, restarted, id)
	qualificationConcurrentCalls(t, ctx, restarted, id)
	qualificationProcessCrash(t, ctx, restarted, prefix)
	qualificationAlarm(t, ctx, restarted, id)
	qualificationStorage(t, ctx, restarted, id)
	if os.Getenv("GREGALE_ENTITY_CLEANUP_QUALIFY") == "1" {
		qualificationCleanup(t, ctx, restarted, id)
	}
	report, _ := json.Marshal(struct {
		Prefix       string `json:"retained_prefix"`
		Reads        int64  `json:"parent_get_attempts"`
		Writes       int64  `json:"parent_conditional_put_attempts"`
		Lists        int64  `json:"parent_list_attempts"`
		Deletes      int64  `json:"parent_delete_attempts"`
		ElapsedMS    int64  `json:"elapsed_ms"`
		ProcessCrash bool   `json:"owner_process_killed"`
	}{prefix, store.reads.Load(), store.writes.Load(), store.lists.Load(), store.deletes.Load(), time.Since(started).Milliseconds(), true})
	t.Log(string(report))
}

func qualificationStorage(t *testing.T, ctx context.Context, m *Manager, id ID) {
	t.Helper()
	claim, err := m.Acquire(ctx, id, "inventory-worker")
	if err != nil {
		t.Fatal(err)
	}
	var result InventoryResult
	for range 100 {
		result, err = m.Inventory(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
		if result.Complete {
			break
		}
	}
	if !result.Complete || !result.CurrentBytesKnown || result.Usage.ReceiptCount != 2 || result.CurrentBytes <= result.Usage.TotalBytes() {
		t.Fatal("native storage inventory failed", result)
	}
	if err := m.SetStorageLimit(ctx, claim, result.Usage.TotalBytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Execute(ctx, claim, request("over-cap"), increment); !errors.Is(err, ErrLimit) {
		t.Fatal("native quota was not enforced", err)
	}
	if replay, err := m.Execute(ctx, claim, request("one"), increment); err != nil || !replay.Replayed || replay.Version != 1 {
		t.Fatal("native receipt replay at capacity failed", replay, err)
	}
	if err := m.SetStorageLimit(ctx, claim, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Release(ctx, claim); err != nil {
		t.Fatal(err)
	}
}

func qualificationCleanup(t *testing.T, ctx context.Context, m *Manager, id ID) {
	t.Helper()
	claim, err := m.Acquire(ctx, id, "cleanup-worker")
	if err != nil {
		t.Fatal(err)
	}
	cursor, deleted := "", 0
	for {
		page, err := m.Collect(ctx, claim, cursor)
		if err != nil || page.Failed != 0 {
			t.Fatal(page, err)
		}
		deleted += page.Deleted
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if deleted == 0 {
		t.Fatal("provider cleanup retained every superseded object")
	}
	result, err := m.Execute(ctx, claim, request("one"), increment)
	if err != nil || !result.Replayed || result.Version != 1 || string(result.Value) != `{"count":1}` {
		t.Fatal(result, err)
	}
	assertCount(t, ctx, m, id, 2, 2)
	if err := m.Release(ctx, claim); err != nil {
		t.Fatal(err)
	}
}

func qualificationAlarm(t *testing.T, ctx context.Context, m *Manager, id ID) {
	t.Helper()
	id.Key = "alarm"
	at := time.Now().UTC().Add(-time.Second)
	if _, err := m.Invoke(ctx, id, "caller", request("schedule"), func(ctx context.Context, view View) (Transition, error) {
		transition, err := increment(ctx, view)
		transition.AlarmAt = &at
		return transition, err
	}); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	for {
		page, err := m.ScanDueAlarms(ctx, cursor)
		if err != nil || page.Failed != 0 {
			t.Fatalf("provider alarm discovery = %+v %v", page, err)
		}
		for _, alarm := range page.Alarms {
			if alarm.Entity != id {
				continue
			}
			result, err := m.InvokeAlarm(ctx, alarm, "alarm-worker", consumeTestAlarm)
			if err != nil || result.Version != 2 {
				t.Fatalf("provider alarm commit = %+v %v", result, err)
			}
			result, err = m.InvokeAlarm(ctx, alarm, "replacement-worker", consumeTestAlarm)
			if err != nil || !result.Replayed {
				t.Fatalf("provider alarm replay = %+v %v", result, err)
			}
			assertCount(t, ctx, m, id, 2, 2)
			return
		}
		if page.NextCursor == "" {
			t.Fatal("committed alarm missing from provider listing")
		}
		cursor = page.NextCursor
	}
}

func qualificationConcurrentCalls(t *testing.T, ctx context.Context, m *Manager, id ID) {
	t.Helper()
	id.Key = "concurrent"
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Invoke(ctx, id, "concurrent-process", request(fmt.Sprint(i)), increment); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	assertCount(t, ctx, m, id, 8, 8)
}

func TestS3QualificationHarnessAgainstLocalWireFixture(t *testing.T) {
	upstream := newS3WireServer(t)
	t.Setenv("GREGALE_ENTITY_QUALIFY", "1")
	t.Setenv("GREGALE_ENTITY_CLEANUP_QUALIFY", "1")
	t.Setenv("GREGALE_ENTITY_QUALIFY_CHILD_PREFIX", "")
	t.Setenv("GREGALE_ENTITY_BUCKET", "private")
	t.Setenv("GREGALE_ENTITY_ENDPOINT", upstream.URL)
	t.Setenv("GREGALE_ENTITY_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "fixture-only")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fixture-only")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("DATABASE_URL", "postgres://unused@127.0.0.1:1/unused")
	TestLiveS3DurableEntityQualification(t)
}

func qualificationFence(t *testing.T, ctx context.Context, m *Manager, id ID) {
	t.Helper()
	id.Key = "fencing"
	old, err := m.Acquire(ctx, id, "old")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Execute(ctx, old, request("old"), func(ctx context.Context, view View) (Transition, error) {
		// Native storage, injected future lease clock: place takeover between
		// reading old state and publishing old work without a long wall-clock wait.
		future, err := Open(ctx, m.store, Options{Now: func() time.Time { return time.Now().Add(time.Minute) }})
		if err != nil {
			return Transition{}, err
		}
		claim, err := future.Acquire(ctx, id, "replacement")
		if err != nil {
			return Transition{}, err
		}
		if _, err := future.Execute(ctx, claim, request("replacement"), increment); err != nil {
			return Transition{}, err
		}
		return increment(ctx, view)
	})
	if !errors.Is(err, ErrStaleOwner) {
		t.Fatalf("obsolete owner publication = %v", err)
	}
	assertCount(t, ctx, m, id, 1, 1)
}

func qualificationChild(t *testing.T, prefix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	m, err := Open(ctx, liveQualificationStore(t, prefix), Options{LeaseDuration: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	id := ID{AccountID: "qualification", AppID: "counter", EnvironmentID: "isolated", Namespace: "counters", Key: "crash"}
	claim, err := m.Acquire(ctx, id, "crashing-process")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Execute(ctx, claim, request("before-crash"), increment); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ENTITY_OWNER_READY")
	// The parent kills this process. No Release or deferred cleanup runs.
	<-ctx.Done()
	t.Fatal("qualification parent failed to kill owner")
}

func qualificationProcessCrash(t *testing.T, ctx context.Context, m *Manager, prefix string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, binary, "-test.run=^TestLiveS3DurableEntityQualification$", "-test.v")
	child.Env = append(os.Environ(), "GREGALE_ENTITY_QUALIFY_CHILD_PREFIX="+prefix)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	scanner := bufio.NewScanner(stdout)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "ENTITY_OWNER_READY" {
			ready = true
			break
		}
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if !ready {
		t.Fatal("qualification owner never committed state and became ready")
	}
	id := ID{AccountID: "qualification", AppID: "counter", EnvironmentID: "isolated", Namespace: "counters", Key: "crash"}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := m.Invoke(ctx, id, "replacement-process", request("before-crash"), increment)
		if err == nil {
			if !result.Replayed || result.Version != 1 {
				t.Fatalf("crash recovery duplicated committed state: %+v", result)
			}
			break
		}
		if !errors.Is(err, ErrBusy) && !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	assertCount(t, ctx, m, id, 1, 1)
}

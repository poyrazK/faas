package objectstorage_test

// adr: 712

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// This fixture implements native JSON API generation preconditions. It does
// not qualify a live bucket; it exercises the production SDK and entity engine
// together, including publication whose successful response is lost.
type gcsEntityWire struct {
	mu                  sync.Mutex
	objects             map[string]gcsEntityWireObject
	next                int64
	manifestWrites      int
	loseNextManifestAck bool
	ignorePreconditions bool
	lists               int
	deletes             int
}

type gcsEntityWireObject struct {
	body       []byte
	generation int64
}

func newGCSEntityWire(t *testing.T) (*gcsEntityWire, *objectstorage.GCS) {
	t.Helper()
	wire := &gcsEntityWire{objects: make(map[string]gcsEntityWireObject), next: 1}
	p := objectstorage.NewGCSConditionalFixtureForTest(t, http.HandlerFunc(wire.serveHTTP))
	return wire, p
}

func (s *gcsEntityWire) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Get("alt") == "media":
		const prefix = "/b/private/o/"
		index := strings.Index(r.URL.Path, prefix)
		if index < 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key := r.URL.Path[index+len(prefix):]
		s.mu.Lock()
		object, ok := s.objects[key]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("X-Goog-Generation", strconv.FormatInt(object.generation, 10))
		w.Header().Set("X-Goog-Metageneration", "1")
		w.Header().Set("ETag", "same-content-etag")
		w.Header().Set("Content-Length", strconv.Itoa(len(object.body)))
		_, _ = w.Write(object.body)
	case r.Method == http.MethodPost && r.URL.Query().Get("uploadType") == "multipart":
		s.write(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/b/private/o"):
		s.list(w, r)
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/b/private/o/"):
		key := strings.SplitN(r.URL.Path, "/b/private/o/", 2)[1]
		s.mu.Lock()
		delete(s.objects, key)
		s.deletes++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (s *gcsEntityWire) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, err := strconv.Atoi(query.Get("maxResults"))
	if err != nil || limit < 1 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Short native pages exercise continuation through inventory and collection.
	limit = min(limit, 3)
	prefix, delimiter, after := query.Get("prefix"), query.Get("delimiter"), query.Get("pageToken")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists++
	entries := s.listEntries(prefix, delimiter, query.Get("startOffset"))
	keys := make([]string, 0, len(entries))
	for key := range entries {
		if key > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var items []map[string]string
	var prefixes []string
	next := ""
	for index, key := range keys {
		if index == limit {
			next = keys[index-1]
			break
		}
		if entries[key] {
			prefixes = append(prefixes, key)
		} else {
			object := s.objects[key]
			items = append(items, map[string]string{"name": key, "size": strconv.Itoa(len(object.body)), "generation": strconv.FormatInt(object.generation, 10)})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "prefixes": prefixes, "nextPageToken": next})
}

// listEntries is called with the fixture mutex held.
func (s *gcsEntityWire) listEntries(prefix, delimiter, start string) map[string]bool {
	entries := make(map[string]bool)
	for key := range s.objects {
		if !strings.HasPrefix(key, prefix) || key < start {
			continue
		}
		if delimiter != "" {
			if index := strings.Index(strings.TrimPrefix(key, prefix), delimiter); index >= 0 {
				key = key[:len(prefix)+index+len(delimiter)]
				entries[key] = true
				continue
			}
		}
		entries[key] = false
	}
	return entries
}

func (s *gcsEntityWire) write(w http.ResponseWriter, r *http.Request) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	parts := multipart.NewReader(r.Body, params["boundary"])
	metadata, err := parts.NextPart()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var attrs struct {
		Name         string `json:"name"`
		CacheControl string `json:"cacheControl"`
	}
	if err := json.NewDecoder(metadata).Decode(&attrs); err != nil || attrs.Name == "" || attrs.CacheControl != "no-store" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	part, err := parts.NextPart()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(part)
	condition, parseErr := strconv.ParseInt(r.URL.Query().Get("ifGenerationMatch"), 10, 64)
	if err != nil || parseErr != nil || condition < 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	previous, exists := s.objects[attrs.Name]
	if !s.ignorePreconditions && (condition == 0 && exists || condition != 0 && (!exists || condition != previous.generation)) {
		s.mu.Unlock()
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	generation := s.next
	s.next++
	s.objects[attrs.Name] = gcsEntityWireObject{body: body, generation: generation}
	lost := false
	if strings.HasSuffix(attrs.Name, "/manifest.json") {
		s.manifestWrites++
		lost = s.loseNextManifestAck
		s.loseNextManifestAck = false
	}
	s.mu.Unlock()
	if lost {
		// The object is durable, but the caller receives no successful ACK.
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	checksum := crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli))
	_ = json.NewEncoder(w).Encode(map[string]string{
		"generation": strconv.FormatInt(generation, 10),
		"size":       strconv.Itoa(len(body)),
		"crc32c":     base64.StdEncoding.EncodeToString(binary.BigEndian.AppendUint32(nil, checksum)),
		"etag":       "same-content-etag",
	})
}

func gcsEntityManager(t *testing.T, provider *objectstorage.GCS, clock *atomic.Int64) *durableentity.Manager {
	t.Helper()
	store, err := durableentity.NewProviderStore(provider, "private")
	if err != nil {
		t.Fatal(err)
	}
	m, err := durableentity.Open(t.Context(), store, durableentity.Options{LeaseDuration: time.Second, Now: func() time.Time { return time.Unix(0, clock.Load()) }})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func gcsEntityRequest(id string) durableentity.Request {
	return durableentity.Request{ID: id, Payload: json.RawMessage(`{"delta":1}`)}
}

func gcsEntityIncrement(_ context.Context, view durableentity.View) (durableentity.Transition, error) {
	var state struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(view.Data, &state); err != nil {
		return durableentity.Transition{}, err
	}
	state.Count++
	body, err := json.Marshal(state)
	return durableentity.Transition{Data: body, Result: body}, err
}

func TestGCSEntityRestartAndLostPublicationAcknowledgement(t *testing.T) {
	wire, provider := newGCSEntityWire(t)
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC).UnixNano())
	m := gcsEntityManager(t, provider, clock)
	id := durableentity.ID{AccountID: "account", AppID: "app", EnvironmentID: "environment", Namespace: "counters", Key: "customer:456"}
	claim, err := m.Acquire(t.Context(), id, "first-process")
	if err != nil {
		t.Fatal(err)
	}
	wire.mu.Lock()
	writesBefore := wire.manifestWrites
	wire.loseNextManifestAck = true
	wire.mu.Unlock()
	if _, err := m.Execute(t.Context(), claim, gcsEntityRequest("first"), gcsEntityIncrement); !errors.Is(err, durableentity.ErrUncertain) {
		t.Fatal("lost publication ACK was not uncertain", err)
	}
	wire.mu.Lock()
	writesAfter := wire.manifestWrites
	wire.mu.Unlock()
	if writesAfter != writesBefore+1 {
		t.Fatal("SDK retried a dispatched manifest write", writesBefore, writesAfter)
	}
	first, err := m.Execute(t.Context(), claim, gcsEntityRequest("first"), func(context.Context, durableentity.View) (durableentity.Transition, error) {
		t.Error("lost ACK replay executed customer code")
		return durableentity.Transition{}, errors.New("replay must not execute")
	})
	if err != nil || !first.Replayed || first.Version != 1 || string(first.Value) != `{"count":1}` {
		t.Fatal("lost ACK failed to replay the committed result", first, err)
	}
	if _, err := m.Execute(t.Context(), claim, gcsEntityRequest("second"), gcsEntityIncrement); err != nil {
		t.Fatal(err)
	}
	if err := m.Release(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	restarted := gcsEntityManager(t, provider, clock)
	next, err := restarted.Acquire(t.Context(), id, "restarted-process")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := restarted.Execute(t.Context(), next, gcsEntityRequest("first"), gcsEntityIncrement)
	if err != nil || !replay.Replayed || replay.Version != first.Version || string(replay.Value) != string(first.Value) {
		t.Fatal("restart lost the original receipt", replay, err)
	}
	view, err := restarted.Read(t.Context(), id)
	if err != nil || view.Version != 2 || string(view.Data) != `{"count":2}` {
		t.Fatal("restart lost committed state", view, err)
	}
}

func TestGCSEntityTakeoverFencesInFlightTransition(t *testing.T) {
	_, provider := newGCSEntityWire(t)
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC).UnixNano())
	old := gcsEntityManager(t, provider, clock)
	next := gcsEntityManager(t, provider, clock)
	id := durableentity.ID{AccountID: "account", AppID: "app", Namespace: "counters", Key: "takeover"}
	claim, err := old.Acquire(t.Context(), id, "old-process")
	if err != nil {
		t.Fatal(err)
	}
	started, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(resume) }) })
	go func() {
		_, err := old.Execute(t.Context(), claim, gcsEntityRequest("late"), func(ctx context.Context, view durableentity.View) (durableentity.Transition, error) {
			close(started)
			select {
			case <-resume:
				return gcsEntityIncrement(ctx, view)
			case <-ctx.Done():
				return durableentity.Transition{}, ctx.Err()
			}
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("old owner did not start its transition")
	}
	clock.Add(int64(2 * time.Second))
	newClaim, err := next.Acquire(t.Context(), id, "new-process")
	if err != nil || newClaim.Epoch <= claim.Epoch {
		t.Fatal("takeover failed", newClaim, err)
	}
	if _, err := next.Execute(t.Context(), newClaim, gcsEntityRequest("winner"), gcsEntityIncrement); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(resume) })
	select {
	case err := <-done:
		if !errors.Is(err, durableentity.ErrStaleOwner) {
			t.Fatal("late owner was not fenced", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late owner did not finish")
	}
	view, err := next.Read(t.Context(), id)
	if err != nil || view.Version != 1 || string(view.Data) != `{"count":1}` {
		t.Fatal("obsolete owner changed committed state", view, err)
	}
}

func TestGCSConditionalStateConcurrentCASAndIdenticalContent(t *testing.T) {
	_, provider := newGCSEntityWire(t)
	body := []byte(`{"count":1}`)
	first, err := provider.WriteStateObject(t.Context(), "private", "state/counter", body, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.WriteStateObject(t.Context(), "private", "state/counter", body, first)
	if err != nil || second == first {
		t.Fatal("identical content reused its CAS token", first, second, err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := provider.WriteStateObject(t.Context(), "private", "state/counter", body, second)
			results <- err
		}()
	}
	close(start)
	winners, rejected := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			winners++
		case errors.Is(err, objectstorage.ErrPreconditionFailed):
			rejected++
		default:
			t.Fatal("unexpected CAS result", err)
		}
	}
	if winners != 1 || rejected != 1 {
		t.Fatal("competing generation writes did not have exactly one winner", winners, rejected)
	}
	if _, err := provider.WriteStateObject(t.Context(), "private", "state/counter", body, first); !errors.Is(err, objectstorage.ErrPreconditionFailed) {
		t.Fatal("obsolete generation was accepted", err)
	}
}

func TestGCSEntityStartupRejectsIgnoredGenerationConditions(t *testing.T) {
	wire, provider := newGCSEntityWire(t)
	wire.mu.Lock()
	wire.ignorePreconditions = true
	wire.mu.Unlock()
	store, err := durableentity.NewProviderStore(provider, "private")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := durableentity.Open(t.Context(), store, durableentity.Options{}); !errors.Is(err, durableentity.ErrUnsupported) {
		t.Fatal("startup accepted a store that ignores generation conditions", err)
	}
}

package statusmirror

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type memStore struct {
	mu       sync.Mutex
	objects  map[string]Object
	cache    map[string]string
	version  int
	conflict int // number of upcoming conditional puts to reject
}

func newMemStore() *memStore {
	return &memStore{objects: map[string]Object{}, cache: map[string]string{}}
}

func (m *memStore) Get(_ context.Context, key string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	object, ok := m.objects[key]
	if !ok {
		return Object{}, ErrNotFound
	}
	return object, nil
}

func (m *memStore) Put(_ context.Context, key string, body []byte, cacheControl, ifMatch string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.objects[key]
	if ifMatch != "" && m.conflict > 0 {
		m.conflict--
		return ErrConflict
	}
	switch {
	case ifMatch == "-" && exists:
		return ErrConflict
	case ifMatch != "" && ifMatch != "-" && (!exists || current.ETag != ifMatch):
		return ErrConflict
	}
	m.version++
	m.objects[key] = Object{Body: append([]byte(nil), body...), ETag: strconv.Itoa(m.version)}
	m.cache[key] = cacheControl
	return nil
}

func TestLoad(t *testing.T) {
	env := func(values map[string]string) func(string) string { return func(k string) string { return values[k] } }
	if r, err := Load(env(nil)); r != nil || err != nil {
		t.Fatalf("unset mirror: got %v, %v; want nil, nil", r, err)
	}
	if _, err := Load(env(map[string]string{EnvEndpoint: "https://acct.r2.cloudflarestorage.com"})); err == nil {
		t.Fatal("partial configuration must fail")
	}
	full := map[string]string{EnvEndpoint: "https://acct.r2.cloudflarestorage.com", EnvBucket: "gregale-status", EnvAccessKey: "a", EnvSecretKey: "s"}
	if r, err := Load(env(full)); err != nil || r == nil {
		t.Fatalf("full configuration: got %v, %v", r, err)
	}
	full[EnvEndpoint] = "http://acct.r2.cloudflarestorage.com"
	if _, err := Load(env(full)); err == nil {
		t.Fatal("plain HTTP endpoint must fail")
	}
}

func TestPublishStatusWritesEnvelopeWithCacheControl(t *testing.T) {
	store := newMemStore()
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	overview := api.PublicStatusOverview{OverallStatus: "operational", DataStatus: "fresh", UpdatedAt: now.Add(-30 * time.Second)}
	if err := PublishStatus(context.Background(), store, "apid-1", overview, now); err != nil {
		t.Fatal(err)
	}
	var doc StatusDocument
	if err := json.Unmarshal(store.objects[StatusKey].Body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != SchemaVersion || !doc.PublishedAt.Equal(now) || doc.Publisher != "apid-1" || doc.Status.OverallStatus != "operational" {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if store.cache[StatusKey] != StatusCacheControl {
		t.Fatalf("cache-control = %q", store.cache[StatusKey])
	}
	if StaleAfter < 2*PublishInterval+15*time.Second {
		t.Fatal("staleness threshold must tolerate a missed push plus cache age")
	}
}

func TestPostAndResolveNotice(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	event, err := PostNotice(ctx, store, NoticeInput{
		Title: "API unreachable", Impact: "major_outage", Components: []string{"api_console"},
		State: "investigating", Message: "We are investigating.", StartsAt: now,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if store.cache[NoticesKey] != NoticesCacheControl {
		t.Fatalf("cache-control = %q", store.cache[NoticesKey])
	}
	resolved, err := UpdateNotice(ctx, store, NoticeUpdate{ID: event.ID, State: "resolved", Message: "Recovered."}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResolvedAt == nil || resolved.State != "resolved" || len(resolved.Updates) != 2 {
		t.Fatalf("unexpected resolved notice: %+v", resolved)
	}
	if _, err := UpdateNotice(ctx, store, NoticeUpdate{ID: event.ID, Message: "again"}, now.Add(2*time.Hour)); err == nil {
		t.Fatal("updating a resolved notice must fail")
	}
	doc, _, err := ReadNotices(ctx, store)
	if err != nil || len(doc.Events) != 1 {
		t.Fatalf("notices = %+v, %v", doc, err)
	}
}

func TestPostNoticeValidates(t *testing.T) {
	store := newMemStore()
	now := time.Now()
	cases := []NoticeInput{
		{Title: "", Impact: "major_outage", Components: []string{"api_console"}, State: "investigating", Message: "m"},
		{Title: "t", Impact: "operational", Components: []string{"api_console"}, State: "investigating", Message: "m"},
		{Title: "t", Impact: "major_outage", Components: []string{"nope"}, State: "investigating", Message: "m"},
		{Title: "t", Impact: "major_outage", Components: []string{"api_console"}, State: "investigating", Message: ""},
	}
	for i, in := range cases {
		in.StartsAt = now
		if _, err := PostNotice(context.Background(), store, in, now); err == nil {
			t.Fatalf("case %d: want validation error", i)
		}
	}
	if len(store.objects) != 0 {
		t.Fatal("invalid notices must not write")
	}
}

func TestPostNoticeRetriesConflicts(t *testing.T) {
	store := newMemStore()
	store.conflict = 2
	now := time.Now()
	in := NoticeInput{Title: "t", Impact: "degraded", Components: []string{"networking"}, State: "identified", Message: "m", StartsAt: now}
	if _, err := PostNotice(context.Background(), store, in, now); err != nil {
		t.Fatalf("want retry to succeed, got %v", err)
	}
	store.conflict = 10
	if _, err := PostNotice(context.Background(), store, in, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict after retries, got %v", err)
	}
}

func TestPruneNoticesKeepsOpenAndRecentResolved(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	var events []api.PublicStatusEvent
	events = append(events, api.PublicStatusEvent{ID: "open"})
	old := now.Add(-8 * 24 * time.Hour)
	events = append(events, api.PublicStatusEvent{ID: "old", ResolvedAt: &old})
	for i := 0; i < maxNotices+5; i++ {
		at := now.Add(-time.Duration(i) * time.Minute)
		events = append(events, api.PublicStatusEvent{ID: "r" + strconv.Itoa(i), ResolvedAt: &at})
	}
	pruned := pruneNotices(events, now)
	if len(pruned) != maxNotices || pruned[0].ID != "open" || pruned[1].ID != "r0" {
		t.Fatalf("pruned = %d events, first %q %q", len(pruned), pruned[0].ID, pruned[1].ID)
	}
	for _, event := range pruned {
		if event.ID == "old" {
			t.Fatal("resolved notice past retention must be dropped")
		}
	}
}

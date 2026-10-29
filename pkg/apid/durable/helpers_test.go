package durable

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeHost is a Host for unit tests that never reach apid's router,
// invocation queue or environment catalog.
type fakeHost struct {
	store     state.Store
	execution bool
}

var _ Host = fakeHost{}

func (fakeHost) LoadApp(http.ResponseWriter, *http.Request, state.Account, string) (state.App, bool) {
	panic("fakeHost.LoadApp: unit tests do not route requests")
}

func (h fakeHost) Store() state.Store { return h.store }

func (fakeHost) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func (fakeHost) EmitAudit(context.Context, string, *string, map[string]any) {}

func (fakeHost) ResolveQueueEnvironment(context.Context, state.Account, state.App, string) (string, *state.ProjectEnvironment, *api.Problem) {
	panic("fakeHost.ResolveQueueEnvironment: unit tests do not resolve environments")
}

func (fakeHost) EnqueuePreparedInvocation(context.Context, state.Invocation, string) (state.Invocation, *api.Problem) {
	panic("fakeHost.EnqueuePreparedInvocation: unit tests do not enqueue")
}

func (fakeHost) ResultRetention(state.Account) *time.Time { return nil }

func (fakeHost) ExecutionPayloadRecipients() ([]*age.X25519Recipient, string, error) {
	return nil, "", errors.New("no execution payload recipient")
}

func (h fakeHost) ExecutionAPIEnabled() bool { return h.execution }

// testBucket is an in-memory durableentity object store with the optional
// listing and cleanup capabilities the engine probes for.
type testBucket struct {
	mu      sync.Mutex
	objects map[string]testObject
	rev     int
	lose    atomic.Bool
}

type testObject struct {
	body []byte
	etag string
}

func newTestBucket() *testBucket { return &testBucket{objects: map[string]testObject{}} }

func (b *testBucket) Get(ctx context.Context, key string, maxBytes int64) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	object, exists := b.objects[key]
	if !exists {
		return nil, "", durableentity.ErrNotFound
	}
	if int64(len(object.body)) > maxBytes {
		return nil, "", durableentity.ErrLimit
	}
	return append([]byte(nil), object.body...), object.etag, nil
}

func (b *testBucket) Put(ctx context.Context, key string, body []byte, expected string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	object, exists := b.objects[key]
	if expected == "" && exists || expected != "" && (!exists || object.etag != expected) {
		return "", durableentity.ErrConflict
	}
	b.rev++
	etag := fmt.Sprintf("\"%d\"", b.rev)
	b.objects[key] = testObject{body: append([]byte(nil), body...), etag: etag}
	if strings.HasSuffix(key, "/manifest.json") && strings.Contains(string(body), `"state_version":1`) && b.lose.Swap(false) {
		return "", errors.New("fixture lost response with private-provider-detail")
	}
	return etag, nil
}

func (b *testBucket) ListEntityPrefixes(ctx context.Context, prefix, cursor string, limit int32) (durableentity.EntityPrefixPage, error) {
	if err := ctx.Err(); err != nil {
		return durableentity.EntityPrefixPage{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[string]bool{}
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) {
			name, _, found := strings.Cut(strings.TrimPrefix(key, prefix), "/")
			if found && prefix+name+"/" > cursor {
				seen[prefix+name+"/"] = true
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	page := durableentity.EntityPrefixPage{Prefixes: keys}
	if len(keys) > int(limit) {
		page.Prefixes = keys[:limit]
		page.NextCursor = page.Prefixes[len(page.Prefixes)-1]
	}
	return page, nil
}

func (b *testBucket) ListEntityObjects(ctx context.Context, prefix, cursor string, limit int32) (durableentity.CleanupObjects, error) {
	if err := ctx.Err(); err != nil {
		return durableentity.CleanupObjects{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var keys []string
	for key := range b.objects {
		if strings.HasPrefix(key, prefix) && key > cursor {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	page := durableentity.CleanupObjects{Keys: keys}
	if len(keys) > int(limit) {
		page.Keys = keys[:limit]
		page.NextCursor = page.Keys[len(page.Keys)-1]
	}
	return page, nil
}

func (b *testBucket) DeleteEntityObject(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

package s3gateway

// adr: 20261031193635

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestGatewayOverlayChecksEachSourcePrefix lists a parent with more emptied
// prefixes than one page checks: the listing pages past the probe bound and
// never shows a prefix the branch emptied.
func TestGatewayOverlayChecksEachSourcePrefix(t *testing.T) {
	const prefixes = 2*overlayPrefixProbes + 3
	source := map[string]string{}
	for i := range prefixes {
		source[fmt.Sprintf("p%02d/k.txt", i)] = "parent"
	}
	handler, _ := newOverlayGatewayHandler(t, source, map[string]string{})
	var want []string
	for i := range prefixes {
		if i%7 == 6 {
			want = append(want, fmt.Sprintf("p%02d/", i))
			continue
		}
		key := fmt.Sprintf("p%02d/k.txt", i)
		if got := serveOverlay(t, handler, http.MethodDelete, "https://s3.gregale.dev/br-preview/"+key); got.Code != http.StatusNoContent {
			t.Fatalf("delete %s = %d", key, got.Code)
		}
	}
	keys, pages := listOverlayKeys(t, handler, url.Values{"delimiter": {"/"}})
	if !slices.Equal(keys, want) || pages < 3 {
		t.Fatalf("listing = %v in %d page(s), want %v over at least 3", keys, pages, want)
	}
}

// batchTombstoneStore refuses a lookup larger than the store's batch, as the
// real stores do, and records each lookup's size.
type batchTombstoneStore struct {
	state.ObjectBucketOverlayStore
	deleted map[string]bool
	batches []int
}

func (s *batchTombstoneStore) ObjectBucketOverlayTombstones(_ context.Context, _ string, keys []string) (map[string]bool, error) {
	if len(keys) > state.ObjectBucketOverlayTombstoneBatch {
		return nil, state.ErrInvalidArgument
	}
	s.batches = append(s.batches, len(keys))
	out := map[string]bool{}
	for _, key := range keys {
		if s.deleted[key] {
			out[key] = true
		}
	}
	return out, nil
}

// TestOverlayTombstonesLooksUpInBatches covers an as_of listing, which reads
// whole provider pages and so can hold more keys than one lookup takes.
func TestOverlayTombstonesLooksUpInBatches(t *testing.T) {
	keys := make([]string, 2*state.ObjectBucketOverlayTombstoneBatch+1)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%05d", i)
	}
	last := keys[len(keys)-1]
	store := &batchTombstoneStore{deleted: map[string]bool{keys[0]: true, last: true}}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	hidden, err := overlayTombstones(r, store, "bucket", keys)
	if err != nil || len(hidden) != 2 || !hidden[keys[0]] || !hidden[last] {
		t.Fatalf("hidden = %v, %v", hidden, err)
	}
	if !slices.Equal(store.batches, []int{1000, 1000, 1}) {
		t.Fatalf("lookups = %v", store.batches)
	}
}

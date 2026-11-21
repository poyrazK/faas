package s3gateway

// adr: 20261031193635

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectoverlay"
)

// TestGatewayOverlayChecksEachSourcePrefix lists a parent with more emptied
// prefixes than one page checks: the listing pages past the probe bound and
// never shows a prefix the branch emptied.
func TestGatewayOverlayChecksEachSourcePrefix(t *testing.T) {
	const prefixes = 2*objectoverlay.PrefixProbes + 3
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

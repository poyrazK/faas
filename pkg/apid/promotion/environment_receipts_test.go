// adr: 20261105153007
package promotion

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
)

func TestEnvironmentPromotionAttemptHeadersAndContext(t *testing.T) {
	type marker struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), marker{}, "membership"))
	defer cancel()
	valid := http.Header{}
	valid.Set(api.EnvironmentPromotionAttemptHeader, strings.Repeat("a", 32))
	valid.Set(api.EnvironmentPromotionHashHeader, strings.Repeat("b", 64))
	for _, kind := range []string{"valid", "legacy", "missing", "duplicate", "invalid"} {
		h := valid.Clone()
		switch kind {
		case "legacy":
			h = http.Header{}
		case "missing":
			h.Del(api.EnvironmentPromotionHashHeader)
		case "duplicate":
			h.Add(api.EnvironmentPromotionAttemptHeader, strings.Repeat("a", 32))
		case "invalid":
			h.Set(api.EnvironmentPromotionHashHeader, strings.Repeat("B", 64))
		}
		ctx, problem := EnvironmentAttempt(parent, h)
		if kind != "valid" && kind != "legacy" {
			if problem == nil {
				t.Fatal("accepted invalid headers", kind)
			}
			continue
		}
		if problem != nil || ctx.Value(marker{}) != "membership" || state.HasEnvironmentPromotionAttempt(ctx) != (kind == "valid") {
			t.Fatal("lost inherited context or legacy contract", kind)
		}
		if kind == "valid" {
			cancel()
			if ctx.Err() != context.Canceled {
				t.Fatal("lost request cancellation")
			}
		}
	}
	if EnvironmentIdempotencyKey(strings.Repeat("x", 255)) != strings.Repeat("x", 255) {
		t.Fatal("changed a storable key")
	}
	first := EnvironmentIdempotencyKey(strings.Repeat("x", 256))
	if len(first) > 255 || first != EnvironmentIdempotencyKey(strings.Repeat("x", 256)) || first == EnvironmentIdempotencyKey(strings.Repeat("y", 256)) {
		t.Fatal("scoped key normalization")
	}
}

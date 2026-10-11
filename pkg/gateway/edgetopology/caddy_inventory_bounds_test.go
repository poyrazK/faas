package edgetopology

// adr: 703

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCaddyInventoryWholeResponseBudgetSupportsLargeConfigsAndRejectsOverflow(t *testing.T) {
	b := edgeBinding(t)
	base := caddyFixture(t, b, false)
	// Whole configs can contain opaque TLS/logging material substantially
	// larger than a selected proxy. It must never escape in the inventory.
	padding := strings.Repeat("x", api.RuntimeUpgradePublicEdgeProxyMaxBytes)
	large := []byte(`{"logging":{"opaque":"` + padding + `"},` + string(base[1:]))
	var gets atomic.Int32
	got, err := staticWholeProbe(t, large, &gets).Collect(t.Context())
	if err != nil || got.ConfigSHA256 != configDigest(large) || strings.Contains(string(jsonConfig(t, got)), padding) {
		t.Fatal("bounded large config rejected or exposed opaque values", err)
	}
	p := wholeProbe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"whole one"`)
		// Flush before the body so the size bound is exercised on streaming
		// replies with no declared content length.
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(strings.Repeat(" ", api.RuntimeUpgradePublicEdgeCaddyConfigMaxBytes+1)))
	})
	if got, err := p.Collect(t.Context()); !errors.Is(err, ErrUnverified) || got.ConfigSHA256 != "" {
		t.Fatal("oversized streaming config returned partial inventory", got, err)
	}
}

func TestCaddyInventoryInvalidReviewsCannotStartCollectionOrAssertAbsence(t *testing.T) {
	body := caddyFixture(t, edgeBinding(t), false)
	var gets atomic.Int32
	p := staticWholeProbe(t, body, &gets)
	for _, review := range []CaddyInventoryReview{
		{},
		{ConfigSHA256: configDigest(body)},
		{ConfigSHA256: "invalid", Proxies: []CaddyProxyReview{{Path: configPath}}},
		{ConfigSHA256: configDigest(body), Proxies: []CaddyProxyReview{{Path: "/config/apps/http/servers/srv0/routes/0/handle/0"}}},
	} {
		if got, err := p.ObserveBindings(t.Context(), review); !errors.Is(err, ErrUnverified) || got.ConfigSHA256 != "" || gets.Load() != 0 {
			t.Fatal("invalid or empty proxy review started collection", got, err, gets.Load())
		}
	}
	if _, err := NewCaddyInventoryProbe("http://localhost:2019", edgeToken); err == nil {
		t.Fatal("implicit admin origin accepted")
	}
	if _, err := NewCaddyInventoryProbe("http://127.0.0.1:2019", "invalid secret"); err == nil {
		t.Fatal("invalid private token accepted")
	}
}

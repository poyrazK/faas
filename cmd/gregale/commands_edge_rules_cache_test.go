package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildEdgeRuleCacheActionStaleWhileRevalidate(t *testing.T) {
	raw, err := buildEdgeRuleAction("cache", edgeRuleActionInputs{
		CacheMaxAgeSeconds:               30,
		CacheStaleWhileRevalidateSeconds: 45,
		CacheStaleIfErrorSeconds:         120,
		CacheMethods:                     []string{"GET"},
	})
	if err != nil {
		t.Fatalf("buildEdgeRuleAction: %v", err)
	}
	var action api.EdgeRuleCacheAction
	if err := json.Unmarshal(raw, &action); err != nil {
		t.Fatalf("unmarshal action: %v", err)
	}
	if action.StaleWhileRevalidateSeconds != 45 {
		t.Fatalf("stale_while_revalidate_seconds = %d, want 45", action.StaleWhileRevalidateSeconds)
	}

	_, err = buildEdgeRuleAction("cache", edgeRuleActionInputs{
		CacheMaxAgeSeconds:               30,
		CacheStaleWhileRevalidateSeconds: api.ResponseCacheStaleWhileRevalidateMaxSeconds + 1,
	})
	if err == nil {
		t.Fatal("buildEdgeRuleAction accepted stale-while-revalidate above the cap")
	}
}

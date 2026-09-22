package api

import "testing"

func TestEdgeRuleCacheActionValidatesStaleWhileRevalidate(t *testing.T) {
	action := &EdgeRuleCacheAction{
		MaxAgeSeconds:               30,
		StaleWhileRevalidateSeconds: ResponseCacheStaleWhileRevalidateMaxSeconds + 1,
		StaleIfErrorSeconds:         60,
	}
	if problem := action.Validate(); problem == nil {
		t.Fatal("Validate accepted stale_while_revalidate_seconds above the platform cap")
	}

	action.StaleWhileRevalidateSeconds = 30
	if problem := action.Validate(); problem != nil {
		t.Fatalf("Validate rejected bounded stale-while-revalidate: %v", problem)
	}
}

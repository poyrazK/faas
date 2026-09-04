package gateway

import "testing"

func BenchmarkRouteCachePeek(b *testing.B) {
	cache := NewRouteCache(1024)
	cache.Put("hot.apps.dom", "app-1")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, ok := cache.Peek("hot.apps.dom"); !ok {
			b.Fatal("Peek missed seeded route")
		}
	}
}

func BenchmarkRouteCacheGet(b *testing.B) {
	cache := NewRouteCache(1024)
	cache.Put("hot.apps.dom", "app-1")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, ok := cache.Get("hot.apps.dom"); !ok {
			b.Fatal("Get missed seeded route")
		}
	}
}

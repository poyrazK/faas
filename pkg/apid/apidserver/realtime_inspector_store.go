package apidserver

import "github.com/onebox-faas/faas/pkg/state"

func realtimeEndpointStore(s *server) (state.ManagedRealtimeEndpointStore, bool) {
	store, ok := s.store.(state.ManagedRealtimeEndpointStore)
	return store, ok
}

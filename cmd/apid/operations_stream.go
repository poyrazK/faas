package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// One listener per apid process fans out wake hints. Stream count must not
// consume the PostgreSQL pool's connection count. Durable events remain truth.
type operationStreamHub struct {
	mu       sync.Mutex
	watchers map[string]map[chan struct{}]struct{}
}

func (s *server) startOperationNotifications(ctx context.Context) {
	hub := &operationStreamHub{watchers: map[string]map[chan struct{}]struct{}{}}
	s.operationStreamHub = hub
	go func() {
		notifications, close, err := s.notif.Subscribe(ctx, []string{"customer_operation_changed"})
		if err != nil {
			return
		}
		defer close()
		for {
			select {
			case <-ctx.Done():
				return
			case notification, ok := <-notifications:
				if !ok {
					return
				}
				hub.mu.Lock()
				for watcher := range hub.watchers[notification.Payload] {
					select {
					case watcher <- struct{}{}:
					default:
					}
				}
				hub.mu.Unlock()
			}
		}
	}()
}

func (h *operationStreamHub) watch(id string) (<-chan struct{}, func()) {
	if h == nil {
		return nil, func() {}
	}
	watcher := make(chan struct{}, 1)
	h.mu.Lock()
	if h.watchers[id] == nil {
		h.watchers[id] = map[chan struct{}]struct{}{}
	}
	h.watchers[id][watcher] = struct{}{}
	h.mu.Unlock()
	return watcher, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.watchers[id], watcher)
		if len(h.watchers[id]) == 0 {
			delete(h.watchers, id)
		}
	}
}

func (s *server) operationStreamAuthorized(ctx context.Context, bearer string, op state.Operation) bool {
	store, ok := s.store.(state.PlatformTenantAccessStore)
	if !ok {
		return false
	}
	acct, token, err := store.AuthenticatePlatformTenantAccessToken(ctx, api.HashAPIKey(bearer))
	if err != nil || !acct.Active() || acct.ID != op.AccountID || token.TenantID != op.PlatformTenantID {
		return false
	}
	for _, scope := range token.Scopes {
		if scope == api.ScopePlatformTenantOperationsRead {
			return true
		}
	}
	return false
}

func writeOperationFrame(w http.ResponseWriter, event string, sequence int64, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(api.OperationStreamWriteTimeout))
	if sequence > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", sequence); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return err
	}
	return controller.Flush()
}

func (s *server) streamOperationEvents(w http.ResponseWriter, r *http.Request, op state.Operation, store state.OperationStore, after int64) {
	streams, ok := s.store.(state.OperationStreamStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation streaming is unavailable"))
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		api.WriteProblem(w, api.ErrCapacity("operation streaming transport is unavailable"))
		return
	}
	lease, err := streams.AcquireOperationStream(r.Context(), op.AccountID, op.PlatformTenantID, op.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	defer func(requestCtx context.Context) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(requestCtx), api.OperationStreamReleaseTimeout)
		defer cancel()
		_ = streams.ReleaseOperationStream(ctx, lease)
	}(r.Context())
	bearer := operationBearer(r)
	if !s.operationStreamAuthorized(r.Context(), bearer, op) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Operation credential expired", "refresh the customer credential before subscribing"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Vary", "Authorization, Accept")
	wake, unwatch := s.operationStreamHub.watch(op.ID)
	defer unwatch()
	s.streamOperationLoop(w, r, op, store, streams, lease, bearer, after, wake)
}

func (s *server) streamOperationLoop(w http.ResponseWriter, r *http.Request, op state.Operation, store state.OperationStore, streams state.OperationStreamStore, lease, bearer string, after int64, wake <-chan struct{}) {
	tick := time.NewTicker(api.OperationStreamAuthInterval)
	defer tick.Stop()
	until := time.NewTimer(api.OperationStreamMaxDuration)
	defer until.Stop()
	nextRead, nextRenew := time.Time{}, time.Now().Add(api.OperationStreamRenewInterval)
	var snapshot []byte
	for {
		now := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), api.OperationStreamReleaseTimeout)
		allowed := s.operationStreamAuthorized(ctx, bearer, op)
		cancel()
		if !allowed {
			_ = writeOperationFrame(w, "auth_expired", 0, map[string]string{"code": "operation_credential_expired"})
			return
		}
		if !now.Before(nextRenew) {
			if err := streams.RenewOperationStream(r.Context(), op.AccountID, lease); err != nil {
				return
			}
			nextRenew = now.Add(api.OperationStreamRenewInterval)
		}
		if !now.Before(nextRead) {
			page, err := store.OperationEvents(r.Context(), op.AccountID, op.PlatformTenantID, op.ID, after, api.OperationEventsPageMax)
			if err != nil {
				_ = writeOperationFrame(w, "unavailable", 0, map[string]string{"code": "operation_unavailable"})
				return
			}
			current, err := store.OperationByID(r.Context(), op.AccountID, op.PlatformTenantID, op.ID)
			if err != nil {
				return
			}
			if page.ResyncRequired {
				if err := writeOperationFrame(w, "resync", current.LatestSequence, current.OperationResponse); err != nil {
					return
				}
				after = current.LatestSequence
			} else {
				for _, event := range page.Events {
					if err := writeOperationFrame(w, "operation", event.Sequence, event); err != nil {
						return
					}
					after = event.Sequence
				}
			}
			if after < page.LatestSequence {
				continue
			}
			encoded, _ := json.Marshal(current.OperationResponse)
			if string(encoded) != string(snapshot) {
				if err := writeOperationFrame(w, "snapshot", 0, current.OperationResponse); err != nil {
					return
				}
				snapshot = encoded
			}
			nextRead = now.Add(api.OperationStreamPollInterval)
			_ = http.NewResponseController(w).SetWriteDeadline(now.Add(api.OperationStreamWriteTimeout))
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-until.C:
			return
		case <-tick.C:
		case <-wake:
			nextRead = time.Time{}
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type realtimeSignalPublisher interface {
	PublishSignal(context.Context, string, string, realtime.EphemeralFrame) error
}

func (o localRealtimeOwner) PublishSignal(ctx context.Context, ep, ch string, frame realtime.EphemeralFrame) error {
	return o.client.BroadcastEphemeral(ctx, ep, ch, frame)
}
func (o *leasedRealtimeOwner) PublishSignal(ctx context.Context, ep, ch string, frame realtime.EphemeralFrame) error {
	return o.RelayEphemeral(ctx, "", ep, ch, frame)
}

type realtimeBackendSignalWindow struct {
	started time.Time
	count   int
}
type realtimeBackendSignalLimiter struct {
	mu      sync.Mutex
	windows map[[2]string]realtimeBackendSignalWindow
}

// Fixed windows are process-local. Bound memory without evicting active buckets.
func (l *realtimeBackendSignalLimiter) allow(ep, ch string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = make(map[[2]string]realtimeBackendSignalWindow)
	}
	key := [2]string{ep, ch}
	window, exists := l.windows[key]
	if !exists && len(l.windows) >= 4096 {
		for key, value := range l.windows {
			if now.Sub(value.started) >= time.Second {
				delete(l.windows, key)
			}
		}
		if len(l.windows) >= 4096 {
			return false
		}
	}
	if window.started.IsZero() || now.Sub(window.started) >= time.Second {
		window = realtimeBackendSignalWindow{started: now}
	}
	if window.count >= 20 {
		return false
	}
	window.count++
	l.windows[key] = window
	return true
}
func (s *server) publishManagedRealtimeSignal(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, owner, ok := s.managedRealtimeEndpointAction(w, r, acct)
	if !ok {
		return
	}
	ch := r.PathValue("channel")
	if scope := r.PathValue("activity_scope"); scope != "" {
		scoped, err := api.RealtimeActivityScopeChannel(ch, scope)
		if err != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
			return
		}
		ch = scoped
	}
	if problem := validateManagedRealtimeChannel(ch); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var req api.ManagedRealtimeSignalRequest
	if decodeJSONSized(r, &req, 4<<10) != nil || len(req.Data) == 0 || len(req.Data) > realtime.ManagedRealtimeSignalDataMaxBytes || !json.Valid(req.Data) || (ep.MaxMessageBytes > 0 && int64(len(req.Data)) > ep.MaxMessageBytes) {
		api.WriteProblem(w, api.ErrRealtimeInvalid("signal requires JSON data within 2048 bytes and the endpoint payload limit"))
		return
	}
	now := time.Now().UTC()
	frame := realtime.EphemeralFrame{Type: "signal", MemberID: "backend", Data: req.Data, Name: req.Name, UpdatedAt: now}
	if req.Name != "" {
		ttl := 5000
		if req.TTLMS != nil {
			ttl = *req.TTLMS
		}
		if ttl < 0 || ttl > 30000 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("ttl_ms must be from 0 to 30000"))
			return
		}
		expiry := now.Add(time.Duration(ttl) * time.Millisecond)
		frame.ExpiresAt = &expiry
	} else if req.TTLMS != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("ttl_ms requires a signal name"))
		return
	}
	if !realtime.ValidateEphemeralFrame(frame) {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid signal name or data"))
		return
	}
	publisher, ok := owner.(realtimeSignalPublisher)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("backend signals unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !s.realtimeBackendSignals.allow(ep.ID, ch, now) {
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Signal rate limit", "backend signals are limited to 20 per second per endpoint/channel on each API process"))
		return
	}
	if err := publisher.PublishSignal(r.Context(), ep.ID, ch, frame); err != nil {
		s.writeManagedRealtimeOwnerError(w, r, "publish signal", err)
		return
	}
	writeJSON(w, http.StatusOK, api.ManagedRealtimeSignalResponse{Accepted: true, MemberID: "backend", ExpiresAt: frame.ExpiresAt})
}

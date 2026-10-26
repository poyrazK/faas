package vmmdgrpc

import (
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/extension"
)

// invokeHookBackoff remembers the instances whose guest reported that no
// extension endpoint accepted an invoke event (ADR-20261025150000). Every
// invoke event opens a vsock connection into the guest; on prod's nested hosts
// that cost about 4.4 ms of guest CPU and 110 VM exits per request, most of a
// hello-world request's VM time. An instance that answered "no extension"
// gets no invoke events for extension.InvokeUnavailableBackoff, then one
// event probes again. The zero value is ready to use.
type invokeHookBackoff struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// invokeHookBackoffSweepAt bounds the map: past this many entries, recording
// one drops every entry whose backoff has ended. Entries for instances that
// stopped receiving requests are otherwise only removed when vmmd restarts.
const invokeHookBackoffSweepAt = 4096

// allow reports whether an invoke event may be sent to instance at now.
func (b *invokeHookBackoff) allow(instance string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.until[instance]
	if !ok {
		return true
	}
	if now.Before(until) {
		return false
	}
	delete(b.until, instance)
	return true
}

// record notes the outcome of one invoke event. Only the guest's explicit
// "no extension endpoint" answer starts a backoff; a transport failure or an
// extension that broke the contract keeps events flowing, as before.
func (b *invokeHookBackoff) record(instance string, err error, now time.Time) {
	if instance == "" || !errors.Is(err, extension.ErrUnavailable) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.until == nil {
		b.until = make(map[string]time.Time)
	}
	if len(b.until) >= invokeHookBackoffSweepAt {
		for k, u := range b.until {
			if !now.Before(u) {
				delete(b.until, k)
			}
		}
	}
	b.until[instance] = now.Add(extension.InvokeUnavailableBackoff)
}

// invokeHookAllowed reports whether this request may send an invoke event.
// With the backoff switched off every request sends one, as before.
func (s *Server) invokeHookAllowed(instance string, now time.Time) bool {
	return !s.invokeHookBackoffOn || s.invokeHooks.allow(instance, now)
}

// recordInvokeHook notes an invoke event's outcome while the backoff is on.
func (s *Server) recordInvokeHook(instance string, err error, now time.Time) {
	if s.invokeHookBackoffOn {
		s.invokeHooks.record(instance, err, now)
	}
}

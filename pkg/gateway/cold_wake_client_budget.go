package gateway

import (
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// coldWakeClientBudget limits how many different parked apps one client
// prefix (IPv4 /24, IPv6 /64) may wake through this gateway in a window (bug
// hunt H75). Rate limits are per app, so one anonymous client sending a
// single GET to ~130 public parked apps filled the gateway's wake admission
// queue: every other customer's cold wake got 503 or timed out, and each
// admitted wake held node RAM until its idle timeout. Requests to an app the
// client already woke in the window are not counted again; the WakeGate
// coalesces them. Loopback, private and unidentified callers are platform
// traffic and are not limited.
type coldWakeClientBudget struct {
	mu      sync.Mutex
	now     func() time.Time
	clients map[string]map[string]time.Time // prefix → app → when woken
}

func newColdWakeClientBudget() *coldWakeClientBudget {
	return &coldWakeClientBudget{now: time.Now, clients: map[string]map[string]time.Time{}}
}

// coldWakeClientKey is the prefix a cold wake is charged to, or "" when the
// caller is exempt.
func coldWakeClientKey(r *http.Request) string {
	ip, ok := TrustedClientIP(r)
	if !ok || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return ""
	}
	return publicAuthClientPrefix(r)
}

// allow reports whether client may wake appID now, recording the wake when it
// may. A refused client is told how long until its oldest wake ages out.
func (b *coldWakeClientBudget) allow(client, appID string) (bool, time.Duration) {
	if b == nil || client == "" {
		return true, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	apps := b.clients[client]
	var oldest time.Time
	for id, at := range apps {
		if now.Sub(at) >= api.GatewayColdWakeClientWindow {
			delete(apps, id)
			continue
		}
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
	}
	if _, seen := apps[appID]; seen {
		return true, 0
	}
	if len(apps) >= api.GatewayColdWakeAppsPerClient {
		return false, oldest.Add(api.GatewayColdWakeClientWindow).Sub(now)
	}
	if apps == nil {
		if len(b.clients) >= api.GatewayColdWakeClientsTrackedMax {
			b.sweepLocked(now)
		}
		apps = map[string]time.Time{}
		b.clients[client] = apps
	}
	apps[appID] = now
	return true, 0
}

// sweepLocked drops clients with no wake in the window; if none have aged
// out it drops one arbitrary client so the table stays bounded.
func (b *coldWakeClientBudget) sweepLocked(now time.Time) {
	for client, apps := range b.clients {
		live := false
		for _, at := range apps {
			if now.Sub(at) < api.GatewayColdWakeClientWindow {
				live = true
				break
			}
		}
		if !live {
			delete(b.clients, client)
		}
	}
	if len(b.clients) < api.GatewayColdWakeClientsTrackedMax {
		return
	}
	for client := range b.clients {
		delete(b.clients, client)
		return
	}
}

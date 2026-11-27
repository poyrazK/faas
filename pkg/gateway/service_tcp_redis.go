package gateway

import (
	"context"
	"errors"
	"net"
	"sync"
)

// serveRedis runs a managed Redis cache session (ADR-20261116192126). Every
// upstream leg goes through forward, so routing, wake, release pinning and
// the endpoint breaker are the same as for any private TCP session; the
// session decides when a leg opens and closes.
func (p *ServiceTCPProxy) serveRedis(ctx context.Context, conn net.Conn, caller ServiceCaller, target ServiceTCPTarget, deploymentID string, port int) (string, string, bool, error) {
	var (
		mu         sync.Mutex
		lastReason string
		attached   bool
	)
	session := newRedisSession(conn, func(ctx context.Context, leg net.Conn) (bool, error) {
		outcome, reason, woke, err := p.forward(ctx, leg, caller, target, deploymentID, port)
		mu.Lock()
		if reason != "" {
			lastReason = reason
		} else {
			attached = true
		}
		mu.Unlock()
		if reason != "" && err == nil {
			err = errors.New("redis session: " + reason)
		}
		if outcome == "error" && err == nil {
			err = errors.New("redis session: leg failed")
		}
		return woke, err
	})
	woke, err := session.run(ctx)
	mu.Lock()
	defer mu.Unlock()
	if !attached && lastReason != "" {
		// No leg ever reached the cache: report the refusal like a plain
		// TCP session would.
		return "", lastReason, woke, err
	}
	if err != nil {
		return "error", "", woke, err
	}
	return "success", "", woke, nil
}

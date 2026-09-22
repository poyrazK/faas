package tcpd

import (
	"errors"
	"strings"
	"sync"
)

// ErrConnectionLimit indicates that a route's account-scoped TCP session
// quota is full. The accepted socket is closed by Server after this error.
var ErrConnectionLimit = errors.New("TCP connection limit reached")

// ConnectionLimiter enforces a bounded number of concurrent TCP sessions per
// account. The limit is deliberately process-local: each public gateway owns
// only the listeners bound on that host, while the durable listener identity
// keeps the quota key stable across refreshes.
type ConnectionLimiter struct {
	max int

	mu      sync.Mutex
	current map[string]int
}

// NewConnectionLimiter returns nil when max is non-positive, which means the
// caller leaves the quota disabled. A nil limiter is safe to pass through the
// Server and Supervisor seams.
func NewConnectionLimiter(max int) *ConnectionLimiter {
	if max <= 0 {
		return nil
	}
	return &ConnectionLimiter{max: max, current: make(map[string]int)}
}

// Acquire reserves one session for key and returns a release function. Empty
// keys are rejected so a malformed route cannot accidentally share one global
// bucket; Server supplies the app ID fallback before calling this method.
func (l *ConnectionLimiter) Acquire(key string) (release func(), ok bool) {
	if l == nil {
		return func() {}, true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return func() {}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current[key] >= l.max {
		return func() {}, false
	}
	l.current[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if n := l.current[key]; n <= 1 {
				delete(l.current, key)
			} else {
				l.current[key] = n - 1
			}
			l.mu.Unlock()
		})
	}, true
}

// Current returns the number of active reservations for key. It exists for
// tests and lightweight observability without exposing the internal map.
func (l *ConnectionLimiter) Current(key string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.current[strings.TrimSpace(key)]
}

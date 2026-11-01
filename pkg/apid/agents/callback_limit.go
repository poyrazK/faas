package agents

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// callbackLimiter counts one apid's callbacks per thread in fixed one-minute
// windows (api.AgentCallbacksPerMinutePerThread). Each apid counts its own
// posts, so N replicas accept at most N times the limit: the limit stops a
// runaway tool, and a post needs a valid token anyway.
type callbackLimiter struct {
	mu      sync.Mutex
	windows map[string]callbackWindow
}

type callbackWindow struct {
	start time.Time
	count int
}

// callbackLimiterMaxKeys is how many windows the limiter keeps before it
// drops the ones that ended.
const callbackLimiterMaxKeys = 10_000

// allow counts one post for key at now, and reports the window's count and
// whether it is within the limit.
func (l *callbackLimiter) allow(key string, now time.Time) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = make(map[string]callbackWindow)
	}
	w := l.windows[key]
	if now.Sub(w.start) >= time.Minute || now.Before(w.start) {
		if len(l.windows) >= callbackLimiterMaxKeys {
			for k, old := range l.windows {
				if now.Sub(old.start) >= time.Minute {
					delete(l.windows, k)
				}
			}
		}
		w = callbackWindow{start: now}
	}
	w.count++
	l.windows[key] = w
	return w.count, w.count <= api.AgentCallbacksPerMinutePerThread
}

package sched

import (
	"context"
	"sync"
)

// StartupOOMRecorder collects the instances whose wake reported that the app
// ran out of memory while it booted (vmmd WakeResponse.startup_oom). The
// gateway puts one on a request's wake context; the schedd client marks every
// admitted instance whose response carries the flag, so the request can file
// its wake as an app fault instead of platform wake latency without widening
// the admission call signatures. Safe for concurrent use: a burst admission
// reports its instances from several goroutines.
type StartupOOMRecorder struct {
	mu        sync.Mutex
	instances map[string]struct{}
}

type startupOOMRecorderKey struct{}

// WithStartupOOMRecorder returns ctx carrying rec. Contexts derived from it
// (including context.WithoutCancel, which the gateway's detached wake leader
// uses) share rec.
func WithStartupOOMRecorder(ctx context.Context, rec *StartupOOMRecorder) context.Context {
	return context.WithValue(ctx, startupOOMRecorderKey{}, rec)
}

// NoteStartupOOM marks instanceID on ctx's recorder. A no-op without a
// recorder or an instance id.
func NoteStartupOOM(ctx context.Context, instanceID string) {
	rec, _ := ctx.Value(startupOOMRecorderKey{}).(*StartupOOMRecorder)
	if rec == nil || instanceID == "" {
		return
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.instances == nil {
		rec.instances = map[string]struct{}{}
	}
	rec.instances[instanceID] = struct{}{}
}

// Has reports whether instanceID's wake reported a startup OOM. Nil-safe.
func (r *StartupOOMRecorder) Has(instanceID string) bool {
	if r == nil || instanceID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.instances[instanceID]
	return ok
}

// Any reports whether any wake this recorder saw reported a startup OOM.
// Nil-safe.
func (r *StartupOOMRecorder) Any() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.instances) > 0
}

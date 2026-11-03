package profiling

import (
	"bytes"
	"fmt"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
)

// contention collects one of Go's cumulative contention profiles, block or
// mutex, for a capture window (ADR-1155): a baseline when the window starts
// and the difference when it ends, so a capture shows only its own window.
type contention struct {
	kind    string
	base    *pprofProfile
	restore func()
}

// Block profile ownership. Go has no getter for the block profile rate, so
// the first capture decides: a cumulative profile that already holds samples
// means the application enabled block profiling itself and keeps its rate;
// otherwise the SDK samples during each window and turns sampling off after.
const (
	blockOwnerUndecided int32 = iota
	blockOwnerSDK
	blockOwnerApplication
)

var blockOwner atomic.Int32

// startContention takes the window's baseline and enables sampling the
// application left off. A nil result means the profile is unavailable.
func startContention(kind string) *contention {
	base, err := lookupContention(kind)
	if err != nil {
		return nil
	}
	c := &contention{kind: kind, base: base}
	switch kind {
	case kindMutex:
		// A negative fraction reads the current one without changing it.
		if runtime.SetMutexProfileFraction(-1) == 0 {
			runtime.SetMutexProfileFraction(mutexProfileFraction)
			c.restore = func() { runtime.SetMutexProfileFraction(0) }
		}
	case kindBlock:
		owner := blockOwnerSDK
		if len(base.samples) > 0 {
			owner = blockOwnerApplication
		}
		blockOwner.CompareAndSwap(blockOwnerUndecided, owner)
		if blockOwner.Load() == blockOwnerSDK {
			runtime.SetBlockProfileRate(blockRateNanos)
			c.restore = func() { runtime.SetBlockProfileRate(0) }
		}
	}
	return c
}

// finish restores the sampling it changed and, when upload is set, returns
// the gzip pprof difference between the window's end and its baseline.
func (c *contention) finish(upload bool) ([]byte, error) {
	if c.restore != nil {
		defer c.restore()
	}
	if !upload {
		return nil, nil
	}
	final, err := lookupContention(c.kind)
	if err != nil {
		return nil, err
	}
	delta, err := contentionDelta(c.base, final)
	if err != nil {
		return nil, fmt.Errorf("profiling: %s delta: %w", c.kind, err)
	}
	return delta, nil
}

func lookupContention(kind string) (*pprofProfile, error) {
	p := pprof.Lookup(kind)
	if p == nil {
		return nil, fmt.Errorf("profiling: no %s profile", kind)
	}
	var buf bytes.Buffer
	if err := p.WriteTo(&buf, 0); err != nil {
		return nil, err
	}
	return parsePprof(buf.Bytes())
}

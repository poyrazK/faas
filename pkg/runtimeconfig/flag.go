package runtimeconfig

import "sync/atomic"

// BoolFlag is a process-local, lock-free boolean used by request and timer
// hot paths. The watcher owns Store; consumers call Load for every decision.
type BoolFlag struct {
	value atomic.Bool
}

func NewBoolFlag(initial bool) *BoolFlag {
	f := &BoolFlag{}
	f.value.Store(initial)
	return f
}

func (f *BoolFlag) Load() bool {
	if f == nil {
		return false
	}
	return f.value.Load()
}

func (f *BoolFlag) Store(value bool) {
	if f != nil {
		f.value.Store(value)
	}
}

package e2etest

import "sync"

type registryRepositoryHold struct {
	entered     chan struct{}
	done        chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func (h *registryRepositoryHold) release() { h.releaseOnce.Do(func() { close(h.done) }) }

// HoldRepository pauses real image requests at the registry boundary. The
// entered channel proves that imaged reached the pull before a test crashes
// its controller or supersedes the pending revision. Requests remain bound to
// their cancellation contexts; Close also releases every outstanding hold.
func (f *FakeRegistry) HoldRepository(repo string) (<-chan struct{}, func()) {
	hold := &registryRepositoryHold{entered: make(chan struct{}), done: make(chan struct{})}
	f.mu.Lock()
	if f.holds == nil {
		f.holds = map[string]*registryRepositoryHold{}
	}
	if previous := f.holds[repo]; previous != nil {
		previous.release()
	}
	f.holds[repo] = hold
	f.mu.Unlock()
	return hold.entered, hold.release
}

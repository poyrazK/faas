package apidserver

import "sync"

// backgroundWork runs best-effort work off the request path with a cap on
// how much runs at once. A public route whose work differs by whether an
// account exists (a password reset mails only a real account) hands that work
// here, so the response returns after the same steps either way (bug hunt
// H86). Past the cap work is dropped, not queued: the caller already answered.
type backgroundWork struct {
	slots chan struct{}
	wg    sync.WaitGroup
}

func newBackgroundWork(limit int) *backgroundWork {
	return &backgroundWork{slots: make(chan struct{}, limit)}
}

// try starts fn unless the cap is reached and reports whether it started. A
// nil backgroundWork (bare test servers) runs fn inline.
func (b *backgroundWork) try(fn func()) bool {
	if b == nil {
		fn()
		return true
	}
	select {
	case b.slots <- struct{}{}:
	default:
		return false
	}
	b.wg.Add(1)
	go func() {
		defer func() {
			<-b.slots
			b.wg.Done()
		}()
		fn()
	}()
	return true
}

// wait blocks until the started work has finished. Tests use it to observe
// the mail a request caused.
func (b *backgroundWork) wait() {
	if b != nil {
		b.wg.Wait()
	}
}

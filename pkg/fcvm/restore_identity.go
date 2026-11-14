package fcvm

import (
	"errors"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// RestoreIdentityExpectation tracks one identity-reload restore
// (ADR-20261031193635 slice 5b): an environment branch restored from its
// parent's credential-free capture. The runtime-config receiver records the
// digest of the identity document it served to the guest; the resume hook
// accepts the restore only when the guest's after_restore hook acknowledged
// that same digest. It is one-shot: once the restore's resume hook has been
// checked, later resumes of the same VM (thaw, quiet pause) carry no reload.
type RestoreIdentityExpectation struct {
	mu       sync.Mutex
	served   string
	consumed bool
}

// ErrRestoreIdentityNotExpected refuses an identity request from an instance
// that is not being restored with an identity reload.
var ErrRestoreIdentityNotExpected = errors.New("fcvm: instance is not restoring with an identity reload")

func newRestoreIdentityExpectation() *RestoreIdentityExpectation {
	return &RestoreIdentityExpectation{}
}

// pending reports whether the restore still awaits its identity reload.
func (e *RestoreIdentityExpectation) pending() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.consumed
}

func (e *RestoreIdentityExpectation) serve(digest string) error {
	if !api.ValidRestoreIdentityDigest(digest) {
		return fmt.Errorf("fcvm: invalid restore identity digest")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.consumed {
		return ErrRestoreIdentityNotExpected
	}
	e.served = digest
	return nil
}

// acknowledge consumes the expectation and reports whether the guest
// acknowledged exactly the identity vmmd served.
func (e *RestoreIdentityExpectation) acknowledge(digest string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	served := e.served
	e.consumed = true
	return served != "" && digest == served
}

// RestoreIdentityPending reports whether instance is in an identity-reload
// restore that has not been checked yet.
func (m *Manager) RestoreIdentityPending(instance string) bool {
	m.mu.Lock()
	w, ok := m.waking[instance]
	m.mu.Unlock()
	return ok && w.restoreIdentity.pending()
}

// ServeRestoreIdentity records the digest of the identity document served
// to instance. It refuses an instance that is not restoring with an identity
// reload, so the identity request kind is no general secret channel.
func (m *Manager) ServeRestoreIdentity(instance, digest string) error {
	m.mu.Lock()
	w, ok := m.waking[instance]
	m.mu.Unlock()
	if !ok || !w.restoreIdentity.pending() {
		return ErrRestoreIdentityNotExpected
	}
	return w.restoreIdentity.serve(digest)
}

// checkRestoreIdentityAck is the resume hook's identity check. A restore
// without an expectation, or one already checked, passes; otherwise the
// guest must echo the served digest, and anything else is the application's
// after_restore failing, which cold-boots the branch.
func checkRestoreIdentityAck(e *RestoreIdentityExpectation, guestDigest string) error {
	if !e.pending() {
		return nil
	}
	if !e.acknowledge(guestDigest) {
		return fmt.Errorf("vmm: %w: restore identity not acknowledged", ErrAfterRestoreHook)
	}
	return nil
}

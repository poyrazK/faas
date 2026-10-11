package ingress

import "github.com/google/uuid"

type Withdrawal struct {
	ID, FenceID   string
	Version       int64
	Closed, Known bool
	Active        int
}

// Withdraw atomically closes begin and late binding, without interrupting any
// existing forward. The fence is irreversible for this process startup session.
func (t *ActivityTracker) Withdraw(id string) (Withdrawal, error) {
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil || u.String() != id {
		return Withdrawal{}, ErrUnverified
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil || (t.withdrawal != "" && t.withdrawal != id) {
		return Withdrawal{}, ErrUnverified
	}
	if t.withdrawal == "" {
		t.withdrawal, t.fence = id, uuid.NewString()
		t.advance()
	}
	return Withdrawal{ID: t.withdrawal, FenceID: t.fence, Version: t.version, Closed: true, Known: t.known, Active: t.total}, nil
}

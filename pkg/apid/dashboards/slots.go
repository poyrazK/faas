package dashboards

import (
	"errors"
	"sync"
)

// ErrDataBusy refuses a dashboard data read while the account already has
// its share of reads in flight (ADR-20261104142830).
var ErrDataBusy = errors.New("dashboards: too many dashboard reads in flight for this account")

// ReadSlots bounds one account's dashboard data reads in flight. A read
// fans out to every widget, each a handful of Prometheus queries over up to
// 7 days, and nothing else bounds how many an account runs at once against
// the Prometheus that alert evaluation and SLO budgets also read. A nil
// *ReadSlots does not limit.
type ReadSlots struct {
	perAccount int

	mu       sync.Mutex
	inFlight map[string]int
}

// NewReadSlots allows perAccount reads in flight per account.
func NewReadSlots(perAccount int) *ReadSlots {
	return &ReadSlots{perAccount: perAccount, inFlight: map[string]int{}}
}

// acquire takes a slot for account and returns its release, or ErrDataBusy.
func (s *ReadSlots) acquire(account string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[account] >= s.perAccount {
		return nil, ErrDataBusy
	}
	s.inFlight[account]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.inFlight[account]--; s.inFlight[account] <= 0 {
			delete(s.inFlight, account)
		}
	}, nil
}

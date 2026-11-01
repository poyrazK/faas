package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// logStreamSlots counts each account's open log streams (live and archive)
// on this gateway against the plan's LogStreamsMax. Nothing bounded them:
// each follow stream polls the shared database from schedd several times a
// second for up to the backstop, so one account could load it for everyone.
type logStreamSlots struct {
	mu   sync.Mutex
	open map[string]int
}

func newLogStreamSlots() *logStreamSlots {
	return &logStreamSlots{open: make(map[string]int)}
}

// admit takes a slot for the account, or writes the 429 limit problem and
// returns false. A nil logStreamSlots admits every stream (tests).
func (s *logStreamSlots) admit(w http.ResponseWriter, acct state.Account) (release func(), ok bool) {
	if s == nil {
		return func() {}, true
	}
	max := acct.Plan.LogStreamsMax()
	s.mu.Lock()
	open := s.open[acct.ID]
	if open >= max {
		s.mu.Unlock()
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "log_streams_limit", "Too many log streams",
			fmt.Sprintf("This account already has %d log streams open on this gateway, the %s plan's limit; close one or retry later.", open, acct.Plan)).
			WithLimit(int64(max), int64(open)).WithDocs("https://gregale.dev/docs/logs#limits"))
		return nil, false
	}
	s.open[acct.ID] = open + 1
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if n := s.open[acct.ID] - 1; n > 0 {
				s.open[acct.ID] = n
			} else {
				delete(s.open, acct.ID)
			}
		})
	}, true
}

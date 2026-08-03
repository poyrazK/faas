// safeBuffer — sync.Mutex-guarded io.Writer for whitebox tests.
// The receive-pump handler writes from a separate goroutine while
// the test reads the captured body; a bare *bytes.Buffer races
// under -race. Same pattern as cmd/apid/handlers_github_test.go
// (memory "e2etest safeBuffer").
package main

import (
	"strings"
	"sync"
)

// safeBuffer is used by the whitebox tests for the AppLogsHandler
// receive pump. The test goroutine reads `String()` while the
// handler goroutine writes to `Write`; the mutex keeps the pair
// race-clean.
type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

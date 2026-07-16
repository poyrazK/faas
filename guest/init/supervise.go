package main

import "fmt"

// Supervisor runs the customer app and restarts it on crash up to Max times,
// then gives up so the VM exits (spec §4.8). The process start is injected so the
// restart policy is unit-tested without spawning anything.
type Supervisor struct {
	Max     int                          // max restarts after the initial start
	Start   func() error                 // runs the app to completion; nil = clean exit
	OnCrash func(attempt int, err error) // optional hook for logging/backoff
}

// Run starts the app and supervises it. It returns nil if the app ever exits
// cleanly, or the last error once the restart budget is exhausted.
func (s Supervisor) Run() error {
	restarts := 0
	for {
		err := s.Start()
		if err == nil {
			return nil // clean exit; nothing to supervise
		}
		if restarts >= s.Max {
			return fmt.Errorf("app crash-looped after %d restart(s): %w", restarts, err)
		}
		restarts++
		if s.OnCrash != nil {
			s.OnCrash(restarts, err)
		}
	}
}

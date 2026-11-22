package vmmdgrpc

import "time"

// WithSandboxUsageEvery shortens how often RunSandbox reports a session's
// usage, for tests.
func (s *Server) WithSandboxUsageEvery(d time.Duration) *Server {
	s.sandboxUsageEvery = d
	return s
}

// WithSandboxReattachGrace shortens how long a detached session VM waits
// for a reattach, for tests.
func (s *Server) WithSandboxReattachGrace(d time.Duration) *Server {
	s.sandboxReattachGrace = d
	return s
}

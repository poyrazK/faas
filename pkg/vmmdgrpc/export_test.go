package vmmdgrpc

import "time"

// WithSandboxUsageEvery shortens how often RunSandbox reports a session's
// usage, for tests.
func (s *Server) WithSandboxUsageEvery(d time.Duration) *Server {
	s.sandboxUsageEvery = d
	return s
}

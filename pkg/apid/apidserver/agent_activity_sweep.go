package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
)

// runAgentActivitySweep keeps agent activity for 7 days (ADR-20261107220000 §2).
func (s *server) runAgentActivitySweep(ctx context.Context) {
	agentconnections.RunActivitySweep(ctx, s.store, s.log, time.Hour)
}

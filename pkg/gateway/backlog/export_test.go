package backlog

import "github.com/prometheus/client_golang/prometheus"

// Refused exposes the refusal counter to the external tests.
func (c *Cap) Refused() prometheus.Counter { return c.refused }

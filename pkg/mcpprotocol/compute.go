package mcpprotocol

import (
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

// ToolCompute is a tool's estimated compute over the performance window
// (ADR-20261122194324): execution time at the app's billed memory (plan RAM
// + 8 MB), priced at the overage rate. Calls that overlap on one instance
// share its memory, so this is an upper bound; idle time before parking goes
// to no tool. Included GB-hours are not subtracted.
type ToolCompute struct {
	GBSeconds  float64
	Millicents int64
}

// EstimateToolCompute converts execution seconds at memoryMB of plan RAM.
func EstimateToolCompute(executionSeconds float64, memoryMB int) ToolCompute {
	if !finite(executionSeconds) || executionSeconds <= 0 || memoryMB <= 0 {
		return ToolCompute{}
	}
	// Integer arithmetic from whole milliseconds: GB-milliseconds × rate.
	ms := int64(math.Round(executionSeconds * 1000))
	billedMB := int64(memoryMB + 8)
	mbMS := ms * billedMB
	millicents := (mbMS*api.OverageMillicentsPerGBHour + 1024*3_600_000/2) / (1024 * 3_600_000)
	return ToolCompute{GBSeconds: float64(mbMS) / 1024 / 1000, Millicents: millicents}
}

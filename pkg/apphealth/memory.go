// adr: 20261102124306
package apphealth

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// MemoryEvidence is an app's memory-pressure rollup, kept by schedd from the
// guests' reports. Known is false when the read failed; Recorded is false
// when the app has never reported.
type MemoryEvidence struct {
	Known    bool
	Recorded bool
	Pressure state.AppMemoryPressure
}

type memoryPressureReader interface {
	AppMemoryPressure(context.Context, string) (state.AppMemoryPressure, bool, error)
}

func collectMemory(ctx context.Context, store EvidenceStore, e *Evidence) {
	reader, ok := store.(memoryPressureReader)
	if !ok {
		return
	}
	p, recorded, err := reader.AppMemoryPressure(ctx, e.App.ID)
	if err != nil {
		return
	}
	e.Memory = MemoryEvidence{Known: true, Recorded: recorded, Pressure: p}
}

// assessMemory warns when the app's guests ran short of memory within the
// rollup window, before the kernel's OOM killer would have to act.
func assessMemory(out *api.AppHealthResponse, e Evidence) {
	if !e.AssessMemory {
		return
	}
	m, p := e.Memory, e.Memory.Pressure
	switch {
	case !m.Known:
		add(out, "memory", Unknown, "Memory-pressure evidence is unavailable.", "metrics", "")
	case !m.Recorded || e.Now.Sub(p.LastAt) > api.AppHealthMemoryPressureWindow:
		add(out, "memory", Pass, fmt.Sprintf("Available memory has not fallen to %d%% of the guest's memory in the last day.",
			api.ProfileMemoryPressureAvailablePercent), "", "")
	default:
		percent := 0
		if p.TotalMB > 0 {
			percent = p.LowestAvailableMB * 100 / p.TotalMB
		}
		hours := max(1, int(math.Ceil(e.Now.Sub(p.WindowStartedAt).Hours())))
		add(out, "memory", Warning, fmt.Sprintf("Available memory fell to %d of %d MB (%d%%) %s in the last %s; anonymous memory peaked at %d MB.",
			p.LowestAvailableMB, p.TotalMB, percent, times(p.Episodes), spoken(time.Duration(hours)*time.Hour), p.PeakAnonMB), "metrics", "")
	}
}

func times(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	default:
		return fmt.Sprintf("%d times", n)
	}
}

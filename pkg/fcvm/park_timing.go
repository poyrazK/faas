package fcvm

import (
	"log/slog"
	"time"
)

// parkSlowLogThreshold is how long a park capture may take before its phase
// timing is logged. Successful 512 MB captures on production took 15–37 s and
// failed ones ran into schedd's 45 s budget with vmmd logging nothing at all,
// so the slow step could not be named (prod hunt r72).
const parkSlowLogThreshold = 10 * time.Second

// parkPhases records how long each step of a park capture took.
type parkPhases struct {
	start time.Time
	last  time.Time
	names []string
	ms    []int64
}

func newParkPhases(now time.Time) *parkPhases {
	return &parkPhases{start: now, last: now}
}

// mark closes the phase that ran since the previous mark.
func (p *parkPhases) mark(name string, now time.Time) {
	p.names = append(p.names, name)
	p.ms = append(p.ms, now.Sub(p.last).Milliseconds())
	p.last = now
}

// attrs returns the phase durations, the total and the phase reached.
func (p *parkPhases) attrs(now time.Time) []any {
	out := make([]any, 0, 2*len(p.names)+4)
	for i, name := range p.names {
		out = append(out, name+"_ms", p.ms[i])
	}
	reached := "begin"
	if len(p.names) > 0 {
		reached = p.names[len(p.names)-1]
	}
	return append(out, "total_ms", now.Sub(p.start).Milliseconds(), "last_phase", reached)
}

// report logs a failed or slow capture with its phases.
func (p *parkPhases) report(log *slog.Logger, instance string, err error, now time.Time) {
	if log == nil || (err == nil && now.Sub(p.start) < parkSlowLogThreshold) {
		return
	}
	attrs := append([]any{"instance", instance, "err", err}, p.attrs(now)...)
	log.Warn("park: capture timing", attrs...)
}

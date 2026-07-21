package meter

import "time"

// StaleAfterMultiplier is the multiplier applied to each tick's interval
// when deciding whether /healthz should report stale. Spec §14 M7:
// "meterd healthy iff sampled within 3 minutes" — with the production
// SampleInterval = 60 s, 3 × SampleInterval = 3 minutes. Quota (3 min),
// stripe (3 h), and dunning (3 h) scale the same way; each tick has its
// own threshold so a slow quota sweep can never mask a hung sample.
//
// Bumping this is a deliberate operator decision (e.g., during a known
// Postgres maintenance window), not a follow-up. Mirrors the schedd /
// vmmd / builderd pattern of "loopback /healthz is operator-owned".
const StaleAfterMultiplier = 3

// HealthStatus is the JSON shape /healthz emits. Healthy==true ⇒ 200;
// Healthy==false ⇒ 503. Stale lists the tick names whose last fire is
// older than StaleAfterMultiplier × interval; Ticks maps every tracked
// tick to either its last-fire wall clock (RFC 3339 UTC) or "never".
// Operators get the verdict + the diagnostics in one GET.
type HealthStatus struct {
	Healthy bool              `json:"healthy"`
	Stale   []string          `json:"stale,omitempty"`
	Ticks   map[string]string `json:"ticks"`
}

// Health computes the current health verdict. now is the wall clock to
// evaluate against — the daemon's clock. Injected so tests don't depend
// on system time. Always returns a fully-populated Ticks map (one entry
// per actively-running timer) so the JSON body is a stable shape for
// dashboards.
//
// Semantics: a tick is "stale" when it has never fired, or when
// now − lastFire > StaleAfterMultiplier × interval. Any single stale
// tick flips Healthy to false. The first-tick warm-up caveat:
// meterd reports 503 from boot until the first sample tick (default
// 60 s), which systemd's watchdog treats as a slow start — documented
// on cmd/meterd/main.go::/healthz. Keep the duplicate tick names here
// in lockstep with runTicks / runQuotaTicks literal "name" arguments.
//
// Loop.Run only wires dunning when l.dunning != nil (loop.go:64-69);
// reflect that here so a test (or production misconfig) running without
// a dunning timer doesn't permanently report `dunning` in Stale.
func (l *Loop) Health(now time.Time) HealthStatus {
	intervals := map[string]time.Duration{
		"sample": l.cfg.SampleInterval,
		"quota":  l.cfg.QuotaInterval,
		"stripe": l.cfg.StripeInterval,
	}
	if l.dunning != nil {
		intervals["dunning"] = l.cfg.DunningInterval
	}
	s := HealthStatus{Ticks: make(map[string]string, len(intervals))}
	for name, interval := range intervals {
		threshold := StaleAfterMultiplier * interval
		last, ok := l.LastTick(name)
		if !ok {
			s.Ticks[name] = "never"
			s.Stale = append(s.Stale, name)
			continue
		}
		s.Ticks[name] = last.UTC().Format(time.RFC3339)
		if now.Sub(last) > threshold {
			s.Stale = append(s.Stale, name)
		}
	}
	s.Healthy = len(s.Stale) == 0
	return s
}

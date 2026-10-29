package meter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// QuietPauseMode is meterd's quiet_pause_unbilled switch
// (ADR-20261023171905): whether the quiet-paused seconds the pause ledger
// proves are subtracted from billed residency.
type QuietPauseMode string

const (
	// QuietPauseOff bills residency as before and reads no ledger.
	QuietPauseOff QuietPauseMode = "off"
	// QuietPauseShadow reads the ledger and counts what it would subtract,
	// but bills as before.
	QuietPauseShadow QuietPauseMode = "shadow"
	// QuietPauseOn subtracts the proven paused seconds.
	QuietPauseOn QuietPauseMode = "on"
)

// ParseQuietPauseMode reads a quiet_pause_unbilled value; empty is off.
func ParseQuietPauseMode(s string) (QuietPauseMode, error) {
	switch mode := QuietPauseMode(s); mode {
	case "":
		return QuietPauseOff, nil
	case QuietPauseOff, QuietPauseShadow, QuietPauseOn:
		return mode, nil
	}
	return QuietPauseOff, fmt.Errorf("meter: quiet_pause_unbilled must be off, shadow or on (got %q)", s)
}

// quietPauseMetrics owns meterd_quiet_pause_unbilled_seconds_total and
// meterd_quiet_pause_unproven_minutes_total. A nil value records nothing.
type quietPauseMetrics struct {
	seconds  *prometheus.CounterVec
	unproven *prometheus.CounterVec
}

func newQuietPauseMetrics(reg prometheus.Registerer) *quietPauseMetrics {
	m := &quietPauseMetrics{
		seconds: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meterd_quiet_pause_unbilled_seconds_total",
			Help: "Quiet-paused seconds the pause ledger proved, by mode: subtracted from billed residency (on) or only counted (shadow). ADR-20261023171905.",
		}, []string{"mode", "plan"}),
		unproven: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meterd_quiet_pause_unproven_minutes_total",
			Help: "Instance-minutes with an open quiet pause the ledger's watermark had not covered when meterd rolled the minute; the uncovered part was billed.",
		}, []string{"mode"}),
	}
	for _, mode := range []QuietPauseMode{QuietPauseShadow, QuietPauseOn} {
		m.unproven.WithLabelValues(string(mode))
		for _, plan := range api.Plans {
			m.seconds.WithLabelValues(string(mode), string(plan))
		}
	}
	if reg != nil {
		reg.MustRegister(m.seconds, m.unproven)
	}
	return m
}

func (m *quietPauseMetrics) observe(mode QuietPauseMode, plan string, seconds int64, unproven bool) {
	if m == nil {
		return
	}
	if seconds > 0 {
		m.seconds.WithLabelValues(string(mode), plan).Add(float64(seconds))
	}
	if unproven {
		m.unproven.WithLabelValues(string(mode)).Inc()
	}
}

// WithQuietPause sets the quiet_pause_unbilled mode. Outside off, every
// exact-residency minute reads the store's pause ledger; a store without one
// leaves billing unchanged.
func (s *Sampler) WithQuietPause(mode QuietPauseMode, reg prometheus.Registerer) *Sampler {
	s.quietPause = mode
	if mode != QuietPauseOff && mode != "" {
		s.quietMetrics = newQuietPauseMetrics(reg)
	}
	return s
}

// quietPausedSeconds reads the pause ledger for one minute, or nil when the
// mode is off or the store keeps no ledger.
func (s *Sampler) quietPausedSeconds(ctx context.Context, minute time.Time) (map[string]state.QuietPausedSeconds, error) {
	if s.quietPause != QuietPauseShadow && s.quietPause != QuietPauseOn {
		return nil, nil
	}
	ledger, ok := s.store.(state.QuietPauseLedgerStore)
	if !ok {
		return nil, nil
	}
	paused, err := ledger.QuietPausedSeconds(ctx, minute, minute.Add(time.Minute))
	if err != nil {
		return nil, fmt.Errorf("meter: load quiet pause ledger: %w", err)
	}
	return paused, nil
}

// WithQuietPauseRelease decides, in on mode, which accounts the paused
// seconds are subtracted for: the quiet_pause_unbilled platform flag, so the
// rollout can release unbilled pause to internal accounts first. Without it
// on mode subtracts for every account.
func (s *Sampler) WithQuietPauseRelease(released func(platformflags.Subject) bool) *Sampler {
	s.quietRelease = released
	return s
}

// quietPauseModeFor is the mode that applies to an app's minutes: on only
// for an account the release decides is in, shadow for the others.
func (s *Sampler) quietPauseModeFor(app state.App, plan api.Plan) QuietPauseMode {
	if s.quietPause != QuietPauseOn || s.quietRelease == nil {
		return s.quietPause
	}
	if s.quietRelease(platformflags.Subject{AccountID: app.AccountID, Plan: string(plan), OrgID: app.OrgID}) {
		return QuietPauseOn
	}
	return QuietPauseShadow
}

// unbilledQuietSeconds applies one instance-minute's proven pause to its
// resident seconds: it records the pause under mode and, when mode is on,
// returns the seconds left to bill.
func (s *Sampler) unbilledQuietSeconds(paused map[string]state.QuietPausedSeconds, mode QuietPauseMode, instanceID, plan string, seconds int64) int64 {
	p, ok := paused[instanceID]
	if !ok {
		return seconds
	}
	sub := min(max(p.Seconds, 0), seconds)
	s.quietMetrics.observe(mode, plan, sub, p.Unproven)
	if mode == QuietPauseOn {
		return seconds - sub
	}
	return seconds
}

// QuietPauseRetentionOnce deletes pauses older than
// api.QuietPauseLedgerRetentionDays in RetentionBatchSize batches, at most
// MaxRetentionBatches per call.
func QuietPauseRetentionOnce(ctx context.Context, ledger state.QuietPauseLedgerStore, now time.Time) (int64, error) {
	cutoff := now.AddDate(0, 0, -api.QuietPauseLedgerRetentionDays)
	var total int64
	for range MaxRetentionBatches {
		n, err := ledger.DeleteQuietPausesBefore(ctx, cutoff, RetentionBatchSize)
		total += n
		if err != nil {
			return total, fmt.Errorf("quiet pause retention (deleted so far %d): %w", total, err)
		}
		if n < RetentionBatchSize {
			return total, nil
		}
	}
	return total, ErrRetentionBatchCap
}

// QuietPauseRetentionLoop runs QuietPauseRetentionOnce every interval until
// ctx is done.
func QuietPauseRetentionLoop(ctx context.Context, ledger state.QuietPauseLedgerStore, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultRetentionInterval
	}
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n, err := QuietPauseRetentionOnce(ctx, ledger, now); err != nil {
				log.Warn("quiet pause retention tick", "rows_deleted", n, "err", err)
			} else if n > 0 {
				log.Info("quiet pause retention tick ok", "rows_deleted", n)
			}
		}
	}
}

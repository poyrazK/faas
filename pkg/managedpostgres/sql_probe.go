package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
)

// SQL availability probe (ADR-20261029172812). Provider health (ADR-463) reads
// metadata only, so it cannot show that a database accepts connections and
// writes. apid connects to one operator-owned canary database per backend on
// a fixed interval, writes a row and reads it back, and exports the outcome.
// It never touches a customer database: a connection would wake a suspended
// compute and bill the customer for it.

// SQLProbeConfigEnv names the probe's JSON configuration. Unset disables it.
const SQLProbeConfigEnv = "FAAS_MANAGED_POSTGRES_SQL_PROBE_CONFIG"

// SQL probe outcomes, a closed set for metric labels.
const (
	SQLProbeOK            = "ok"
	SQLProbeConnectFailed = "connect_failed"
	SQLProbeWriteFailed   = "write_failed"
	SQLProbeReadFailed    = "read_failed"
	SQLProbeTimeout       = "timeout"
)

var sqlProbeOutcomes = []string{SQLProbeOK, SQLProbeConnectFailed, SQLProbeWriteFailed, SQLProbeReadFailed, SQLProbeTimeout}

const (
	defaultSQLProbeInterval = time.Minute
	defaultSQLProbeTimeout  = 10 * time.Second
	sqlProbeCreate          = `CREATE TABLE IF NOT EXISTS gregale_availability_probe (probe text PRIMARY KEY, observed_at timestamptz NOT NULL)`
	sqlProbeWrite           = `INSERT INTO gregale_availability_probe (probe, observed_at) VALUES ('apid', clock_timestamp())
		ON CONFLICT (probe) DO UPDATE SET observed_at = excluded.observed_at RETURNING observed_at`
	sqlProbeRead = `SELECT observed_at FROM gregale_availability_probe WHERE probe = 'apid'`
)

var (
	sqlProbeBackendPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	sqlProbeEnvPattern     = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
)

// SQLProbeConfig is the file SQLProbeConfigEnv names.
type SQLProbeConfig struct {
	IntervalSeconds int64            `json:"interval_seconds,omitempty"`
	TimeoutSeconds  int64            `json:"timeout_seconds,omitempty"`
	Targets         []SQLProbeTarget `json:"targets"`
}

// SQLProbeTarget is one backend's canary database. DSNEnv names the
// environment variable holding its connection string, so the secret stays in
// the sealed environment and out of the file.
type SQLProbeTarget struct {
	Backend string `json:"backend"`
	DSNEnv  string `json:"dsn_env"`
}

type sqlProbeTarget struct {
	backend string
	config  *pgx.ConnConfig
}

// SQLProber probes each target once per interval.
type SQLProber struct {
	targets  []sqlProbeTarget
	interval time.Duration
	timeout  time.Duration
	probe    func(context.Context, *pgx.ConnConfig) string
	metrics  *SQLProbeMetrics
	logger   *slog.Logger
	now      func() time.Time
}

// LoadSQLProber reads SQLProbeConfigEnv. It returns nil when the variable is
// unset, and an error for any invalid file, target or connection string.
func LoadSQLProber(getenv func(string) string, logger *slog.Logger, reg prometheus.Registerer) (*SQLProber, error) {
	path := strings.TrimSpace(getenv(SQLProbeConfigEnv))
	if path == "" {
		return nil, nil
	}
	config, err := readSQLProbeConfig(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SQLProbeConfigEnv, err)
	}
	return newSQLProber(config, getenv, logger, reg)
}

func readSQLProbeConfig(path string) (SQLProbeConfig, error) {
	file, err := os.Open(path) //nolint:forbidigo // operator-owned deployment file
	if err != nil {
		return SQLProbeConfig{}, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var config SQLProbeConfig
	if err := decoder.Decode(&config); err != nil {
		return SQLProbeConfig{}, fmt.Errorf("decode: %w", ErrInvalid)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return SQLProbeConfig{}, fmt.Errorf("trailing data: %w", ErrInvalid)
	}
	return config, nil
}

func newSQLProber(config SQLProbeConfig, getenv func(string) string, logger *slog.Logger, reg prometheus.Registerer) (*SQLProber, error) {
	interval, timeout := defaultSQLProbeInterval, defaultSQLProbeTimeout
	if config.IntervalSeconds != 0 {
		interval = time.Duration(config.IntervalSeconds) * time.Second
	}
	if config.TimeoutSeconds != 0 {
		timeout = time.Duration(config.TimeoutSeconds) * time.Second
	}
	if interval < 30*time.Second || interval > time.Hour || timeout < time.Second || timeout > 30*time.Second || timeout >= interval {
		return nil, fmt.Errorf("interval must be 30–3600 s and timeout 1–30 s, below the interval: %w", ErrInvalid)
	}
	if len(config.Targets) == 0 {
		return nil, fmt.Errorf("no targets: %w", ErrInvalid)
	}
	prober := &SQLProber{interval: interval, timeout: timeout, probe: probeSQL, logger: logger, now: time.Now}
	if prober.logger == nil {
		prober.logger = slog.Default()
	}
	seen := map[string]bool{}
	backends := make([]string, 0, len(config.Targets))
	for _, target := range config.Targets {
		if !sqlProbeBackendPattern.MatchString(target.Backend) || seen[target.Backend] || !sqlProbeEnvPattern.MatchString(target.DSNEnv) {
			return nil, fmt.Errorf("target %q: backend must be a unique backend ID and dsn_env an environment variable name: %w", target.Backend, ErrInvalid)
		}
		seen[target.Backend] = true
		dsn := strings.TrimSpace(getenv(target.DSNEnv))
		if dsn == "" {
			return nil, fmt.Errorf("target %s: %s is empty: %w", target.Backend, target.DSNEnv, ErrInvalid)
		}
		connConfig, err := pgx.ParseConfig(dsn)
		if err != nil {
			// The parse error can quote the connection string; never return it.
			return nil, fmt.Errorf("target %s: %s is not a PostgreSQL connection string: %w", target.Backend, target.DSNEnv, ErrInvalid)
		}
		prober.targets = append(prober.targets, sqlProbeTarget{backend: target.Backend, config: connConfig})
		backends = append(backends, target.Backend)
	}
	metrics, err := NewSQLProbeMetrics(reg, "apid", backends)
	if err != nil {
		return nil, err
	}
	prober.metrics = metrics
	return prober, nil
}

// Run probes every target at once and then once per interval until ctx ends.
func (p *SQLProber) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		p.ProbeOnce(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ProbeOnce probes each target in turn, each within the probe timeout.
func (p *SQLProber) ProbeOnce(ctx context.Context) {
	for _, target := range p.targets {
		if ctx.Err() != nil {
			return
		}
		started := p.now()
		probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
		outcome := p.probe(probeCtx, target.config)
		cancel()
		if ctx.Err() != nil {
			// Shutdown, not an outage.
			return
		}
		finished := p.now()
		p.metrics.observe(target.backend, outcome, finished.Sub(started), finished)
		if outcome != SQLProbeOK {
			p.logger.Warn("managed postgres sql probe failed", "backend", target.backend, "outcome", outcome)
		}
	}
}

// probeSQL opens a new connection, as a customer would, writes the probe row
// and reads it back. Another apid replica may have written a newer row, so
// the read must be no older than this write.
func probeSQL(ctx context.Context, config *pgx.ConnConfig) string {
	conn, err := pgx.ConnectConfig(ctx, config.Copy())
	if err != nil {
		return sqlProbeFailure(ctx, SQLProbeConnectFailed)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	var written time.Time
	err = conn.QueryRow(ctx, sqlProbeWrite).Scan(&written)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		// The canary is new: create the table once.
		if _, err = conn.Exec(ctx, sqlProbeCreate); err == nil {
			err = conn.QueryRow(ctx, sqlProbeWrite).Scan(&written)
		}
	}
	if err != nil {
		return sqlProbeFailure(ctx, SQLProbeWriteFailed)
	}
	var read time.Time
	if err := conn.QueryRow(ctx, sqlProbeRead).Scan(&read); err != nil || read.Before(written) {
		return sqlProbeFailure(ctx, SQLProbeReadFailed)
	}
	return SQLProbeOK
}

func sqlProbeFailure(ctx context.Context, outcome string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return SQLProbeTimeout
	}
	return outcome
}

// SQLProbeMetrics exports the probe. Backend is an operator-chosen backend ID,
// never tenant or resource identity.
type SQLProbeMetrics struct {
	probes      *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	lastSuccess *prometheus.GaugeVec
}

// NewSQLProbeMetrics registers the families under the daemon prefix. A nil
// registerer returns a nil bundle, whose observe does nothing.
func NewSQLProbeMetrics(reg prometheus.Registerer, prefix string, backends []string) (*SQLProbeMetrics, error) {
	if reg == nil {
		return nil, nil
	}
	if prefix == "" {
		return nil, ErrInvalid
	}
	name := prefix + "_managed_postgres_sql_probe_"
	enabled := prometheus.NewGauge(prometheus.GaugeOpts{Name: name + "enabled", Help: "1 when apid probes managed PostgreSQL canary databases with SQL."})
	m := &SQLProbeMetrics{
		probes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: name + "total",
			Help: "Managed PostgreSQL canary SQL probes by backend and closed outcome."}, []string{"backend", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name + "duration_seconds",
			Help:    "Managed PostgreSQL canary SQL probe duration: a new connection, a write and a read.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}}, []string{"backend"}),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name + "last_success_timestamp_seconds",
			Help: "Unix time of the latest successful canary SQL probe; zero before the first."}, []string{"backend"}),
	}
	for _, collector := range []prometheus.Collector{enabled, m.probes, m.duration, m.lastSuccess} {
		if err := reg.Register(collector); err != nil {
			return nil, err
		}
	}
	enabled.Set(1)
	for _, backend := range backends {
		for _, outcome := range sqlProbeOutcomes {
			m.probes.WithLabelValues(backend, outcome).Add(0)
		}
		m.lastSuccess.WithLabelValues(backend).Set(0)
	}
	return m, nil
}

func (m *SQLProbeMetrics) observe(backend, outcome string, took time.Duration, at time.Time) {
	if m == nil {
		return
	}
	m.probes.WithLabelValues(backend, outcome).Inc()
	m.duration.WithLabelValues(backend).Observe(took.Seconds())
	if outcome == SQLProbeOK {
		m.lastSuccess.WithLabelValues(backend).Set(float64(at.Unix()))
	}
}

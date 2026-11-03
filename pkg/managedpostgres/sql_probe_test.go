// adr: 20261029172812 — apid proves each backend's canary database accepts a
// connection, a write and a read, and exports the outcome.
package managedpostgres

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const sqlProbeTestDSN = "postgres://probe:not-a-real-secret@canary.example.net:5432/gregale?sslmode=require"

func TestLoadSQLProberValidatesItsConfiguration(t *testing.T) {
	env := map[string]string{"NEON_EU_PROBE_DSN": sqlProbeTestDSN, "BROKEN_DSN": "postgres://%zz"}
	load := func(t *testing.T, body string) (*SQLProber, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "sql-probe.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadSQLProber(func(key string) string {
			if key == SQLProbeConfigEnv {
				return path
			}
			return env[key]
		}, nil, prometheus.NewRegistry())
	}
	if prober, err := LoadSQLProber(func(string) string { return "" }, nil, prometheus.NewRegistry()); prober != nil || err != nil {
		t.Fatalf("unset config: %v, %v; want the probe off", prober, err)
	}
	prober, err := load(t, `{"interval_seconds":120,"timeout_seconds":5,"targets":[{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"}]}`)
	if err != nil || prober == nil || prober.interval != 2*time.Minute || prober.timeout != 5*time.Second || len(prober.targets) != 1 {
		t.Fatalf("valid config: %+v, %v", prober, err)
	}
	if prober, err := load(t, `{"targets":[{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"}]}`); err != nil || prober.interval != time.Minute || prober.timeout != 10*time.Second {
		t.Fatalf("defaults: %+v, %v", prober, err)
	}
	for name, body := range map[string]string{
		"unknown field":      `{"targets":[{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"}],"surprise":1}`,
		"trailing document":  `{"targets":[{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"}]}{}`,
		"no targets":         `{"targets":[]}`,
		"interval too short": `{"interval_seconds":10,"targets":[{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"}]}`,
		"timeout too long":   `{"timeout_seconds":60,"targets":[{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"}]}`,
		"timeout ≥ interval": `{"interval_seconds":30,"timeout_seconds":30,"targets":[{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"}]}`,
		"bad backend":        `{"targets":[{"backend":"Neon EU","dsn_env":"NEON_EU_PROBE_DSN"}]}`,
		"duplicate backend":  `{"targets":[{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"},{"backend":"neon-eu","dsn_env":"NEON_EU_PROBE_DSN"}]}`,
		"bad env name":       `{"targets":[{"backend":"neon-eu","dsn_env":"neon-eu-dsn"}]}`,
		"unset env":          `{"targets":[{"backend":"neon-eu","dsn_env":"MISSING_DSN"}]}`,
		"unparseable dsn":    `{"targets":[{"backend":"neon-eu","dsn_env":"BROKEN_DSN"}]}`,
	} {
		_, err := load(t, body)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v; want ErrInvalid", name, err)
		}
		if err != nil && (strings.Contains(err.Error(), "not-a-real-secret") || strings.Contains(err.Error(), "%zz")) {
			t.Errorf("%s: the error quotes the connection string: %v", name, err)
		}
	}
}

func TestSQLProberExportsEachOutcome(t *testing.T) {
	connConfig, err := pgx.ParseConfig(sqlProbeTestDSN)
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	metrics, err := NewSQLProbeMetrics(registry, "apid", []string{"neon-eu", "neon-us"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 17, 0, 0, 0, time.UTC)
	outcomes := map[string]string{"neon-eu": SQLProbeOK, "neon-us": SQLProbeWriteFailed}
	prober := &SQLProber{
		targets:  []sqlProbeTarget{{backend: "neon-eu", config: connConfig}, {backend: "neon-us", config: connConfig}},
		interval: time.Minute, timeout: time.Second, metrics: metrics, now: func() time.Time { return now },
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	var seen []string
	prober.probe = func(ctx context.Context, _ *pgx.ConnConfig) string {
		if deadline, ok := ctx.Deadline(); !ok || deadline.After(time.Now().Add(time.Second)) {
			t.Error("a probe ran without the probe timeout")
		}
		backend := []string{"neon-eu", "neon-us"}[len(seen)]
		seen = append(seen, backend)
		return outcomes[backend]
	}
	prober.ProbeOnce(context.Background())
	for _, tc := range []struct {
		backend, outcome string
		want             float64
	}{
		{"neon-eu", SQLProbeOK, 1}, {"neon-eu", SQLProbeWriteFailed, 0},
		{"neon-us", SQLProbeWriteFailed, 1}, {"neon-us", SQLProbeOK, 0}, {"neon-us", SQLProbeTimeout, 0},
	} {
		if got := testutil.ToFloat64(metrics.probes.WithLabelValues(tc.backend, tc.outcome)); got != tc.want {
			t.Errorf("%s %s = %v, want %v", tc.backend, tc.outcome, got, tc.want)
		}
	}
	if got := testutil.ToFloat64(metrics.lastSuccess.WithLabelValues("neon-eu")); got != float64(now.Unix()) {
		t.Errorf("neon-eu last success %v", got)
	}
	if got := testutil.ToFloat64(metrics.lastSuccess.WithLabelValues("neon-us")); got != 0 {
		t.Errorf("a failed probe set the last success: %v", got)
	}

	// Shutdown in the middle of a probe is not an outage.
	cancelled, cancel := context.WithCancel(context.Background())
	prober.probe = func(context.Context, *pgx.ConnConfig) string { cancel(); return SQLProbeTimeout }
	prober.ProbeOnce(cancelled)
	if got := testutil.ToFloat64(metrics.probes.WithLabelValues("neon-eu", SQLProbeTimeout)); got != 0 {
		t.Errorf("a cancelled probe was recorded as a timeout")
	}
}

func TestProbeSQLAgainstPostgres(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	config := pool.Config().ConnConfig.Copy()
	ctx := context.Background()
	for i := range 2 {
		// The first probe creates the table; the second reuses it.
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		outcome := probeSQL(probeCtx, config)
		cancel()
		if outcome != SQLProbeOK {
			t.Fatalf("probe %d on a healthy database = %s", i+1, outcome)
		}
	}
	readOnly := config.Copy()
	readOnly.RuntimeParams["default_transaction_read_only"] = "on"
	if outcome := probeSQL(ctx, readOnly); outcome != SQLProbeWriteFailed {
		t.Fatalf("read-only database = %s, want %s", outcome, SQLProbeWriteFailed)
	}
	unreachable := config.Copy()
	unreachable.Host, unreachable.Port = "127.0.0.1", 1
	unreachable.Fallbacks = nil
	if outcome := probeSQL(ctx, unreachable); outcome != SQLProbeConnectFailed {
		t.Fatalf("unreachable database = %s, want %s", outcome, SQLProbeConnectFailed)
	}
	expired, cancel := context.WithTimeout(ctx, time.Nanosecond)
	defer cancel()
	<-expired.Done()
	if outcome := probeSQL(expired, config); outcome != SQLProbeTimeout {
		t.Fatalf("expired deadline = %s, want %s", outcome, SQLProbeTimeout)
	}
}

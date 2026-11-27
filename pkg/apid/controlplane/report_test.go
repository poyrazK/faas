package controlplane_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/controlplane"
	"github.com/onebox-faas/faas/pkg/promql"
)

var now = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func row(v float64, kv ...string) promql.VectorSample {
	l := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		l[kv[i]] = kv[i+1]
	}
	return promql.VectorSample{Labels: l, Value: v}
}

func one(v float64) []promql.VectorSample { return []promql.VectorSample{row(v)} }

func ago(d time.Duration) float64 { return float64(now.Add(-d).Unix()) }

// healthy mirrors what the production Prometheus returned on 2026-11-26.
func healthy() map[string][]promql.VectorSample {
	return map[string][]promql.VectorSample{
		"pg_connections":         one(123),
		"pg_connection_capacity": one(185),
		"pg_longest_query":       one(0),
		"pg_oldest_transaction":  one(0),
		"pg_idle_in_transaction": one(0),
		"pg_lock_waits":          one(0),
		"pg_deadlocks":           one(0),
		"pg_collector_last_run":  one(ago(40 * time.Second)),
		"pg_collector_errors":    {row(0, "section", "activity"), row(0, "section", "statements")},
		"pg_database_size":       one(3934272535),
		"pg_cache_hit":           one(0.99609),
		"pg_commits":             one(304.2),
		"pg_rollbacks":           one(11.04),
		"pg_temp_bytes":          one(7458264),
		"pg_backends": {
			row(10, "application", "faas-apid", "state", "idle"), row(2, "application", "faas-apid", "state", "active"),
			row(5, "application", "faas-meterd", "state", "idle"), row(0, "application", "gone", "state", "idle"),
		},
		"pg_statement_time":  {row(2.3, "queryid", "1"), row(9.94, "queryid", "2"), row(0, "queryid", "3")},
		"pg_statement_calls": {row(1370.7, "queryid", "2"), row(117.5, "queryid", "1")},
		"pg_statement_info": {
			row(1, "queryid", "2", "query", "with tracking as (select tracked_from from usage_month_totals_tracking) select 1"),
			row(1, "queryid", "1", "query", strings.Repeat("x", 600)),
		},
		"base_backup":            one(ago(7 * time.Hour)),
		"state_backup":           one(ago(6 * time.Hour)),
		"state_backup_bytes":     one(15291),
		"wal_last_archived":      one(ago(50 * time.Second)),
		"offhost_wal_configured": one(1),
		"offhost_wal_last":       one(ago(51 * time.Second)),
		"wal_prune":              one(ago(6 * time.Minute)),
		"wal_archive_oldest":     one(55587),
		"notification_outbox_oldest": {
			row(0, "channel", "app_wake"), row(0, "channel", "deployment_ready"),
		},
		"notification_outbox_pending": {row(0, "channel", "app_wake"), row(0, "channel", "deployment_ready")},
		"webhook_outbox_oldest":       one(0),
		"webhook_outbox_pending":      one(0),
		"pool_conns": {
			row(1, "pool", "apid", "job", "apid", "state", "acquired"), row(11, "pool", "apid", "job", "apid", "state", "idle"),
			row(0, "pool", "apid", "job", "apid", "state", "constructing"),
			row(3, "pool", "schedd", "job", "schedd", "node", "bhs-2.faas", "state", "acquired"),
		},
		"pool_max":          {row(12, "pool", "apid", "job", "apid"), row(16, "pool", "schedd", "job", "schedd", "node", "bhs-2.faas")},
		"pool_canceled":     {row(0, "pool", "apid", "job", "apid")},
		"pool_acquire_wait": {row(0.044, "pool", "apid", "job", "apid")},
		"pool_acquires":     {row(50299, "pool", "apid", "job", "apid")},
		"hub_conns":         {row(1, "hub", "apid", "job", "apid"), row(0, "hub", "apid_background", "job", "apid")},
		"hub_subscribers":   {row(11, "hub", "apid", "job", "apid"), row(0, "hub", "apid_background", "job", "apid")},
		"hub_reconnects":    {row(0, "hub", "apid", "job", "apid")},
		"targets": {
			row(1, "job", "apid", "instance", "127.0.0.1:9091"), row(1, "job", "schedd", "instance", "127.0.0.1:9103"),
		},
		"host_cpus":          one(12),
		"host_load1":         one(0.44),
		"host_mem_total":     one(33534414848),
		"host_mem_available": one(28269838336),
	}
}

func build(s map[string][]promql.VectorSample) api.ObsControlPlaneReport {
	return controlplane.Build(controlplane.Input{Now: now, Samples: controlplane.Samples{ByQuery: s, Source: controlplane.SourceObserved}})
}

func checksByKey(r api.ObsControlPlaneReport) map[string]api.ObsControlPlaneCheck {
	out := map[string]api.ObsControlPlaneCheck{}
	for _, c := range r.Checks {
		out[c.Key] = c
	}
	return out
}

func TestBuildHealthyProduction(t *testing.T) {
	r := build(healthy())
	if r.State != "ok" || r.Totals.Checks != 17 || r.Totals.Critical+r.Totals.Warning+r.Totals.Unknown != 0 {
		for _, c := range r.Checks {
			t.Logf("%s %s %s", c.Key, c.State, c.Detail)
		}
		t.Fatalf("state = %s totals = %+v", r.State, r.Totals)
	}
	c := checksByKey(r)
	if conn := c["connections"]; conn.Value == nil || *conn.Value != 66.5 || conn.Detail != "123 of 185" || conn.Group != "postgres" {
		t.Fatalf("connections = %+v", conn)
	}
	if b := c["base_backup"]; b.At == nil || b.Detail != "7.0 h ago" || b.Group != "backups" {
		t.Fatalf("base backup = %+v", b)
	}
	if s := c["state_backup"]; s.Detail != "6.0 h ago, 15.3 KB" {
		t.Fatalf("state backup = %+v", s)
	}
	if n := c["notification_outbox"]; n.Detail != "0 undelivered" || n.Group != "delivery" {
		t.Fatalf("notification outbox = %+v", n)
	}
	if tg := c["targets"]; tg.Detail != "2 of 2 up" {
		t.Fatalf("targets = %+v", tg)
	}
	p := r.Postgres
	if *p.ClientConnections != 123 || *p.CacheHitPercent != 99.6 || *p.RollbackPercent != 3.5 || *p.CollectorAgeSeconds != 40 {
		t.Fatalf("postgres = %+v", p)
	}
	if len(r.Backends) != 3 || r.Backends[0].Application != "faas-apid" || r.Backends[0].Count != 10 {
		t.Fatalf("backends = %+v (zero groups dropped, largest first)", r.Backends)
	}
	if len(r.Statements) != 2 || r.Statements[0].QueryID != "2" || *r.Statements[0].MeanMs != 7.25 || *r.Statements[0].Calls != 1371 {
		t.Fatalf("statements = %+v", r.Statements)
	}
	if q := []rune(r.Statements[1].Query); len(q) != api.ObsCPStatementQueryMaxChars || q[len(q)-1] != '…' {
		t.Fatalf("long query not cut: %d runes", len(q))
	}
	if len(r.Pools) != 2 || r.Pools[0].Pool != "schedd" || r.Pools[0].Node != "bhs-2.faas" || *r.Pools[0].UtilizationPercent != 18.8 {
		t.Fatalf("pools = %+v (busiest first)", r.Pools)
	}
	if apid := r.Pools[1]; apid.Acquired != 1 || apid.Idle != 11 || *apid.MeanAcquireWaitMs != 0 {
		t.Fatalf("apid pool = %+v", apid)
	}
	// A hub with neither subscribers nor a connection is idle, not disabled,
	// and is not counted as connected.
	for _, h := range r.NotifyHubs {
		if h.State != "ok" {
			t.Fatalf("hub = %+v", h)
		}
	}
	if h := c["notify_hubs"]; h.Detail != "1 connected, 1 idle (no subscribers)" {
		t.Fatalf("hubs = %q", h.Detail)
	}
	// db_pools counts pools; a percent threshold would not compare with it.
	if p := c["db_pools"]; p.WarnAt != nil || p.Unit != "count" {
		t.Fatalf("db_pools = %+v", p)
	}
	if *r.Host.CPUs != 12 || *r.Host.MemAvailableBytes != 28269838336 {
		t.Fatalf("host = %+v", r.Host)
	}
}

func TestBuildFlagsProblems(t *testing.T) {
	s := healthy()
	s["pg_connections"] = one(170)        // 91.9%: critical
	s["pg_oldest_transaction"] = one(900) // over 600 s
	s["pg_collector_errors"] = []promql.VectorSample{row(1, "section", "statements"), row(0, "section", "activity")}
	s["base_backup"] = one(0) // never pushed
	s["backup_units_failed"] = []promql.VectorSample{row(1, "name", "faas-cp-state-backup.service")}
	s["offhost_wal_configured"] = one(0) // not configured: ok
	s["wal_last_archived"] = one(ago(20 * time.Minute))
	s["notification_outbox_oldest"] = []promql.VectorSample{row(1200, "channel", "app_wake"), row(30, "channel", "deployment_ready")}
	s["notification_outbox_pending"] = []promql.VectorSample{row(4, "channel", "app_wake"), row(1, "channel", "deployment_ready")}
	s["pool_canceled_5m"] = []promql.VectorSample{row(2, "pool", "apid", "job", "apid")}
	s["pool_conns"] = append(s["pool_conns"], row(15, "pool", "schedd", "job", "schedd", "node", "bhs-3.faas", "state", "acquired"))
	s["pool_max"] = append(s["pool_max"], row(16, "pool", "schedd", "job", "schedd", "node", "bhs-3.faas"))
	s["hub_conns"] = append(s["hub_conns"], row(0, "hub", "schedd", "job", "schedd", "node", "bhs-2.faas"))
	s["hub_subscribers"] = append(s["hub_subscribers"], row(6, "hub", "schedd", "job", "schedd", "node", "bhs-2.faas"))
	s["targets"] = append(s["targets"], row(0, "job", "meterd", "instance", "127.0.0.1:9096"))

	r := build(s)
	c := checksByKey(r)
	want := map[string]string{
		"connections": "critical", "oldest_transaction": "warning", "activity_collector": "warning",
		"base_backup": "warning", "state_backup": "warning", "offhost_wal": "ok", "wal_archive": "warning",
		"notification_outbox": "warning", "db_pools": "critical", "notify_hubs": "warning", "targets": "critical",
		"idle_in_transaction": "ok", "wal_prune": "ok",
	}
	for k, v := range want {
		if c[k].State != v {
			t.Errorf("%s = %s (%s), want %s", k, c[k].State, c[k].Detail, v)
		}
	}
	if c["connections"].Alert != "FaasPostgresConnectionsCritical" || c["db_pools"].Alert != "FaasDBPoolStarved" || c["activity_collector"].Alert != "FaasPostgresActivityMetricsFailing" {
		t.Fatalf("alerts = %s %s %s", c["connections"].Alert, c["db_pools"].Alert, c["activity_collector"].Alert)
	}
	if c["base_backup"].Detail != "never succeeded" || c["state_backup"].Detail != "6.0 h ago, 15.3 KB; faas-cp-state-backup.service failed" {
		t.Fatalf("backups = %q / %q", c["base_backup"].Detail, c["state_backup"].Detail)
	}
	if c["notification_outbox"].Detail != "20 min on app_wake (4 undelivered), 5 undelivered in all" {
		t.Fatalf("outbox = %q", c["notification_outbox"].Detail)
	}
	if c["db_pools"].Detail != "acquires canceled: apid" || c["notify_hubs"].Detail != "subscribers but no connection: schedd@bhs-2.faas" || c["targets"].Detail != "down: meterd@127.0.0.1:9096" {
		t.Fatalf("daemons = %q / %q / %q", c["db_pools"].Detail, c["notify_hubs"].Detail, c["targets"].Detail)
	}
	if r.Pools[0].Pool != "apid" || r.Pools[0].State != "critical" || r.Pools[1].State != "warning" || r.Pools[1].Node != "bhs-3.faas" {
		t.Fatalf("pools = %+v (worst first)", r.Pools)
	}
	if r.Targets[0].Up || r.Targets[0].Job != "meterd" {
		t.Fatalf("targets = %+v (down first)", r.Targets)
	}
	if r.State != "critical" || r.Totals.Critical != 3 || r.Totals.Warning != 7 {
		t.Fatalf("state = %s totals = %+v", r.State, r.Totals)
	}
}

// A stale collector leaves its Postgres readings unknown: a last value of
// "no open transaction" is not current evidence. Connections come from a
// separate exporter and stay graded.
func TestStaleCollectorLeavesPostgresUnknown(t *testing.T) {
	s := healthy()
	s["pg_collector_last_run"] = one(ago(10 * time.Minute))
	s["pg_oldest_transaction"] = one(0)
	s["pg_connections"] = one(170)
	r := build(s)
	for _, c := range r.Checks {
		switch {
		case c.Key == "activity_collector":
			if c.State != "warning" || c.Detail != "ran 10 min ago" {
				t.Fatalf("collector = %+v", c)
			}
		case c.Key == "connections":
			if c.State != "critical" || c.Detail != "170 of 185" {
				t.Fatalf("connections = %+v (a stale activity collector must not hide them)", c)
			}
		case c.Group == "postgres":
			if c.State != "unknown" || !strings.HasSuffix(c.Detail, "(collector stale)") {
				t.Fatalf("%s = %+v", c.Key, c)
			}
		}
	}
}

// Edge readings graded as their alerts do: off-host WAL configured but never
// pushed fires PgWalOffHostPushStale; nothing archived yet does not fire
// PgWalArchiveStalled; a lock-wait read missing (the activity section
// failed) is unknown, not zero.
func TestAlertEdgeReadings(t *testing.T) {
	s := healthy()
	s["offhost_wal_last"] = one(0)
	s["wal_last_archived"] = one(0)
	delete(s, "pg_lock_waits")
	c := checksByKey(build(s))
	if o := c["offhost_wal"]; o.State != "warning" || o.Detail != "never succeeded" {
		t.Fatalf("offhost = %+v", o)
	}
	if w := c["wal_archive"]; w.State != "ok" || w.Detail != "nothing archived yet" {
		t.Fatalf("wal archive = %+v", w)
	}
	if l := c["lock_waits"]; l.State != "unknown" {
		t.Fatalf("lock waits = %+v", l)
	}
	q := controlplane.Queries()["pg_lock_waits"]
	if strings.Contains(q, "vector(0)") || !strings.Contains(q, `faas_postgres_activity_metrics_errors{section="activity"}) == 0`) {
		t.Fatalf("lock waits query = %s", q)
	}
}

func TestBuildEmptyShape(t *testing.T) {
	r := controlplane.Build(controlplane.Input{Now: now, Samples: controlplane.Samples{Source: controlplane.SourceUnavailable}})
	if r.State != "unknown" || r.Totals.Unknown != r.Totals.Checks || r.Totals.Checks != 17 {
		t.Fatalf("state = %s totals = %+v", r.State, r.Totals)
	}
	if r.FailedQueries == nil || r.Backends == nil || r.Statements == nil || r.Pools == nil || r.NotifyHubs == nil || r.Targets == nil {
		t.Fatal("slices must be empty, not null")
	}
	if r.WindowSeconds != 3600 {
		t.Fatalf("window = %d", r.WindowSeconds)
	}
}

type fakeProm struct {
	fail  string
	mu    sync.Mutex
	times map[time.Time]bool
}

func (f *fakeProm) QueryVectorAt(_ context.Context, q string, at time.Time) ([]promql.VectorSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.times == nil {
		f.times = map[time.Time]bool{}
	}
	f.times[at] = true
	if f.fail == "*" || f.fail != "" && strings.Contains(q, f.fail) {
		return nil, errors.New("boom")
	}
	return nil, nil
}

func TestReadSources(t *testing.T) {
	ctx := context.Background()
	f := &fakeProm{}
	if got := controlplane.ReadWith(ctx, f, now); got.Source != controlplane.SourceObserved || len(f.times) != 1 || !f.times[now] {
		t.Fatalf("observed = %+v times=%v", got, f.times)
	}
	if got := controlplane.ReadWith(ctx, &fakeProm{fail: "faas_cp_state_backup_bytes"}, now); got.Source != controlplane.SourcePartial ||
		len(got.Failed) != 1 || got.Failed[0] != "state_backup_bytes" {
		t.Fatalf("partial = %+v", got)
	}
	if got := controlplane.ReadWith(ctx, &fakeProm{fail: "*"}, now); got.Source != controlplane.SourceUnavailable {
		t.Fatalf("unavailable = %+v", got)
	}
	q := controlplane.Queries()
	if !strings.Contains(q["pool_canceled"], "[3600s:1m]") || !strings.Contains(q["pool_canceled_5m"], "[5m:1m]") || !strings.Contains(q["targets"], `node=""`) {
		t.Fatal("queries")
	}
	if controlplane.Read(ctx, nil, now).Source != controlplane.SourceNotConfigured {
		t.Fatal("nil client should be not_configured")
	}
}

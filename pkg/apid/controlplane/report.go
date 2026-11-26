// Package controlplane builds the operator control-plane health report
// (ADR-20261126120000): the control plane's Postgres (from the postgres
// activity collector), its backups and WAL, the delivery outboxes, every
// daemon's database pool and pg_notify hub, the control-plane scrape targets
// and the host. It reads only Prometheus and grades each signal with the
// threshold of the alert it mirrors.
package controlplane

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// Report sources.
const (
	SourceObserved      = "observed"
	SourcePartial       = "partial"
	SourceUnavailable   = "unavailable"
	SourceNotConfigured = "not_configured"
)

// Check states.
const (
	StateOK       = "ok"
	StateWarning  = "warning"
	StateCritical = "critical"
	StateUnknown  = "unknown"
)

// Check groups.
const (
	GroupPostgres = "postgres"
	GroupBackups  = "backups"
	GroupDelivery = "delivery"
	GroupDaemons  = "daemons"
)

// Query names.
const (
	qConns            = "pg_connections"
	qConnCap          = "pg_connection_capacity"
	qLongestQuery     = "pg_longest_query"
	qOldestXact       = "pg_oldest_transaction"
	qIdleXact         = "pg_idle_in_transaction"
	qLockWaits        = "pg_lock_waits"
	qDeadlocks        = "pg_deadlocks"
	qCollectorRun     = "pg_collector_last_run"
	qCollectorErrors  = "pg_collector_errors"
	qDBSize           = "pg_database_size"
	qCacheHit         = "pg_cache_hit"
	qCommits          = "pg_commits"
	qRollbacks        = "pg_rollbacks"
	qTemp             = "pg_temp_bytes"
	qBackends         = "pg_backends"
	qStmtTime         = "pg_statement_time"
	qStmtCalls        = "pg_statement_calls"
	qStmtInfo         = "pg_statement_info"
	qBaseBackup       = "base_backup"
	qStateBackup      = "state_backup"
	qStateBackupBytes = "state_backup_bytes"
	qBackupUnits      = "backup_units_failed"
	qWALArchived      = "wal_last_archived"
	qOffhostOn        = "offhost_wal_configured"
	qOffhostLast      = "offhost_wal_last"
	qWALPrune         = "wal_prune"
	qWALOldest        = "wal_archive_oldest"
	qNotifOldest      = "notification_outbox_oldest"
	qNotifPending     = "notification_outbox_pending"
	qWebhookOldest    = "webhook_outbox_oldest"
	qWebhookPending   = "webhook_outbox_pending"
	qPoolConns        = "pool_conns"
	qPoolMax          = "pool_max"
	qPoolCanceled     = "pool_canceled"
	qPoolCanceledNow  = "pool_canceled_5m"
	qPoolWait         = "pool_acquire_wait"
	qPoolAcquires     = "pool_acquires"
	qHubConns         = "hub_conns"
	qHubSubs          = "hub_subscribers"
	qHubReconnects    = "hub_reconnects"
	qTargets          = "targets"
	qCPUs             = "host_cpus"
	qLoad1            = "host_load1"
	qMemTotal         = "host_mem_total"
	qMemAvail         = "host_mem_available"
)

// family renames the daemon prefix of a per-daemon metric family
// (<daemon>_db_pool_conns, …) into a label, so one query reads every daemon.
func family(label, suffix string) string {
	return `label_replace({__name__=~".+_` + suffix + `"}, "` + label + `", "$1", "__name__", "(.+)_` + suffix + `")`
}

// Queries returns the report's PromQL by name.
func Queries() map[string]string {
	w := strconv.Itoa(int(api.ObsCPWindow.Seconds())) + "s"
	k := strconv.Itoa(api.ObsCPStatementsMax)
	inc := func(m string) string { return "sum(increase(" + m + "[" + w + "]))" }
	// A subquery keeps the label_replace'd name through increase().
	famInc := func(label, suffix, by, rng string) string {
		return "sum by (" + by + ") (increase(" + family(label, suffix) + "[" + rng + ":1m]))"
	}
	topStmt := "topk(" + k + ", increase(faas_postgres_statement_exec_seconds_total[" + w + "]) > 0)"
	return map[string]string{
		qConns:            "max(faas_postgres_client_connections)",
		qConnCap:          "max(faas_postgres_ordinary_connection_capacity)",
		qLongestQuery:     "max(faas_postgres_longest_query_seconds)",
		qOldestXact:       "max(faas_postgres_oldest_transaction_seconds)",
		qIdleXact:         "max(faas_postgres_idle_in_transaction_max_seconds)",
		qLockWaits:        `sum(faas_postgres_backends_waiting{wait_event_type="Lock"}) or vector(0)`,
		qDeadlocks:        "sum(increase(faas_postgres_deadlocks_total[15m]))",
		qCollectorRun:     "max(faas_postgres_activity_metrics_last_run_timestamp_seconds)",
		qCollectorErrors:  "max by (section) (faas_postgres_activity_metrics_errors)",
		qDBSize:           "max(faas_postgres_database_size_bytes)",
		qCacheHit:         inc("faas_postgres_blocks_hit_total") + " / (" + inc("faas_postgres_blocks_hit_total") + " + " + inc("faas_postgres_blocks_read_total") + ")",
		qCommits:          "sum(rate(faas_postgres_xact_commit_total[" + w + "]))",
		qRollbacks:        "sum(rate(faas_postgres_xact_rollback_total[" + w + "]))",
		qTemp:             inc("faas_postgres_temp_bytes_total"),
		qBackends:         "sum by (application, state) (faas_postgres_backends)",
		qStmtTime:         topStmt,
		qStmtCalls:        "increase(faas_postgres_statement_calls_total[" + w + "]) and on (queryid) " + topStmt,
		qStmtInfo:         "faas_postgres_statement_info and on (queryid) " + topStmt,
		qBaseBackup:       "max(apid_pg_backup_last_pushed_seconds)",
		qStateBackup:      "max(faas_cp_state_backup_last_success_timestamp_seconds)",
		qStateBackupBytes: "max(faas_cp_state_backup_bytes)",
		qBackupUnits:      `node_systemd_unit_state{job="node",state="failed",name=~"faas-pg-basebackup.service|faas-cp-state-backup.service"} == 1`,
		qWALArchived:      "max(faas_postgres_wal_last_archived_timestamp_seconds)",
		qOffhostOn:        "max(faas_postgres_offhost_wal_configured)",
		qOffhostLast:      "max(faas_postgres_offhost_wal_last_success_timestamp_seconds)",
		qWALPrune:         "max(apid_pg_wal_prune_last_success_timestamp_seconds)",
		qWALOldest:        "max(apid_pg_wal_archive_oldest_age_seconds)",
		qNotifOldest:      "max by (channel) (faas_notification_outbox_oldest_undelivered_seconds)",
		qNotifPending:     "sum by (channel) (faas_notification_outbox_undelivered)",
		qWebhookOldest:    "max(schedd_webhook_event_outbox_oldest_pending_seconds)",
		qWebhookPending:   "max(schedd_webhook_event_outbox_pending_count)",
		qPoolConns:        "sum by (pool, job, node, state) (" + family("pool", "db_pool_conns") + ")",
		qPoolMax:          "max by (pool, job, node) (" + family("pool", "db_pool_max_conns") + ")",
		qPoolCanceled:     famInc("pool", "db_pool_canceled_acquires_total", "pool, job, node", w),
		qPoolCanceledNow:  famInc("pool", "db_pool_canceled_acquires_total", "pool, job, node", "5m") + " > 0",
		qPoolWait:         famInc("pool", "db_pool_acquire_wait_seconds_total", "pool, job, node", w),
		qPoolAcquires:     famInc("pool", "db_pool_acquires_total", "pool, job, node", w),
		qHubConns:         "max by (hub, job, node) (" + family("hub", "db_notify_hub_conns") + ")",
		qHubSubs:          "max by (hub, job, node) (" + family("hub", "db_notify_hub_subscribers") + ")",
		qHubReconnects:    famInc("hub", "db_notify_hub_reconnects_total", "hub, job, node", w),
		qTargets:          `up{node=""}`,
		qCPUs:             `count(node_cpu_seconds_total{job="node",mode="idle"})`,
		qLoad1:            `max(node_load1{job="node"})`,
		qMemTotal:         `max(node_memory_MemTotal_bytes{job="node"})`,
		qMemAvail:         `max(node_memory_MemAvailable_bytes{job="node"})`,
	}
}

// VectorQuerier is the slice of the Prometheus client the report uses.
type VectorQuerier interface {
	QueryVectorAt(ctx context.Context, query string, at time.Time) ([]promql.VectorSample, error)
}

// Samples is what Read returned: each query's samples by name, the names
// that failed, and the source.
type Samples struct {
	ByQuery map[string][]promql.VectorSample
	Failed  []string
	Source  string
}

// Read runs every query concurrently at one instant under
// ObsCPPrometheusBudget.
func Read(ctx context.Context, client *promql.Client, at time.Time) Samples {
	if client == nil || client.BaseURL() == "" {
		return Samples{Source: SourceNotConfigured, ByQuery: map[string][]promql.VectorSample{}}
	}
	return read(ctx, client, at)
}

func read(ctx context.Context, q VectorQuerier, at time.Time) Samples {
	ctx, cancel := context.WithTimeout(ctx, api.ObsCPPrometheusBudget)
	defer cancel()
	queries := Queries()
	out := Samples{ByQuery: make(map[string][]promql.VectorSample, len(queries))}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, query := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := q.QueryVectorAt(ctx, query, at)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Failed = append(out.Failed, name)
				return
			}
			out.ByQuery[name] = rows
		}()
	}
	wg.Wait()
	sort.Strings(out.Failed)
	switch {
	case len(out.Failed) == 0:
		out.Source = SourceObserved
	case len(out.Failed) == len(queries):
		out.Source = SourceUnavailable
	default:
		out.Source = SourcePartial
	}
	return out
}

// Input is the instant and the samples.
type Input struct {
	Now     time.Time
	Samples Samples
}

// view reads single values out of the samples.
type view map[string][]promql.VectorSample

// scalar is a single-sample query's value; ok is false when it is missing
// or not a number.
func (v view) scalar(name string) (float64, bool) {
	rows := v[name]
	if len(rows) == 0 || math.IsNaN(rows[0].Value) || math.IsInf(rows[0].Value, 0) {
		return 0, false
	}
	return rows[0].Value, true
}

func (v view) ptr(name string) *float64 {
	if x, ok := v.scalar(name); ok {
		return &x
	}
	return nil
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func f64(x float64) *float64 { return &x }

func intPtr(p *float64) *int {
	if p == nil {
		return nil
	}
	n := int(math.Round(*p))
	return &n
}

func int64Ptr(p *float64) *int64 {
	if p == nil {
		return nil
	}
	n := int64(math.Round(*p))
	return &n
}

// Build folds the samples into the report.
func Build(in Input) api.ObsControlPlaneReport {
	v := view(in.Samples.ByQuery)
	if v == nil {
		v = view{}
	}
	out := api.ObsControlPlaneReport{
		AsOf: in.Now, Source: in.Samples.Source, FailedQueries: in.Samples.Failed,
		WindowSeconds: int(api.ObsCPWindow.Seconds()),
		Backends:      []api.ObsControlPlaneBackend{}, Statements: []api.ObsControlPlaneStatement{},
		Pools: []api.ObsControlPlanePool{}, NotifyHubs: []api.ObsControlPlaneNotifyHub{}, Targets: []api.ObsControlPlaneTarget{},
	}
	if out.FailedQueries == nil {
		out.FailedQueries = []string{}
	}
	out.Postgres = postgres(v, in.Now)
	out.Backends = backends(v)
	out.Statements = statements(v)
	out.Pools = pools(v)
	out.NotifyHubs = hubs(v)
	out.Targets = targets(v)
	out.Host = api.ObsControlPlaneHost{CPUs: intPtr(v.ptr(qCPUs)), Load1: v.ptr(qLoad1),
		MemTotalBytes: int64Ptr(v.ptr(qMemTotal)), MemAvailableBytes: int64Ptr(v.ptr(qMemAvail))}
	out.Checks = checks(v, in.Now, out)
	out.State = StateOK
	for _, c := range out.Checks {
		out.Totals.Checks++
		switch c.State {
		case StateCritical:
			out.Totals.Critical++
		case StateWarning:
			out.Totals.Warning++
		case StateUnknown:
			out.Totals.Unknown++
		}
		if stateRank(c.State) < stateRank(out.State) {
			out.State = c.State
		}
	}
	return out
}

// stateRank orders states worst first.
func stateRank(s string) int {
	switch s {
	case StateCritical:
		return 0
	case StateWarning:
		return 1
	case StateUnknown:
		return 2
	default:
		return 3
	}
}

func postgres(v view, now time.Time) api.ObsControlPlanePostgres {
	p := api.ObsControlPlanePostgres{
		ClientConnections: intPtr(v.ptr(qConns)), ConnectionCapacity: intPtr(v.ptr(qConnCap)),
		DatabaseSizeBytes: int64Ptr(v.ptr(qDBSize)), LongestQuerySeconds: v.ptr(qLongestQuery),
		OldestTransactionSeconds: v.ptr(qOldestXact), IdleInTransactionMaxSeconds: v.ptr(qIdleXact),
		TempBytes: int64Ptr(v.ptr(qTemp)),
	}
	if x, ok := v.scalar(qCacheHit); ok {
		p.CacheHitPercent = f64(round1(x * 100))
	}
	commits, okC := v.scalar(qCommits)
	if okC {
		p.CommitsPerSecond = f64(round1(commits))
	}
	if rb, ok := v.scalar(qRollbacks); ok && okC && commits+rb > 0 {
		p.RollbackPercent = f64(round1(rb / (commits + rb) * 100))
	}
	if run, ok := v.scalar(qCollectorRun); ok && run > 0 {
		p.CollectorAgeSeconds = f64(math.Max(0, math.Round(now.Sub(time.Unix(int64(run), 0)).Seconds())))
	}
	return p
}

func backends(v view) []api.ObsControlPlaneBackend {
	out := []api.ObsControlPlaneBackend{}
	for _, r := range v[qBackends] {
		if n := int(math.Round(r.Value)); n > 0 {
			out = append(out, api.ObsControlPlaneBackend{Application: r.Labels["application"], State: r.Labels["state"], Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Application+out[i].State < out[j].Application+out[j].State
	})
	if len(out) > api.ObsCPRowsMax {
		out = out[:api.ObsCPRowsMax]
	}
	return out
}

func statements(v view) []api.ObsControlPlaneStatement {
	calls := map[string]float64{}
	for _, r := range v[qStmtCalls] {
		calls[r.Labels["queryid"]] = r.Value
	}
	text := map[string]string{}
	for _, r := range v[qStmtInfo] {
		text[r.Labels["queryid"]] = r.Labels["query"]
	}
	out := []api.ObsControlPlaneStatement{}
	for _, r := range v[qStmtTime] {
		id := r.Labels["queryid"]
		if id == "" || math.IsNaN(r.Value) || r.Value <= 0 {
			continue
		}
		s := api.ObsControlPlaneStatement{QueryID: id, Query: cut(text[id], api.ObsCPStatementQueryMaxChars), ExecSeconds: round1(r.Value)}
		if c, ok := calls[id]; ok && c >= 0.5 {
			n := int64(math.Round(c))
			s.Calls = &n
			s.MeanMs = f64(math.Round(r.Value/c*1000*100) / 100)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExecSeconds != out[j].ExecSeconds {
			return out[i].ExecSeconds > out[j].ExecSeconds
		}
		return out[i].QueryID < out[j].QueryID
	})
	if len(out) > api.ObsCPStatementsMax {
		out = out[:api.ObsCPStatementsMax]
	}
	return out
}

// cut shortens s to at most n runes.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

type poolKey struct{ pool, job, node string }

func keyOf(l map[string]string, name string) poolKey {
	return poolKey{l[name], l["job"], l["node"]}
}

func pools(v view) []api.ObsControlPlanePool {
	rows := map[poolKey]*api.ObsControlPlanePool{}
	row := func(k poolKey) *api.ObsControlPlanePool {
		if rows[k] == nil {
			rows[k] = &api.ObsControlPlanePool{Pool: k.pool, Job: k.job, Node: k.node, State: StateOK}
		}
		return rows[k]
	}
	for _, r := range v[qPoolConns] {
		k := keyOf(r.Labels, "pool")
		if k.pool == "" {
			continue
		}
		n := int(math.Round(r.Value))
		switch r.Labels["state"] {
		case "acquired":
			row(k).Acquired += n
		case "idle":
			row(k).Idle += n
		default:
			row(k)
		}
	}
	for _, r := range v[qPoolMax] {
		if k := keyOf(r.Labels, "pool"); k.pool != "" && r.Value > 0 {
			row(k).Max = intPtr(&r.Value)
		}
	}
	for _, r := range v[qPoolCanceled] {
		if k := keyOf(r.Labels, "pool"); rows[k] != nil {
			rows[k].CanceledAcquires = int64(math.Round(r.Value))
		}
	}
	acquires := map[poolKey]float64{}
	for _, r := range v[qPoolAcquires] {
		acquires[keyOf(r.Labels, "pool")] = r.Value
	}
	for _, r := range v[qPoolWait] {
		k := keyOf(r.Labels, "pool")
		if rows[k] != nil && acquires[k] >= 1 {
			rows[k].MeanAcquireWaitMs = f64(math.Round(r.Value/acquires[k]*1000*100) / 100)
		}
	}
	starved := map[poolKey]bool{}
	for _, r := range v[qPoolCanceledNow] {
		starved[keyOf(r.Labels, "pool")] = true
	}
	out := make([]api.ObsControlPlanePool, 0, len(rows))
	for k, p := range rows {
		if p.Max != nil && *p.Max > 0 {
			p.UtilizationPercent = f64(round1(float64(p.Acquired) / float64(*p.Max) * 100))
		}
		switch {
		case starved[k]:
			p.State = StateCritical
		case p.UtilizationPercent != nil && *p.UtilizationPercent > api.ObsCPPoolSaturationPercent:
			p.State = StateWarning
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := stateRank(a.State), stateRank(b.State); ra != rb {
			return ra < rb
		}
		if ua, ub := util(a), util(b); ua != ub {
			return ua > ub
		}
		return a.Pool+a.Node+a.Job < b.Pool+b.Node+b.Job
	})
	if len(out) > api.ObsCPRowsMax {
		out = out[:api.ObsCPRowsMax]
	}
	return out
}

func util(p api.ObsControlPlanePool) float64 {
	if p.UtilizationPercent == nil {
		return -1
	}
	return *p.UtilizationPercent
}

func hubs(v view) []api.ObsControlPlaneNotifyHub {
	rows := map[poolKey]*api.ObsControlPlaneNotifyHub{}
	row := func(k poolKey) *api.ObsControlPlaneNotifyHub {
		if rows[k] == nil {
			rows[k] = &api.ObsControlPlaneNotifyHub{Hub: k.pool, Job: k.job, Node: k.node, State: StateOK}
		}
		return rows[k]
	}
	for _, r := range v[qHubConns] {
		if k := keyOf(r.Labels, "hub"); k.pool != "" {
			row(k).Conns = int(math.Round(r.Value))
		}
	}
	for _, r := range v[qHubSubs] {
		if k := keyOf(r.Labels, "hub"); k.pool != "" {
			row(k).Subscribers = int(math.Round(r.Value))
		}
	}
	for _, r := range v[qHubReconnects] {
		if k := keyOf(r.Labels, "hub"); rows[k] != nil {
			rows[k].Reconnects = int64(math.Round(r.Value))
		}
	}
	out := make([]api.ObsControlPlaneNotifyHub, 0, len(rows))
	for _, h := range rows {
		if h.Conns == 0 && h.Subscribers > 0 {
			h.State = StateWarning
		}
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := stateRank(a.State), stateRank(b.State); ra != rb {
			return ra < rb
		}
		if a.Reconnects != b.Reconnects {
			return a.Reconnects > b.Reconnects
		}
		return a.Hub+a.Node+a.Job < b.Hub+b.Node+b.Job
	})
	if len(out) > api.ObsCPRowsMax {
		out = out[:api.ObsCPRowsMax]
	}
	return out
}

func targets(v view) []api.ObsControlPlaneTarget {
	out := []api.ObsControlPlaneTarget{}
	for _, r := range v[qTargets] {
		out = append(out, api.ObsControlPlaneTarget{Job: r.Labels["job"], Instance: r.Labels["instance"], Up: r.Value == 1})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Up != out[j].Up {
			return !out[i].Up
		}
		return out[i].Job+out[i].Instance < out[j].Job+out[j].Instance
	})
	if len(out) > api.ObsCPRowsMax {
		out = out[:api.ObsCPRowsMax]
	}
	return out
}

// checks grades every signal, in a fixed order.
func checks(v view, now time.Time, r api.ObsControlPlaneReport) []api.ObsControlPlaneCheck {
	var out []api.ObsControlPlaneCheck
	add := func(c api.ObsControlPlaneCheck) { out = append(out, c) }
	collectorStale := collectorCheck(v, r.Postgres, add)
	pg := func(c api.ObsControlPlaneCheck) {
		c.Group = GroupPostgres
		if collectorStale && c.State != StateUnknown {
			c.State, c.Detail = StateUnknown, c.Detail+" (collector stale)"
		}
		add(c)
	}
	pg(connectionsCheck(r.Postgres))
	pg(above("oldest_transaction", "Oldest open transaction", "seconds", v.ptr(qOldestXact), api.ObsCPOldestTransactionWarnSeconds, "FaasPostgresLongTransaction", secondsText))
	pg(above("idle_in_transaction", "Longest idle in transaction", "seconds", v.ptr(qIdleXact), api.ObsCPIdleInTransactionWarnSeconds, "FaasPostgresIdleInTransaction", secondsText))
	pg(above("lock_waits", "Backends waiting on locks", "count", v.ptr(qLockWaits), api.ObsCPLockWaitsWarn, "FaasPostgresLockWaits", countText))
	pg(above("deadlocks", "Deadlocks in the last 15 min", "count", v.ptr(qDeadlocks), 0, "FaasPostgresDeadlocks", countText))

	failedUnits := map[string]bool{}
	for _, row := range v[qBackupUnits] {
		failedUnits[row.Labels["name"]] = true
	}
	backup := func(c api.ObsControlPlaneCheck) { c.Group = GroupBackups; add(c) }
	backup(lastSuccess("base_backup", "Postgres base backup pushed", v, now, qBaseBackup, api.ObsCPBackupStaleSeconds, true, "PgBackupStale",
		failedUnits["faas-pg-basebackup.service"], "faas-pg-basebackup.service failed", ""))
	size := ""
	if b, ok := v.scalar(qStateBackupBytes); ok {
		size = ", " + bytesText(b)
	}
	backup(lastSuccess("state_backup", "Control-plane state backup", v, now, qStateBackup, api.ObsCPBackupStaleSeconds, false, "FaasControlPlaneStateBackupStale",
		failedUnits["faas-cp-state-backup.service"], "faas-cp-state-backup.service failed", size))
	backup(walArchiveCheck(v, now))
	backup(offhostCheck(v, now))
	backup(lastSuccess("wal_prune", "WAL archive pruned", v, now, qWALPrune, api.ObsCPWALPruneStaleSeconds, true, "PgWalPruneStale", false, "", ""))
	backup(above("wal_archive_oldest", "Oldest archived WAL", "seconds", v.ptr(qWALOldest), api.ObsCPWALArchiveOldestWarnSeconds, "PgWalArchiveLarge", secondsText))

	delivery := func(c api.ObsControlPlaneCheck) { c.Group = GroupDelivery; add(c) }
	delivery(notificationOutboxCheck(v))
	webhook := above("webhook_outbox", "Oldest pending webhook event", "seconds", v.ptr(qWebhookOldest), api.ObsCPWebhookOutboxBacklogSeconds, "FaasWebhookEventOutboxBacklog", secondsText)
	if n, ok := v.scalar(qWebhookPending); ok && webhook.State != StateUnknown {
		webhook.Detail += fmt.Sprintf(", %d pending", int64(n))
	}
	delivery(webhook)

	daemons := func(c api.ObsControlPlaneCheck) { c.Group = GroupDaemons; add(c) }
	daemons(poolsCheck(v, r.Pools))
	daemons(hubsCheck(v, r.NotifyHubs))
	daemons(targetsCheck(v, r.Targets))
	return out
}

func secondsText(x float64) string { return durationText(x) }

func countText(x float64) string { return strconv.FormatInt(int64(math.Round(x)), 10) }

// durationText renders seconds compactly: 45 s, 12 min, 3.5 h, 2.1 d.
func durationText(s float64) string {
	switch {
	case s < 120:
		return fmt.Sprintf("%d s", int64(math.Round(s)))
	case s < 2*3600:
		return fmt.Sprintf("%d min", int64(math.Round(s/60)))
	case s < 48*3600:
		return fmt.Sprintf("%.1f h", s/3600)
	default:
		return fmt.Sprintf("%.1f d", s/86400)
	}
}

func bytesText(b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for b >= 1000 && i < len(units)-1 {
		b /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", int64(b))
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

// above is a warning when value exceeds warn (the alert's `>`).
func above(key, label, unit string, value *float64, warn float64, alert string, text func(float64) string) api.ObsControlPlaneCheck {
	c := api.ObsControlPlaneCheck{Key: key, Label: label, Unit: unit, WarnAt: f64(warn), Alert: alert, State: StateUnknown, Detail: "not reported"}
	if value == nil {
		return c
	}
	c.Value = f64(round1(*value))
	c.Detail = text(*value)
	c.State = StateOK
	if *value > warn {
		c.State = StateWarning
	}
	return c
}

func connectionsCheck(p api.ObsControlPlanePostgres) api.ObsControlPlaneCheck {
	c := api.ObsControlPlaneCheck{Key: "connections", Label: "Postgres connections", Unit: "percent",
		WarnAt: f64(api.ObsCPConnectionsWarnPercent), CriticalAt: f64(api.ObsCPConnectionsCriticalPercent),
		Alert: "FaasPostgresConnectionsHigh", State: StateUnknown, Detail: "not reported"}
	if p.ClientConnections == nil || p.ConnectionCapacity == nil || *p.ConnectionCapacity <= 0 {
		return c
	}
	pct := float64(*p.ClientConnections) / float64(*p.ConnectionCapacity) * 100
	c.Value = f64(round1(pct))
	c.Detail = fmt.Sprintf("%d of %d", *p.ClientConnections, *p.ConnectionCapacity)
	switch {
	case pct > api.ObsCPConnectionsCriticalPercent:
		c.State, c.Alert = StateCritical, "FaasPostgresConnectionsCritical"
	case pct > api.ObsCPConnectionsWarnPercent:
		c.State = StateWarning
	default:
		c.State = StateOK
	}
	return c
}

// collectorCheck grades the postgres activity collector and reports whether
// it is stale, which leaves the Postgres checks unknown.
func collectorCheck(v view, p api.ObsControlPlanePostgres, add func(api.ObsControlPlaneCheck)) bool {
	c := api.ObsControlPlaneCheck{Key: "activity_collector", Group: GroupPostgres, Label: "Postgres stats collector", Unit: "seconds",
		WarnAt: f64(api.ObsCPActivityStaleSeconds), Alert: "FaasPostgresActivityMetricsStale", State: StateUnknown, Detail: "not reported"}
	stale := true
	if p.CollectorAgeSeconds != nil {
		c.Value = p.CollectorAgeSeconds
		c.Detail = "ran " + durationText(*p.CollectorAgeSeconds) + " ago"
		c.State = StateOK
		stale = *p.CollectorAgeSeconds > api.ObsCPActivityStaleSeconds
		if stale {
			c.State = StateWarning
		}
	}
	var failing []string
	for _, r := range v[qCollectorErrors] {
		if r.Value == 1 {
			failing = append(failing, r.Labels["section"])
		}
	}
	if len(failing) > 0 && c.State != StateUnknown {
		sort.Strings(failing)
		c.State, c.Alert = StateWarning, "FaasPostgresActivityMetricsFailing"
		c.Detail += "; failing: " + strings.Join(failing, ", ")
	}
	add(c)
	return stale
}

// lastSuccess grades the age of a last-success timestamp. With zeroIsNever,
// a zero timestamp means it never succeeded (a warning, as its alert
// fires); otherwise zero is not reported. A failed unit is a warning too.
// extra is appended to a successful reading's detail.
func lastSuccess(key, label string, v view, now time.Time, query string, stale float64, zeroIsNever bool, alert string, unitFailed bool, unitText, extra string) api.ObsControlPlaneCheck {
	c := api.ObsControlPlaneCheck{Key: key, Label: label, Unit: "seconds", WarnAt: f64(stale), Alert: alert, State: StateUnknown, Detail: "not reported"}
	ts, ok := v.scalar(query)
	switch {
	case ok && ts > 0:
		at := time.Unix(int64(ts), 0).UTC()
		age := math.Max(0, now.Sub(at).Seconds())
		c.At, c.Value = &at, f64(math.Round(age))
		c.Detail = durationText(age) + " ago" + extra
		c.State = StateOK
		if age > stale {
			c.State = StateWarning
		}
	case ok && zeroIsNever:
		c.State, c.Detail = StateWarning, "never succeeded"
	}
	if unitFailed {
		c.State = StateWarning
		if c.Detail == "not reported" {
			c.Detail = unitText
		} else {
			c.Detail += "; " + unitText
		}
	}
	return c
}

func walArchiveCheck(v view, now time.Time) api.ObsControlPlaneCheck {
	c := lastSuccess("wal_archive", "Postgres WAL archived", v, now, qWALArchived, api.ObsCPWALArchiveStallSeconds, false, "PgWalArchiveStalled", false, "", "")
	if ts, ok := v.scalar(qWALArchived); ok && ts == 0 {
		c.Detail = "nothing archived yet"
	}
	return c
}

func offhostCheck(v view, now time.Time) api.ObsControlPlaneCheck {
	on, ok := v.scalar(qOffhostOn)
	if ok && on != 1 {
		return api.ObsControlPlaneCheck{Key: "offhost_wal", Label: "WAL pushed off host", Unit: "seconds", State: StateOK,
			Detail: "not configured", Alert: "PgWalOffHostPushStale", WarnAt: f64(api.ObsCPOffhostWALStaleSeconds)}
	}
	c := lastSuccess("offhost_wal", "WAL pushed off host", v, now, qOffhostLast, api.ObsCPOffhostWALStaleSeconds, false, "PgWalOffHostPushStale", false, "", "")
	if !ok {
		c.State, c.Value, c.At, c.Detail = StateUnknown, nil, nil, "not reported"
	}
	return c
}

func notificationOutboxCheck(v view) api.ObsControlPlaneCheck {
	c := api.ObsControlPlaneCheck{Key: "notification_outbox", Label: "Oldest undelivered notification", Unit: "seconds",
		WarnAt: f64(api.ObsCPNotificationOutboxStuckSeconds), Alert: "FaasNotificationOutboxStuck", State: StateUnknown, Detail: "not reported"}
	rows := v[qNotifOldest]
	if len(rows) == 0 {
		return c
	}
	pending := map[string]float64{}
	var total float64
	for _, r := range v[qNotifPending] {
		pending[r.Labels["channel"]] = r.Value
		total += r.Value
	}
	worst := rows[0]
	for _, r := range rows[1:] {
		if r.Value > worst.Value {
			worst = r
		}
	}
	c.Value = f64(round1(worst.Value))
	c.State = StateOK
	if worst.Value > api.ObsCPNotificationOutboxStuckSeconds {
		c.State = StateWarning
	}
	if worst.Value > 0 {
		c.Detail = fmt.Sprintf("%s on %s, %d undelivered", durationText(worst.Value), worst.Labels["channel"], int64(total))
	} else {
		c.Detail = fmt.Sprintf("%d undelivered", int64(total))
	}
	return c
}

func poolsCheck(v view, rows []api.ObsControlPlanePool) api.ObsControlPlaneCheck {
	c := api.ObsControlPlaneCheck{Key: "db_pools", Label: "Daemon database pools", Unit: "count",
		WarnAt: f64(api.ObsCPPoolSaturationPercent), Alert: "FaasDBPoolNearCap", State: StateUnknown, Detail: "not reported"}
	if _, ok := v[qPoolConns]; !ok || len(rows) == 0 {
		return c
	}
	var starved, near []string
	for _, p := range rows {
		name := p.Pool
		if p.Node != "" {
			name += "@" + p.Node
		}
		switch p.State {
		case StateCritical:
			starved = append(starved, name)
		case StateWarning:
			near = append(near, name)
		}
	}
	c.Value = f64(float64(len(starved) + len(near)))
	switch {
	case len(starved) > 0:
		c.State, c.Alert = StateCritical, "FaasDBPoolStarved"
		c.Detail = "acquires canceled: " + strings.Join(starved, ", ")
	case len(near) > 0:
		c.State = StateWarning
		c.Detail = "over " + strconv.Itoa(api.ObsCPPoolSaturationPercent) + "% in use: " + strings.Join(near, ", ")
	default:
		c.State = StateOK
		c.Detail = fmt.Sprintf("%d pools healthy", len(rows))
	}
	return c
}

func hubsCheck(v view, rows []api.ObsControlPlaneNotifyHub) api.ObsControlPlaneCheck {
	c := api.ObsControlPlaneCheck{Key: "notify_hubs", Label: "pg_notify listeners", Unit: "count",
		Alert: "FaasDBNotifyHubDisabled", State: StateUnknown, Detail: "not reported"}
	if _, ok := v[qHubConns]; !ok || len(rows) == 0 {
		return c
	}
	var disabled []string
	for _, h := range rows {
		if h.State == StateWarning {
			name := h.Hub
			if h.Node != "" {
				name += "@" + h.Node
			}
			disabled = append(disabled, name)
		}
	}
	c.Value = f64(float64(len(disabled)))
	c.State, c.Detail = StateOK, fmt.Sprintf("%d listeners connected", len(rows))
	if len(disabled) > 0 {
		c.State, c.Detail = StateWarning, "subscribers but no connection: "+strings.Join(disabled, ", ")
	}
	return c
}

func targetsCheck(v view, rows []api.ObsControlPlaneTarget) api.ObsControlPlaneCheck {
	c := api.ObsControlPlaneCheck{Key: "targets", Label: "Control-plane daemons scraped", Unit: "count", State: StateUnknown, Detail: "not reported"}
	if _, ok := v[qTargets]; !ok || len(rows) == 0 {
		return c
	}
	var down []string
	for _, t := range rows {
		if !t.Up {
			down = append(down, t.Job)
		}
	}
	c.Value = f64(float64(len(down)))
	c.State, c.Detail = StateOK, fmt.Sprintf("%d of %d up", len(rows)-len(down), len(rows))
	if len(down) > 0 {
		c.State, c.Detail = StateCritical, "down: "+strings.Join(down, ", ")
	}
	return c
}

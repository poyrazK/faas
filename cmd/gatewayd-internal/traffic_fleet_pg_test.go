//go:build !no_pg

// adr: 375
package main

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func fleetDaemonCall(t *testing.T, p *fleetDaemonProcess, app fleetDaemonApp, path string, want int) fleetDaemonResponse {
	t.Helper()
	response, err := requestFleetDaemon(t.Context(), p.ready.Endpoint, app.Host, path)
	if err != nil || response.status != want {
		t.Fatalf("daemon request status=%d want=%d body=%q err=%v", response.status, want, response.body, err)
	}
	return response
}

func fleetDaemonCounter(t *testing.T, f fleetDaemonFixture, scope, subject string) int64 {
	t.Helper()
	var tokens int64
	if err := f.pool.QueryRow(t.Context(), "SELECT tokens FROM pg_ratelimit_counters WHERE scope=$1 AND subject_id=$2 AND plan='pro'", scope, subject).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	return tokens
}

// Future refill timestamps preserve a fixed fixture balance without replacing
// the production limiter or changing its refill/window implementation.
func fleetDaemonSetBalance(t *testing.T, f fleetDaemonFixture, scope, subject string, tokens int) {
	t.Helper()
	result, err := f.pool.Exec(t.Context(), "UPDATE pg_ratelimit_counters SET tokens=$3,last_refill=now()+interval '1 minute' WHERE scope=$1 AND subject_id=$2 AND plan='pro'", scope, subject, tokens)
	if err != nil || result.RowsAffected() != 1 {
		t.Fatalf("set fixture balance: affected=%d err=%v", result.RowsAffected(), err)
	}
}

func fleetDaemonReplacement(t *testing.T, f fleetDaemonFixture, previous *fleetDaemonProcess) *fleetDaemonProcess {
	t.Helper()
	previous.stop(t)
	retired, err := f.store.ReadGatewayTrafficEpoch(t.Context(), previous.ready.NodeName)
	if err != nil || retired.Generation != previous.ready.Generation {
		t.Fatalf("retired daemon generation=%+v previous=%d err=%v", retired, previous.ready.Generation, err)
	}
	rows, err := f.store.ListServingGatewayTrafficRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.NodeName == previous.ready.NodeName && !row.ReportedAt.IsZero() {
			t.Fatal("stopped daemon retained a serving observation")
		}
	}
	next := f.process(t, 0)
	// Generations come from one global SQL sequence, so the other gateway's
	// registration also consumes a value. Replacement must advance ownership.
	if next.ready.Generation <= retired.Generation || next.ready.RetryBackendID != previous.ready.RetryBackendID {
		t.Fatalf("replacement wiring previous=%+v next=%+v", previous.ready, next.ready)
	}
	return next
}

func TestTrafficFleetDaemonRateAdmissionAndRecovery(t *testing.T) {
	f := newFleetDaemonFixture(t)
	first, second := f.process(t, 0), f.process(t, 1)
	if first.ready.RetryBackendID != second.ready.RetryBackendID || first.ready.NodeName == second.ready.NodeName {
		t.Fatal("fleet observations do not identify distinct gateways with one retry backend")
	}
	type result struct {
		fleetDaemonResponse
		err error
	}
	results := make(chan result, 16)
	var wg sync.WaitGroup
	started := time.Now()
	for i := range 16 {
		wg.Go(func() {
			response, err := requestFleetDaemon(t.Context(), []*fleetDaemonProcess{first, second}[i%2].ready.Endpoint, f.apps[0].Host, "/work")
			results <- result{response, err}
		})
	}
	wg.Wait()
	close(results)
	allowed := 0
	latencies := make([]time.Duration, 0, 16)
	for response := range results {
		if response.err != nil {
			t.Fatal(response.err)
		}
		latencies = append(latencies, response.duration)
		switch response.status {
		case http.StatusOK:
			allowed++
			if response.body != "guest served" {
				t.Fatalf("guest response=%q", response.body)
			}
		case http.StatusTooManyRequests:
			if response.headers.Get("X-Faas-Rate-Limit-Scope") != "app" {
				t.Fatalf("refusal scope=%q", response.headers.Get("X-Faas-Rate-Limit-Scope"))
			}
		default:
			t.Fatalf("unexpected fleet response=%+v", response.fleetDaemonResponse)
		}
	}
	bound := 4 + int(time.Since(started)/time.Second)
	if allowed < 4 || allowed > bound || len(f.vm.calls("/work")) != allowed || f.vm.guestCalls("/work") != allowed {
		t.Fatalf("admitted=%d bound=%d RPCs=%v guest=%d", allowed, bound, f.vm.calls("/work"), f.vm.guestCalls("/work"))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("daemon fleet: admitted=%d/16 p50=%s p95=%s", allowed, latencies[8], latencies[15])
	fleetDaemonSetBalance(t, f, "app", f.apps[0].ID, 0)
	third := fleetDaemonReplacement(t, f, first)
	fleetDaemonCall(t, third, f.apps[0], "/work", http.StatusTooManyRequests)
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE pg_ratelimit_counters RENAME TO pg_ratelimit_counters_offline"); err != nil {
		t.Fatal(err)
	}
	for _, process := range []*fleetDaemonProcess{second, third} {
		response := fleetDaemonCall(t, process, f.apps[0], "/work", http.StatusServiceUnavailable)
		if !strings.Contains(response.body, "rate_limit_unavailable") {
			t.Fatalf("counter outage response=%q", response.body)
		}
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE pg_ratelimit_counters_offline RENAME TO pg_ratelimit_counters"); err != nil {
		t.Fatal(err)
	}
	fleetDaemonCall(t, third, f.apps[0], "/work", http.StatusTooManyRequests)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), "SELECT subject_id FROM pg_ratelimit_counters WHERE scope='app' AND subject_id=$1 FOR UPDATE", f.apps[0].ID); err != nil {
		t.Fatal(err)
	}
	response := fleetDaemonCall(t, second, f.apps[0], "/work", http.StatusServiceUnavailable)
	if response.duration > time.Second || !strings.Contains(response.body, "rate_limit_unavailable") {
		t.Fatalf("bounded lock response=%+v", response)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	fleetDaemonCall(t, second, f.apps[0], "/work", http.StatusTooManyRequests)
	if fleetDaemonCounter(t, f, "app", f.apps[0].ID) != 0 || len(f.vm.calls("/work")) != allowed || f.vm.guestCalls("/work") != allowed {
		t.Fatal("replacement, outage or lock recovery reset debt or forwarded a refused request")
	}
}

func TestTrafficFleetDaemonCacheHitsChargeAppAndAccount(t *testing.T) {
	f := newFleetDaemonFixture(t)
	first, second := f.process(t, 0), f.process(t, 1)
	fleetDaemonCall(t, first, f.apps[1], "/cache", http.StatusOK)
	fleetDaemonSetBalance(t, f, "app", f.apps[1].ID, 4)
	fleetDaemonSetBalance(t, f, "account", f.app.AccountID, 100)
	for i, process := range []*fleetDaemonProcess{first, second, first, second} {
		fleetDaemonCall(t, process, f.apps[1], "/cache", http.StatusOK)
		if got := fleetDaemonCounter(t, f, "app", f.apps[1].ID); got != int64(3-i) {
			t.Fatalf("cache app charge #%d balance=%d", i+1, got)
		}
		if got := fleetDaemonCounter(t, f, "account", f.app.AccountID); got != int64(99-i) {
			t.Fatalf("cache account charge #%d balance=%d", i+1, got)
		}
	}
	if len(f.vm.calls("/cache")) != 2 || f.vm.guestCalls("/cache") != 2 {
		t.Fatalf("cache must avoid three guest forwards: RPC=%v guest=%d", f.vm.calls("/cache"), f.vm.guestCalls("/cache"))
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE pg_ratelimit_counters RENAME TO pg_ratelimit_counters_offline"); err != nil {
		t.Fatal(err)
	}
	for _, process := range []*fleetDaemonProcess{first, second} {
		response := fleetDaemonCall(t, process, f.apps[1], "/cache", http.StatusServiceUnavailable)
		if !strings.Contains(response.body, "rate_limit_unavailable") {
			t.Fatal("warm cache bypassed unavailable central admission")
		}
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE pg_ratelimit_counters_offline RENAME TO pg_ratelimit_counters"); err != nil {
		t.Fatal(err)
	}
	for _, process := range []*fleetDaemonProcess{first, second} {
		response := fleetDaemonCall(t, process, f.apps[1], "/cache", http.StatusTooManyRequests)
		if response.headers.Get("X-Faas-Rate-Limit-Scope") != "app" {
			t.Fatal("warm cache bypassed app admission")
		}
	}
	if got := fleetDaemonCounter(t, f, "account", f.app.AccountID); got != 94 {
		t.Fatalf("sequential account admission balance=%d want=94", got)
	}
	fleetDaemonSetBalance(t, f, "account", f.app.AccountID, 0)
	for _, process := range []*fleetDaemonProcess{first, second} {
		for _, app := range []fleetDaemonApp{f.apps[0], f.apps[1]} {
			response := fleetDaemonCall(t, process, app, "/cache", http.StatusTooManyRequests)
			if response.headers.Get("X-Faas-Rate-Limit-Scope") != "account" {
				t.Fatal("app did not share its account refusal")
			}
		}
	}
	if len(f.vm.calls("/cache")) != 2 || f.vm.guestCalls("/cache") != 2 || fleetDaemonCounter(t, f, "app", f.apps[1].ID) != 0 {
		t.Fatal("refused warm cache reached guest or changed app debt")
	}
}

type fleetDaemonRetryCounter struct {
	originals, retries int64
	expires            time.Time
}

func fleetDaemonRetries(t *testing.T, f fleetDaemonFixture, originals, retries int64) fleetDaemonRetryCounter {
	t.Helper()
	var counter fleetDaemonRetryCounter
	var live bool
	err := f.pool.QueryRow(t.Context(), "SELECT originals,retries,expires_at,expires_at>clock_timestamp() FROM traffic_retry_counters WHERE app_id=$1", f.apps[2].ID).
		Scan(&counter.originals, &counter.retries, &counter.expires, &live)
	if err != nil || !live || counter.originals != originals || counter.retries != retries {
		t.Fatalf("retry counter=%+v live=%v want=%d/%d err=%v", counter, live, originals, retries, err)
	}
	return counter
}

func TestTrafficFleetDaemonRetryBudgetSurvivesReplacement(t *testing.T) {
	f := newFleetDaemonFixture(t)
	first, second := f.process(t, 0), f.process(t, 1)
	for scope, subject := range map[string]string{"app": f.apps[2].ID, "account": f.app.AccountID} {
		if _, err := f.pool.Exec(t.Context(), "INSERT INTO pg_ratelimit_counters (scope,subject_id,plan,tokens,last_refill) VALUES ($1,$2,'pro',100,now()+interval '1 minute')", scope, subject); err != nil {
			t.Fatal(err)
		}
	}
	response, err := requestFleetDaemon(t.Context(), first.ready.Endpoint, f.apps[2].Host, "/retry")
	if err != nil || response.status != http.StatusOK {
		counter := fleetDaemonRetries(t, f, 1, 1)
		t.Fatalf("first replay status=%d body=%q err=%v RPC=%v guest=%d counter=%+v", response.status, response.body, err, f.vm.calls("/retry"), f.vm.guestCalls("/retry"), counter)
	}
	initial := fleetDaemonRetries(t, f, 1, 1)
	fleetDaemonCall(t, second, f.apps[2], "/retry", http.StatusServiceUnavailable)
	shared := fleetDaemonRetries(t, f, 2, 1)
	third := fleetDaemonReplacement(t, f, first)
	fleetDaemonCall(t, third, f.apps[2], "/retry", http.StatusServiceUnavailable)
	replaced := fleetDaemonRetries(t, f, 3, 1)
	if !initial.expires.Equal(shared.expires) || !initial.expires.Equal(replaced.expires) {
		t.Fatal("retry debt was not verified within one database-owned window")
	}
	calls := f.vm.calls("/retry")
	if len(calls) != 4 || calls[0] != f.vm.badInstance || calls[1] == f.vm.badInstance || calls[2] != f.vm.badInstance || calls[3] != f.vm.badInstance || f.vm.guestCalls("/retry") != 1 {
		t.Fatalf("shared replay: RPC=%v guest=%d", calls, f.vm.guestCalls("/retry"))
	}
	// An actual application 500 is an answer, so only one guest attempt runs.
	fleetDaemonCall(t, third, f.apps[2], "/retry?application=error", http.StatusInternalServerError)
	answered := fleetDaemonRetries(t, f, 4, 1)
	if !initial.expires.Equal(answered.expires) || len(f.vm.calls("/retry?application=error")) != 1 || f.vm.guestCalls("/retry?application=error") != 1 {
		t.Fatal("application error was replayed or crossed the observed retry window")
	}
	for scope, subject := range map[string]string{"app": f.apps[2].ID, "account": f.app.AccountID} {
		if got := fleetDaemonCounter(t, f, scope, subject); got != 96 {
			t.Fatalf("%s balance=%d want=96 for four originals and one retry", scope, got)
		}
	}
	t.Logf("one retry for four originals; three daemon generations share backend %s", third.ready.RetryBackendID)
}

func TestTrafficFleetDaemonRetryStoreFailureRefusesReplayAndRecovers(t *testing.T) {
	f := newFleetDaemonFixture(t)
	first, second := f.process(t, 0), f.process(t, 1)
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE traffic_retry_counters RENAME TO traffic_retry_counters_offline"); err != nil {
		t.Fatal(err)
	}
	fleetDaemonCall(t, first, f.apps[2], "/retry", http.StatusServiceUnavailable)
	if len(f.vm.calls("/retry")) != 1 || f.vm.guestCalls("/retry") != 0 {
		t.Fatal("unobserved original obtained a replay during retry-store failure")
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE traffic_retry_counters_offline RENAME TO traffic_retry_counters"); err != nil {
		t.Fatal(err)
	}
	fleetDaemonCall(t, second, f.apps[2], "/retry", http.StatusOK)
	initial := fleetDaemonRetries(t, f, 1, 1)
	third := fleetDaemonReplacement(t, f, first)
	fleetDaemonCall(t, third, f.apps[2], "/retry", http.StatusServiceUnavailable)
	recovered := fleetDaemonRetries(t, f, 2, 1)
	if !initial.expires.Equal(recovered.expires) || len(f.vm.calls("/retry")) != 4 || f.vm.guestCalls("/retry") != 1 {
		t.Fatal("retry-store recovery or replacement granted an extra replay")
	}
}

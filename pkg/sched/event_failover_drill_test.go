package sched_test

// adr: 20261029093000

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// eventFailoverDrillReport is what make drill-event-failover records.
type eventFailoverDrillReport struct {
	RatePerSecond        int     `json:"rate_per_second"`
	Acknowledged         int     `json:"acknowledged"`
	Survived             int     `json:"survived"`
	Lost                 int     `json:"lost"`
	LossWindowSeconds    float64 `json:"loss_window_seconds"`
	CrashToFirstAcceptS  float64 `json:"crash_to_first_accept_seconds"`
	ReceiptsSettled      int     `json:"receipts_settled"`
	ReceiptsUnsettled    int     `json:"receipts_unsettled"`
	RoutingSettleSeconds float64 `json:"routing_settle_seconds"`
}

type drillAck struct {
	id string
	at time.Time
}

// TestEventFailoverDrill is the event GA failover drill (ADR-20261029093000),
// run by scripts/drill-event-failover.sh against a disposable primary and its
// streaming standby. It publishes at the target rate, crashes the primary,
// promotes the standby, and reports what survived and how routing resumed.
func TestEventFailoverDrill(t *testing.T) {
	if os.Getenv("FAAS_RUN_EVENT_FAILOVER_DRILL") != "1" {
		t.Skip("set FAAS_RUN_EVENT_FAILOVER_DRILL=1 (make drill-event-failover)")
	}
	rate := drillEnvInt(t, "FAAS_EVENT_DRILL_RATE", 50)
	crashAfter := time.Duration(drillEnvInt(t, "FAAS_EVENT_DRILL_CRASH_AFTER_SECONDS", 30)) * time.Second
	ctx := context.Background()
	primary := drillPool(t, os.Getenv("FAAS_EVENT_DRILL_PRIMARY_URL"))
	if err := db.MigrateUp(ctx, primary); err != nil {
		t.Fatalf("migrate primary: %v", err)
	}
	store := state.NewPgStore(primary)
	account, err := store.CreateAccount(ctx, "failover-drill-"+uuid.NewString()+"@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "failover-drill",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.created", nil); err != nil {
		t.Fatal(err)
	}

	acks, crashedAt := drillPublishUntilCrash(t, store, account.ID, rate, crashAfter)
	primary.Close()
	drillRun(t, "FAAS_EVENT_DRILL_PROMOTE_CMD")
	standby := drillPool(t, os.Getenv("FAAS_EVENT_DRILL_STANDBY_URL"))
	promoted := state.NewPgStore(standby)
	firstAccept := drillFirstAccept(t, promoted, account.ID)

	report := eventFailoverDrillReport{RatePerSecond: rate, Acknowledged: len(acks), CrashToFirstAcceptS: firstAccept.Sub(crashedAt).Seconds()}
	var earliestLost time.Time
	for _, ack := range acks {
		var present bool
		if err := standby.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM event_fanout_outbox WHERE account_id=$1 AND source='orders' AND event_id=$2)`, account.ID, ack.id).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present {
			report.Survived++
			continue
		}
		report.Lost++
		if earliestLost.IsZero() || ack.at.Before(earliestLost) {
			earliestLost = ack.at
		}
	}
	if !earliestLost.IsZero() {
		report.LossWindowSeconds = crashedAt.Sub(earliestLost).Seconds()
	}
	report.ReceiptsSettled, report.ReceiptsUnsettled, report.RoutingSettleSeconds = drillRouteOnPromoted(t, promoted, standby, account.ID)
	drillWriteReport(t, report)
	if report.ReceiptsUnsettled != 0 {
		t.Fatalf("%d receipts did not settle on the promoted standby", report.ReceiptsUnsettled)
	}
}

// drillPublishUntilCrash publishes at rate until the crash command has run
// and publishing fails, returning every acknowledged event.
func drillPublishUntilCrash(t *testing.T, store *state.PgStore, account string, rate int, crashAfter time.Duration) ([]drillAck, time.Time) {
	t.Helper()
	var (
		mu        sync.Mutex
		acks      []drillAck
		crashedAt time.Time
	)
	start := time.Now()
	tick := time.NewTicker(time.Second / time.Duration(rate))
	defer tick.Stop()
	var inflight sync.WaitGroup
	failures := 0
	for n := 0; ; n++ {
		<-tick.C
		mu.Lock()
		crashed, failed := crashedAt, failures
		mu.Unlock()
		if crashed.IsZero() && time.Since(start) >= crashAfter {
			drillRun(t, "FAAS_EVENT_DRILL_CRASH_CMD")
			mu.Lock()
			crashedAt = time.Now()
			mu.Unlock()
		}
		if !crashed.IsZero() && (failed >= rate || time.Since(crashed) > 10*time.Second) {
			break
		}
		id := fmt.Sprintf("drill-%06d", n)
		payload := drillPayload(t, account, id)
		inflight.Add(1)
		go func() {
			defer inflight.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := store.AppendEvent(ctx, "apid", "event.published", &account, payload)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				acks = append(acks, drillAck{id: id, at: time.Now()})
			} else if !crashedAt.IsZero() {
				failures++
			}
		}()
	}
	inflight.Wait()
	return acks, crashedAt
}

// drillFirstAccept retries a publish on the promoted standby until one is
// accepted, returning when.
func drillFirstAccept(t *testing.T, store *state.PgStore, account string) time.Time {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := store.AppendEvent(ctx, "apid", "event.published", &account, drillPayload(t, account, "drill-after-"+uuid.NewString()))
		cancel()
		if err == nil {
			return time.Now()
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the promoted standby accepted no publish within 5 minutes")
	return time.Time{}
}

// drillRouteOnPromoted runs fanout sweeps on the promoted standby until every
// receipt of the account settles or a minute passes.
func drillRouteOnPromoted(t *testing.T, store *state.PgStore, pool *pgxpool.Pool, account string) (int, int, float64) {
	t.Helper()
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	start := time.Now()
	var settled, unsettled int
	for {
		loop.XRunEventFanoutSweep(ctx)
		if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='delivered'), count(*) FILTER (WHERE state<>'delivered')
			FROM event_fanout_outbox WHERE account_id=$1`, account).Scan(&settled, &unsettled); err != nil {
			t.Fatal(err)
		}
		if unsettled == 0 || time.Since(start) > time.Minute {
			return settled, unsettled, time.Since(start).Seconds()
		}
	}
}

func drillPayload(t *testing.T, account, id string) []byte {
	t.Helper()
	envelope, err := (events.Envelope{ID: id, Source: "orders", Type: "order.created", Data: json.RawMessage(`{}`)}).Normalize(account, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func drillPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	if dsn == "" {
		t.Fatal("FAAS_EVENT_DRILL_PRIMARY_URL and FAAS_EVENT_DRILL_STANDBY_URL are required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func drillRun(t *testing.T, env string) {
	t.Helper()
	command := os.Getenv(env)
	if command == "" {
		t.Fatalf("%s is required", env)
	}
	if out, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", env, err, out)
	}
}

func drillEnvInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("%s must be a positive integer", name)
	}
	return n
}

func drillWriteReport(t *testing.T, report eventFailoverDrillReport) {
	t.Helper()
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("event failover drill: %s", data)
	if dir := os.Getenv("FAAS_EVENT_DRILL_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "event-failover-drill.json"), append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// adr: 20261112140000
package commitmanaged

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type changesEnv struct {
	store    *state.PgStore
	control  *pgxpool.Pool
	customer *pgxpool.Pool
	account  state.Account
	app      state.App
	source   state.CommitSource
	manager  *Manager
}

func newChangesEnv(t *testing.T, withBinding bool) *changesEnv {
	t.Helper()
	ctx := t.Context()
	control := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, control); err != nil {
		t.Fatal(err)
	}
	customer := pgtest.OpenDatabase(t)
	store := state.NewPgStore(control)
	acct, err := store.CreateAccount(ctx, "changes-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "orders-worker-" + uuid.NewString()[:8], RAMMB: 512, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if withBinding {
		if _, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "order-changes",
			QueueName: "order-changes", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
			t.Fatal(err)
		}
	}
	feeds := []commitwork.ChangeFeed{{Name: "orders", Table: "public.orders", Columns: []string{"id", "status"}}}
	raw, _ := json.Marshal(feeds)
	src, err := store.UpsertCommitChangesSource(ctx, acct.ID, app.ID, "changes-"+app.Slug,
		state.CommitChangesConfig{QueueName: "order-changes", Feeds: raw, PollIntervalMS: 60000})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := commitwork.SealConnection(identity.Recipient(), src.ID, "postgres://relay:pw@db.example.com/shop?sslmode=verify-full")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetCommitSourceConnection(ctx, acct.ID, src.ID, sealed); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE public.orders (id int PRIMARY KEY, status text NOT NULL, secret text)`,
	} {
		if _, err := customer.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	pw, _ := commitwork.NewRelayPassword()
	tx, err := customer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := commitwork.InstallChanges(ctx, tx, commitwork.ChangesInstall{SourceID: src.ID, Feeds: feeds, RelayPassword: pw})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = customer.Exec(context.Background(), `DROP OWNED BY `+res.RelayRole)
		_, _ = customer.Exec(context.Background(), `DROP ROLE IF EXISTS `+res.RelayRole)
	})
	env := &changesEnv{store: store, control: control, customer: customer, account: acct, app: app, source: src}
	env.manager = &Manager{Store: store, Identities: []*age.X25519Identity{identity},
		Open: func(ctx context.Context, _ string, _ commitwork.NetworkPolicy) (*pgxpool.Pool, error) {
			return pgxpool.NewWithConfig(ctx, customer.Config().Copy())
		}}
	return env
}

func (e *changesEnv) pending(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.customer.QueryRow(t.Context(), `SELECT count(*) FROM public.gregale_outbox WHERE accepted_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *changesEnv) makeDue(t *testing.T) {
	t.Helper()
	if _, err := e.control.Exec(t.Context(), `UPDATE commit_sources SET relay_next_poll_at='-infinity',
 last_checked_at=clock_timestamp()-interval '2 minutes' WHERE id=$1::uuid`, e.source.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCommitRelayReservedConnectionDefersToGeneralWorker(t *testing.T) {
	e := newChangesEnv(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var checked, due time.Time
	if err := e.control.QueryRow(ctx, `UPDATE commit_sources SET
		last_checked_at=statement_timestamp()-interval '2 minutes',
		relay_next_poll_at=statement_timestamp()-interval '1 second'
		WHERE id=$1::uuid AND relay_status='healthy'
		RETURNING last_checked_at,relay_next_poll_at`, e.source.ID).Scan(&checked, &due); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders VALUES (1,'deferred',NULL)`); err != nil {
		t.Fatal(err)
	}
	open := e.manager.Open
	e.manager.Open = func(ctx context.Context, raw string, policy commitwork.NetworkPolicy) (*pgxpool.Pool, error) {
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return open(ctx, raw, policy)
		}
	}
	var status string
	var accepted int
	e.manager.Report = func(_ string, code string, n int) { status, accepted = code, n }
	reserved, err := e.store.ClaimHealthyCommitRelaySources(ctx, 1, state.CommitRelaySourceLease)
	if err != nil || len(reserved) != 1 {
		t.Fatalf("reserved claim: %d %v", len(reserved), err)
	}
	e.manager.pollSource(ctx, reserved[0], 25*time.Millisecond)
	if status != "reserved_connect_deferred" || accepted != 0 || e.pending(t) != 1 {
		t.Fatalf("reservation completion: status=%s accepted=%d", status, accepted)
	}
	var afterChecked, afterDue time.Time
	var backoff int
	if err := e.control.QueryRow(ctx, `SELECT last_checked_at,relay_next_poll_at,relay_poll_backoff_ms,relay_status
		FROM commit_sources WHERE id=$1::uuid`, e.source.ID).Scan(&afterChecked, &afterDue, &backoff, &status); err != nil {
		t.Fatal(err)
	}
	if !checked.Equal(afterChecked) || !due.Equal(afterDue) || backoff != 0 || status != "healthy" {
		t.Fatalf("handoff changed the poll schedule or observation: due=%v checked=%v backoff=%d status=%s", afterDue, afterChecked, backoff, status)
	}
	if claims, err := e.store.ClaimHealthyCommitRelaySources(ctx, 1, state.CommitRelaySourceLease); err != nil || len(claims) != 0 {
		t.Fatalf("reserved worker reclaimed the deferred source: %d %v", len(claims), err)
	}
	general, err := e.store.ClaimCommitRelaySources(ctx, 1, state.CommitRelaySourceLease)
	if err != nil || len(general) != 1 {
		t.Fatalf("general claim: %d %v", len(general), err)
	}
	if deferred, err := e.store.DeferCommitRelaySource(ctx, reserved[0]); err != nil || deferred {
		t.Fatalf("stale reservation changed the general worker's lease: %t %v", deferred, err)
	}
	e.manager.pollSource(ctx, general[0], 0)
	if status != "healthy" || accepted != 1 || e.pending(t) != 0 {
		t.Fatalf("general worker did not complete the event: status=%s accepted=%d", status, accepted)
	}
}

func TestCommitRelayAdaptiveReservedDeadline(t *testing.T) {
	manager := Manager{ReservedConnectBudget: FastReservedConnectBudget}
	for _, c := range []struct {
		cap      time.Duration
		observed int32
		want     time.Duration
	}{
		{0, 1250, 750 * time.Millisecond},
		{DefaultAdaptiveReservedConnectMax, 0, 750 * time.Millisecond},
		{DefaultAdaptiveReservedConnectMax, 500, 750 * time.Millisecond},
		{DefaultAdaptiveReservedConnectMax, 1250, 1500 * time.Millisecond},
		{DefaultAdaptiveReservedConnectMax, 1800, 2 * time.Second},
		{DefaultAdaptiveReservedConnectMax, 5000, 2 * time.Second},
		{1600 * time.Millisecond, 500, 750 * time.Millisecond},
		{1600 * time.Millisecond, 1258, 1508 * time.Millisecond},
		{1600 * time.Millisecond, 1800, 1600 * time.Millisecond},
		{750 * time.Millisecond, 1250, 750 * time.Millisecond},
	} {
		manager.AdaptiveReservedConnectMax = c.cap
		if got := manager.reservedConnectDeadline(c.observed); got != c.want {
			t.Fatalf("deadline with %s cap and %d ms observed: got %s, want %s", c.cap, c.observed, got, c.want)
		}
	}
}

func TestCommitRelayPollsHealthySourceWhileAnotherWaits(t *testing.T) {
	e := newChangesEnv(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	slow, err := e.store.CreateCommitSource(ctx, state.CommitSource{AccountID: e.account.ID, AppID: e.app.ID, Name: "slow-outbox"})
	if err != nil {
		t.Fatal(err)
	}
	const slowURL = "postgres://relay:fixture@slow.example.com/shop?sslmode=verify-full"
	sealed, err := commitwork.SealConnection(e.manager.Identities[0].Recipient(), slow.ID, slowURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetCommitSourceConnection(ctx, e.account.ID, slow.ID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := e.control.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at=clock_timestamp()+interval '1 hour' WHERE id=$1::uuid`, e.source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders VALUES (1, 'new', NULL)`); err != nil {
		t.Fatal(err)
	}

	slowStarted := make(chan struct{}, 1)
	releaseSlow := make(chan struct{})
	open := e.manager.Open
	e.manager.Open = func(ctx context.Context, url string, policy commitwork.NetworkPolicy) (*pgxpool.Pool, error) {
		if url != slowURL {
			return open(ctx, url, policy)
		}
		select {
		case slowStarted <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-releaseSlow:
			return nil, errors.New("fixture database unavailable")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	type pollReport struct {
		source, status string
		accepted       int
	}
	reports := make(chan pollReport, 16)
	e.manager.Report = func(source, status string, accepted int) {
		reports <- pollReport{source, status, accepted}
	}
	done := make(chan error, 1)
	go func() { done <- e.manager.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, ctx.Err()) {
			t.Errorf("relay shutdown: %v", err)
		}
	}()
	select {
	case <-slowStarted:
	case <-ctx.Done():
		t.Fatal("relay never started polling the slow source")
	}
	e.makeDue(t)
	select {
	case got := <-reports:
		if got != (pollReport{e.source.ID, "healthy", 1}) {
			t.Fatalf("first completed poll while slow source was blocked: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("healthy source did not finish while the slow source was blocked")
	}
	if claimed, err := e.store.ClaimCommitRelaySources(ctx, 2, state.CommitRelaySourceLease); err != nil || len(claimed) != 0 {
		t.Fatalf("another scheduler could claim the active slow source or the rescheduled healthy source: %d, %v", len(claimed), err)
	}
	close(releaseSlow)
	select {
	case got := <-reports:
		if got != (pollReport{slow.ID, "database_unavailable", 0}) {
			t.Fatalf("slow poll report: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("slow source did not record its failed poll")
	}
	var backoffMS, delayMS int64
	if err := e.control.QueryRow(ctx, `SELECT relay_poll_backoff_ms,
 (extract(epoch FROM relay_next_poll_at-last_checked_at)*1000)::bigint FROM commit_sources WHERE id=$1::uuid`, slow.ID).Scan(&backoffMS, &delayMS); err != nil {
		t.Fatal(err)
	}
	backoff, delay := time.Duration(backoffMS)*time.Millisecond, time.Duration(delayMS)*time.Millisecond
	if backoff != relayMinBackoff || delay != 5*time.Second+relayMinBackoff {
		t.Fatalf("failed poll backoff=%v next delay=%v", backoff, delay)
	}
}

func TestCommitRelayBackoffGrowsCapsAndResets(t *testing.T) {
	source := state.CommitRelaySource{CommitSource: state.CommitSource{ID: "first-source"}, PollBackoffMS: 5_000}
	if got := sourceBackoff(source, "database_unavailable", false, true); got != 10*time.Second {
		t.Fatalf("second failure backoff=%v", got)
	}
	source.PollBackoffMS = 160_000
	ceiling := sourceBackoff(source, "database_unavailable", false, true)
	if ceiling < relayTransportCeiling-relayTransportSpread || ceiling > relayTransportCeiling {
		t.Fatalf("transport backoff outside bounded range: %v", ceiling)
	}
	source.PollBackoffMS = int(ceiling.Milliseconds())
	if got := sourceBackoff(source, "database_unavailable", true, true); got != ceiling {
		t.Fatalf("transport ceiling changed on a subsequent failure: %v vs %v", got, ceiling)
	}
	source.ID = "second-source"
	if got := sourceBackoff(source, "database_unavailable", false, true); got == ceiling {
		t.Fatalf("transport ceilings did not spread across sources: %v", got)
	}
	source.PollBackoffMS = 160_000
	if got := sourceBackoff(source, "database_unavailable", false, false); got != relayMaxBackoff {
		t.Fatalf("unclassified database failure backoff=%v", got)
	}
	if got := sourceBackoff(source, "handoff_pending", true, false); got != relayMaxBackoff {
		t.Fatalf("timed-out poll backoff=%v", got)
	}
	if got := sourceBackoff(source, "healthy", false, false); got != 0 {
		t.Fatalf("healthy poll backoff=%v", got)
	}
}

func TestCommitRelayReportsSchemaRejection(t *testing.T) {
	e := newChangesEnv(t, true)
	ctx := t.Context()
	rejected, unrecognized := uuid.NewString(), uuid.NewString()
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload,blocked_code)
 VALUES ($1,'order.created','{}','event_schema_rejected'), ($2,'order.created','{}','unrecognized_reason')`, rejected, unrecognized); err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := e.store.ListCommitBlockedEvents(ctx, e.account.ID, e.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	codes := make(map[string]string)
	for _, item := range items {
		codes[item.EventID] = item.Code
	}
	if codes[rejected] != "event_schema_rejected" || codes[unrecognized] != "blocked" {
		t.Fatalf("unexpected blocked reason codes: %+v", codes)
	}
}

func TestChangesSourceDeliversToWorkerQueue(t *testing.T) {
	e := newChangesEnv(t, true)
	ctx := t.Context()
	for _, stmt := range []string{
		`INSERT INTO public.orders VALUES (1, 'new', 'hidden')`,
		`UPDATE public.orders SET status = 'paid' WHERE id = 1`,
	} {
		if _, err := e.customer.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("%d changes left in the outbox after a tick", n)
	}
	rows, err := e.control.Query(ctx, `SELECT source, queue_name, queue_binding_id IS NOT NULL, payload FROM invocations
 WHERE app_id = $1::uuid ORDER BY created_at, id`, e.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var source, queue string
		var bound bool
		var payload []byte
		if err := rows.Scan(&source, &queue, &bound, &payload); err != nil {
			t.Fatal(err)
		}
		if source != string(state.InvocationQueue) || queue != "order-changes" || !bound {
			t.Fatalf("delivered as source=%q queue=%q bound=%v", source, queue, bound)
		}
		if strings.Contains(string(payload), "hidden") {
			t.Fatalf("payload leaks a column that is not allowlisted: %s", payload)
		}
		var env struct {
			Source string          `json:"source"`
			Type   string          `json:"type"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(payload, &env); err != nil || env.Source != "gregale.commit."+e.source.ID {
			t.Fatalf("envelope %s (%v)", payload, err)
		}
		types = append(types, env.Type)
	}
	if strings.Join(types, ",") != "database.orders.insert,database.orders.update" {
		t.Fatalf("delivered %v", types)
	}

	// Inside the poll interval the source is not touched; after it, it is.
	if _, err := e.customer.Exec(ctx, `DELETE FROM public.orders WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.pending(t); n != 1 {
		t.Fatalf("source polled inside its interval: %d pending", n)
	}
	e.makeDue(t)
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("source not polled after its interval: %d pending", n)
	}
}

func TestChangesSourceWaitsForQueueBindingAndRefusesGenericEvents(t *testing.T) {
	e := newChangesEnv(t, false)
	ctx := t.Context()
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders VALUES (1, 'new', NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	// Without the binding the change is retried, not blocked.
	var blocked *string
	if err := e.customer.QueryRow(ctx, `SELECT blocked_code FROM public.gregale_outbox`).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if blocked != nil || e.pending(t) != 1 {
		t.Fatalf("missing binding blocked the change (%v) or dropped it", blocked)
	}
	// A changes source never takes events through the generic path.
	inv := state.Invocation{AccountID: e.account.ID, AppID: e.app.ID, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}
	if _, err := e.store.AcceptCommitEvent(ctx, e.account.ID, e.source.ID, uuid.NewString(), "x", json.RawMessage(`{}`), inv, 100); err == nil {
		t.Fatal("generic acceptance took an event for a changes source")
	}
	if _, err := e.store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: e.account.ID, AppID: e.app.ID, Name: "order-changes",
		QueueName: "order-changes", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `UPDATE public.gregale_outbox SET next_attempt_at = clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	e.makeDue(t)
	if err := e.manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("change not delivered once the binding exists: %d pending", n)
	}
	// The receipt replays unchanged.
	var event string
	if err := e.customer.QueryRow(ctx, `SELECT event_id::text FROM public.gregale_outbox`).Scan(&event); err != nil {
		t.Fatal(err)
	}
	first, err := e.store.CommitReceiptByEvent(ctx, e.account.ID, e.source.ID, event)
	if err != nil || first.InvocationID == "" {
		t.Fatalf("receipt %+v (%v)", first, err)
	}
	var kind string
	var data []byte
	if err := e.customer.QueryRow(ctx, `SELECT event_type, payload FROM public.gregale_outbox WHERE event_id = $1::uuid`, event).Scan(&kind, &data); err != nil {
		t.Fatal(err)
	}
	again, err := e.store.AcceptCommitChange(ctx, e.account.ID, e.source.ID, event, kind, data, state.Invocation{}, nil)
	if err != nil || again.ID != first.ID || again.InvocationID != first.InvocationID {
		t.Fatalf("replay %+v, first %+v (%v)", again, first, err)
	}
	if _, err := e.store.AcceptCommitChange(ctx, e.account.ID, e.source.ID, event, kind, json.RawMessage(`{"changed":true}`), state.Invocation{}, nil); err == nil {
		t.Fatal("a changed payload replayed under the same event id")
	}
}

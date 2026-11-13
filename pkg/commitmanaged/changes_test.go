// adr: 20261112140000
package commitmanaged

import (
	"context"
	"encoding/json"
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
	now      time.Time
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
	env := &changesEnv{store: store, control: control, customer: customer, account: acct, app: app, source: src, now: time.Now()}
	env.manager = &Manager{Store: store, Identities: []*age.X25519Identity{identity}, Now: func() time.Time { return env.now },
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
	e.now = e.now.Add(2 * time.Minute)
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
	e.now = e.now.Add(2 * time.Minute)
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

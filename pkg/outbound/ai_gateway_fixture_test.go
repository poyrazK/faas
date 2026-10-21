package outbound

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// aiPreviewFixture is a Pro account with an app bound to a customer OpenAI
// integration stored in Postgres, as a preview customer would set it up
// (ADR-998).
type aiPreviewFixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	store    *state.PgStore
	account  state.Account
	app      state.App
	offer    state.OutboundIntegrationOffer
	upstream *httptest.Server
}

func newAIPreviewFixture(t *testing.T, provider http.HandlerFunc, policy state.OutboundAIPolicy) *aiPreviewFixture {
	t.Helper()
	upstream := httptest.NewTLSServer(provider)
	t.Cleanup(upstream.Close)
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("ai-preview-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "ai-preview-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "llm", Origin: upstream.URL,
		AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: CredentialSourceCustomerSealed, OwnerKind: IntegrationOwnerCustomer,
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000,
			CircuitBreakerFailureThreshold: 3, CircuitBreakerOpenSeconds: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	policy.Provider = api.OutboundAIProviderOpenAI
	if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, policy); err != nil {
		t.Fatal(err)
	}
	return &aiPreviewFixture{t: t, pool: pool, store: store, account: account, app: app, offer: offer, upstream: upstream}
}

// handler serves the fixture's integration from Postgres; backend replaces
// the Postgres backend when set.
func (f *aiPreviewFixture) handler(rollout AIGatewayRollout, backend Backend) *Handler {
	f.t.Helper()
	return f.handlerOn(f.pool, rollout, backend)
}

// handlerOn is handler on another pool of the fixture's database.
func (f *aiPreviewFixture) handlerOn(pool *pgxpool.Pool, rollout AIGatewayRollout, backend Backend) *Handler {
	f.t.Helper()
	resolver, err := NewPostgresResolver(pool)
	if err != nil {
		f.t.Fatal(err)
	}
	if backend == nil {
		if backend, err = NewPostgresBackend(pool); err != nil {
			f.t.Fatal(err)
		}
	}
	handler, err := NewHandler(resolver, backend, f.upstream.Client())
	if err != nil {
		f.t.Fatal(err)
	}
	handler.AIGatewayEnabled, handler.AIGatewayRollout = true, rollout
	handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
	return handler
}

// tracedPool opens a pool of size conns on the fixture's database whose
// round trips the returned tracer counts.
func (f *aiPreviewFixture) tracedPool(conns int) (*pgxpool.Pool, *roundTripTracer) {
	f.t.Helper()
	config := f.pool.Config()
	config.MaxConns = int32(conns)
	tracer := &roundTripTracer{}
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(pool.Close)
	return pool, tracer
}

// spec is an admission of the fixture's app on its integration; Postgres
// reads the limits from the integration's stored policy.
func (f *aiPreviewFixture) spec() AdmissionSpec {
	return AdmissionSpec{IntegrationID: f.offer.ID, RatePerSecond: 100, Burst: 20, MaxInFlight: 20,
		LeaseTTL: 30 * time.Second, BindingAppID: f.app.ID}
}

// roundTripTracer counts a pool's round trips: one per statement sent on its
// own, including BEGIN and COMMIT, and one per pipelined batch.
type roundTripTracer struct{ roundTrips atomic.Int64 }

func (r *roundTripTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	r.roundTrips.Add(1)
	return ctx
}

func (*roundTripTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (r *roundTripTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	r.roundTrips.Add(1)
	return ctx
}

func (*roundTripTracer) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {}

func (*roundTripTracer) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}

// call sends one chat completion as the bound app through the relay.
func (f *aiPreviewFixture) call(ctx context.Context, handler *Handler, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	ctx = withRelayPrincipal(ctx, relayPrincipal{app: WorkloadIdentity{AccountID: f.account.ID, AppID: f.app.ID}})
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://outboundd"+Prefix+f.offer.ID+"/v1/chat/completions",
		strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

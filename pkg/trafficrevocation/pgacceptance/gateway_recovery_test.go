// adr: 531
package pgacceptance

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type securityServiceProvider struct {
	snapshot gateway.ServiceEndpointsSnapshot
}

func (p securityServiceProvider) ServiceEndpoints(context.Context, string) (gateway.ServiceEndpointsSnapshot, error) {
	return p.snapshot, nil
}

type securityHTTPResult struct {
	status int
	body   string
	err    error
}

func requestSecurityGateway(server *httptest.Server, path string) <-chan securityHTTPResult {
	result := make(chan securityHTTPResult, 1)
	go func() {
		resp, err := server.Client().Get(server.URL + "/v1/internal/services/target/" + path)
		if err != nil {
			result <- securityHTTPResult{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		result <- securityHTTPResult{status: resp.StatusCode, body: string(body), err: err}
	}()
	return result
}

func TestPGSecurityTwoGatewayHTTPMissedNotificationsOutageAndReplacement(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "security-gateway-recovery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "security-recovery", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	otherPool, err := pgxpool.NewWithConfig(ctx, pool.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer otherPool.Close()
	started := make(chan context.Context, 8)
	newGateway := func(source *pgxpool.Pool) (*httptest.Server, *trafficrevocation.Registry) {
		t.Helper()
		backend := state.NewPGTrafficSecurityBackend(source)
		if _, err := backend.Read(ctx, nil); err != nil {
			t.Fatal(err)
		}
		registry := trafficrevocation.New(backend)
		runCtx, stop := context.WithCancel(ctx)
		go registry.Run(runCtx) // deliberately no LISTEN subscriber: periodic repair must suffice
		t.Cleanup(func() { stop(); registry.Close() })
		proxy := gateway.NewServiceProxy(gateway.ServiceProxyConfig{
			TrafficRevocations:    registry,
			Provider:              securityServiceProvider{gateway.ServiceEndpointsSnapshot{AppID: app.ID, Endpoints: []gateway.ServiceEndpoint{{InstanceID: "fixture", NodeID: "node", Port: 8080, DeploymentID: deployment.ID}}}},
			ResolveCallerIdentity: func(context.Context, string) (string, string, error) { return app.ID, "", nil },
			Resolve: func(context.Context, string, string) (gateway.ServiceTarget, bool, error) {
				return gateway.ServiceTarget{AppID: app.ID}, true, nil
			},
			Authorize: func(context.Context, string, string) (gateway.ServiceCaller, error) {
				return gateway.ServiceCaller{AppID: app.ID, AccountID: account.ID}, nil
			},
			Forward: func(gateway.Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/fresh" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					started <- r.Context()
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("partial"))
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					panic(http.ErrAbortHandler) // forwarding ownership fixture; actual gRPC abort is tested in gateway
				})
			},
		})
		server := httptest.NewServer(proxy)
		server.Client().Timeout = 5 * time.Second
		t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
		return server, registry
	}
	first, firstRegistry := newGateway(pool)
	second, secondRegistry := newGateway(otherPool)
	waitStarted := func() context.Context {
		t.Helper()
		select {
		case ctx := <-started:
			return ctx
		case <-time.After(time.Second):
			t.Fatal("gateway did not dispatch")
			return nil
		}
	}
	waitAbort := func(result <-chan securityHTTPResult) {
		t.Helper()
		select {
		case got := <-result:
			if got.status != http.StatusOK || got.body != "partial" || got.err == nil {
				t.Fatalf("revoked exchange ended cleanly: %+v", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("gateway missed periodic security repair")
		}
	}
	one, two := requestSecurityGateway(first, "wait"), requestSecurityGateway(second, "wait")
	waitStarted()
	waitStarted()
	if held, err := store.SetAccountAbuseHold(ctx, account.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil || !held {
		t.Fatalf("hold=%v/%v", held, err)
	}
	if released, err := store.ReleaseAccountAbuseHold(ctx, account.ID); err != nil || !released {
		t.Fatalf("release=%v/%v", released, err)
	}
	waitAbort(one)
	waitAbort(two)
	// Both gateways verified generation zero. A release leaves generation two;
	// neither gateway receives a notification or may revive its old exchange.
	for _, registry := range []*trafficrevocation.Registry{firstRegistry, secondRegistry} {
		if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
			t.Fatalf("completed exchange retained registrations: %d/%d", exchanges, scopes)
		}
	}
	one, two = requestSecurityGateway(first, "wait"), requestSecurityGateway(second, "wait")
	firstCtx, secondCtx := waitStarted(), waitStarted()
	otherPool.Close() // only the second gateway loses its authoritative pool
	waitAbort(two)
	if firstCtx.Err() != nil && secondCtx.Err() != nil {
		t.Fatal("one gateway store failure canceled the healthy peer")
	}
	select {
	case got := <-one:
		t.Fatalf("healthy gateway stopped during peer outage: %+v", got)
	default:
	}
	refused := <-requestSecurityGateway(second, "fresh")
	if refused.status != http.StatusServiceUnavailable || refused.err != nil || !strings.Contains(refused.body, api.CodeTrafficRevocationUnavailable) {
		t.Fatalf("failed gateway used a private allow fallback: %+v", refused)
	}
	if held, err := store.SetAccountAbuseHold(ctx, account.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil || !held {
		t.Fatalf("second hold=%v/%v", held, err)
	}
	waitAbort(one)
	if released, err := store.ReleaseAccountAbuseHold(ctx, account.ID); err != nil || !released {
		t.Fatalf("second release=%v/%v", released, err)
	}
	replacement, _ := newGateway(pool)
	fresh := <-requestSecurityGateway(replacement, "fresh")
	if fresh.status != http.StatusNoContent || fresh.err != nil {
		t.Fatalf("replacement did not verify released durable generation: %+v", fresh)
	}
}

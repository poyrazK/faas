//go:build !no_pg

// adr: 570
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/state"
)

// The actual daemon and its Postgres policy reader serve HTTP. Guest execution
// is replaced by this counting dispatcher; this fixture proves ingress wiring.
type daemonIngressPolicyDispatcher struct{ calls atomic.Int64 }

func (d *daemonIngressPolicyDispatcher) Wake(context.Context, string) error {
	d.calls.Add(1)
	return nil
}

func (d *daemonIngressPolicyDispatcher) Invoke(_ context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	d.calls.Add(1)
	inv.State, inv.Result = state.InvocationCompleted, json.RawMessage(`{}`)
	return inv, nil
}

// daemonIngressPolicyKey signs the fixture's scheduler tokens: every synth
// request needs one (ADR-20261103090000).
var daemonIngressPolicyKey = func() ed25519.PrivateKey {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	return priv
}()

type daemonIngressPolicyVerifier struct{}

func (daemonIngressPolicyVerifier) AllowedSvcNames() []string { return []string{"schedd"} }

func (daemonIngressPolicyVerifier) Verify(_ context.Context, token string) (string, error) {
	return internalsvc.Verify(token, map[string]ed25519.PublicKey{"schedd": daemonIngressPolicyKey.Public().(ed25519.PublicKey)})
}

func daemonSchedulerToken(t *testing.T, appID string) string {
	t.Helper()
	token, err := internalsvc.Mint("schedd", 30*time.Second, map[string]any{"app_id": appID}, daemonIngressPolicyKey, "")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func assertDaemonSyntheticIngressPolicy(t *testing.T, url string, pool *pgxpool.Pool, store *state.PgStore, dispatcher *daemonIngressPolicyDispatcher) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "daemon-ingress-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	check := func(appID string, withToken bool, status int, wantCalls int64) {
		t.Helper()
		before := dispatcher.calls.Load()
		for _, route := range []struct{ path, body string }{
			{"/v1/synthesize", `{"app_id":%q,"path":"/"}`},
			{"/v1/invocations:dispatch", `{"app_id":%q,"invocation_id":"inv-1","source":"async_invoke"}`},
			{"/v1/invocations:dispatch_batch", `{"app_id":%q,"invocation_id":"inv-1","source":"queue","records":[{"id":"record-1"}]}`},
		} {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, url+route.path, strings.NewReader(fmt.Sprintf(route.body, appID)))
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			if withToken {
				request.Header.Set("Authorization", "Bearer "+daemonSchedulerToken(t, appID))
			}
			started := time.Now()
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			cancel()
			if readErr != nil || response.StatusCode != status || time.Since(started) > time.Second {
				t.Fatalf("%s response=%d %s err=%v time=%s", route.path, response.StatusCode, body, readErr, time.Since(started))
			}
			if status == http.StatusServiceUnavailable && (!strings.Contains(string(body), api.CodeTrafficPolicyUnavailable) || response.Header.Get("Retry-After") != "1") {
				t.Fatalf("unverifiable ingress lost stable retry contract: %s", body)
			}
		}
		if calls := dispatcher.calls.Load() - before; calls != wantCalls {
			t.Fatalf("status=%d dispatch calls=%d want=%d", status, calls, wantCalls)
		}
	}
	check(app.ID, true, http.StatusOK, 3)
	// A synth request without the scheduler's token is refused even for an
	// open app (ADR-20261103090000).
	check(app.ID, false, http.StatusForbidden, 0)
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetPublicAuth: true, PublicAuth: &state.AppPublicAuthUpdate{Mode: state.AppPublicAuthModeInternalOnly}}); err != nil {
		t.Fatal(err)
	}
	check(app.ID, false, http.StatusForbidden, 0)
	check(app.ID, true, http.StatusOK, 3)
	check(uuid.NewString(), true, http.StatusServiceUnavailable, 0)
	lock, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(t.Context()) }()
	if _, err := lock.Exec(t.Context(), "LOCK TABLE apps IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	check(app.ID, true, http.StatusServiceUnavailable, 0)
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(app.ID, false, http.StatusForbidden, 0)
	if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetPublicAuth: true, PublicAuth: &state.AppPublicAuthUpdate{Mode: state.AppPublicAuthModeOpen}}); err != nil {
		t.Fatal(err)
	}
	check(app.ID, true, http.StatusOK, 3)
}

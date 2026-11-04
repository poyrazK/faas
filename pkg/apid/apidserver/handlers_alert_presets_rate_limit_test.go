package apidserver_test

// adr: 20261030214500 — preset test sends are real webhook posts, so an
// account gets AlertPresetTestsPerAccountMinute of them, like channel tests.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSendTestAlertPresetCore_RateLimitedPerAccount(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(receiver.Close)

	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(ident.Recipient(), "alert_rule_secret", []byte("secret"), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	prev := *apidserver.HostIdentitiesForUnseal
	t.Cleanup(func() { *apidserver.HostIdentitiesForUnseal = prev })
	*apidserver.HostIdentitiesForUnseal = func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{ident} }

	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "preset-limit@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "preset-limit", RAMMB: 128, Runtime: "node22", Type: state.AppTypeFunction})
	if err != nil {
		t.Fatal(err)
	}
	store.SeedAlertPresetForTest(state.AlertPreset{
		Name: "api_down", DisplayName: "API is down", Category: "availability", Metric: "api_reachable",
		Comparison: "lt", Threshold: 1, WindowSpec: "5m", DefaultCooldownMinutes: 5, MinimumPlan: "pro", EnabledInCatalog: true,
	})
	if _, err := store.CreateAlertRule(ctx, state.AlertRule{
		AccountID: acct.ID, AppID: app.ID, Name: "API is down (preset-limit)", Metric: "api_reachable", Comparison: "lt",
		Threshold: 1, WindowSpec: "5m", CooldownMinutes: 5, Enabled: true, WebhookURL: receiver.URL, WebhookSecretSealed: sealed,
	}); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})

	for i := range api.AlertPresetTestsPerAccountMinute {
		if _, prob := srv.SendTestAlertPresetCore(ctx, acct, "preset-limit", "api_down"); prob != nil {
			t.Fatalf("test send %d refused: %+v", i+1, prob)
		}
	}
	_, prob := srv.SendTestAlertPresetCore(ctx, acct, "preset-limit", "api_down")
	if prob == nil || prob.Status != http.StatusTooManyRequests || prob.Code != api.CodeAlertPresetTestRateLimited {
		t.Fatalf("test send past the limit = %+v, want 429 %s", prob, api.CodeAlertPresetTestRateLimited)
	}
}

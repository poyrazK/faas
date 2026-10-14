package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDispatcherGitOpsAccountEventWithoutApp(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "gitops-hook@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	loader, sealed := identityForSealedBlob(t)
	received := make(chan []byte, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: account.ID, Scope: state.AppWebhookScopeAccount,
		TargetURL: target.URL, SecretSealed: sealed, EventFilter: []string{"environment.gitops.changed"}, Enabled: true, DeliveryFormat: state.AppWebhookDeliveryFormatCloudEvents}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := store.RecordAppWebhookDelivery(t.Context(), state.AppWebhookDelivery{WebhookID: hook.ID, AccountID: account.ID,
		Event: state.AppWebhookEventEnvironmentGitOpsChanged, Payload: json.RawMessage(`{"source_id":"source","axis":"drift","status":"drifted"}`)})
	if err != nil {
		t.Fatal(err)
	}
	d := NewDispatcher(store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.IdentityLoader, d.HTTPClient = loader, target.Client()
	d.Sleeper = (&recordingSleeper{}).Sleep
	d.cycle(context.Background())
	select {
	case body := <-received:
		var envelope struct {
			Source, Subject, Type string
			ID                    string `json:"id"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Source != "urn:gregale:account:"+account.ID || envelope.Subject != "accounts/"+account.ID || envelope.Type != "environment.gitops.changed" || envelope.ID != delivery.ID {
			t.Fatalf("wrong account event envelope: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("account-only GitOps delivery was not dispatched")
	}
}

// adr: 1155 — candidate assessment and customer status feedback contracts.
package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestDispatcherGitOpsAccountEventWithoutApp(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "gitops-hook@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	loader, sealed := identityForSealedBlob(t)
	type receivedRequest struct {
		body   []byte
		header http.Header
	}
	received := make(chan receivedRequest, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- receivedRequest{body: body, header: r.Header.Clone()}
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
	case request := <-received:
		body := request.body
		unix, err := strconv.ParseInt(request.header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if err != nil || request.header.Get("X-Faas-Delivery-Id") != delivery.ID || webhookout.NewSigner([]byte("test-secret")).Verify(unix, delivery.ID, body, strings.TrimPrefix(request.header.Get("X-Faas-Webhook-Signature"), "sha256=")) != nil {
			t.Fatal("account-only delivery was not signed over its exact body and identity")
		}
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
	d.inflight.Wait()
	completed, err := store.AppWebhookDeliveryByID(t.Context(), delivery.ID)
	if err != nil || completed.Status != state.AppWebhookDeliverySucceeded {
		t.Fatalf("account delivery did not complete: %+v %v", completed, err)
	}
}

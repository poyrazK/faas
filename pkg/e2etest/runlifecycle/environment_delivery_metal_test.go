//go:build metal

package runlifecycle_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func requireLiveEnvironmentBuild(t *testing.T, f *runFixture, id string, worker *builderd.Builderd) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(api.BuildTimeoutSeconds)*time.Second)
	defer cancel()
	done := make(chan struct{})
	var buildErr error
	go func() { _, buildErr = worker.ProcessNext(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("builder did not settle after diagnostic watch")
		}
	}()
	seen := int64(0)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("live build diagnostic deadline", ctx.Err())
		case <-done:
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if seen == 0 {
				t.Fatal("no package-manager diagnostic observed before build completion")
			}
			page, err := f.client.ExecutionEnvironmentEvents(t.Context(), id, seen)
			if err != nil || page.NextSequence < seen {
				t.Fatal("durable diagnostic replay", err)
			}
			for _, event := range page.Events {
				if event.Sequence <= seen {
					t.Fatal("diagnostic replay duplicated cursor")
				}
			}
			t.Logf("environment diagnostics: live_before_build_done=true resume_after=%d next_sequence=%d gap=%t", seen, page.NextSequence, page.Gap)
			return
		case <-ticker.C:
			if seen != 0 {
				continue
			}
			page, err := f.client.ExecutionEnvironmentEvents(ctx, id, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range page.Events {
				if event.Code == "build_output" {
					seen = event.Sequence
					break
				}
			}
		}
	}
}

type environmentCompletionReceiver struct {
	mu          sync.Mutex
	hookID      string
	held        string
	released    bool
	deliveryIDs map[string]string
	attempts    map[string]int
}

func newEnvironmentCompletionReceiver(t *testing.T, f *runFixture, store *state.PgStore) *environmentCompletionReceiver {
	t.Helper()
	c := &environmentCompletionReceiver{deliveryIDs: map[string]string{}, attempts: map[string]int{}}
	const signingSecret = "run-metal-completion-secret"
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		deliveryID := r.Header.Get("X-Faas-Delivery-Id")
		stamp, e := strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if e != nil || webhookout.NewSigner([]byte(signingSecret)).Verify(stamp, deliveryID, body, strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256=")) != nil {
			t.Error("invalid live completion signature")
			w.WriteHeader(401)
			return
		}
		var event struct {
			Type string                              `json:"type"`
			Data api.ExecutionFinishedWebhookPayload `json:"data"`
		}
		if json.Unmarshal(body, &event) != nil || event.Type != api.ExecutionFinishedWebhookEvent || event.Data.EventID == "" || event.Data.EventID != event.Data.ExecutionID || event.Data.ReceiptURL != "/v1/executions/"+event.Data.ExecutionID {
			t.Error("invalid live completion envelope")
			w.WriteHeader(400)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		id := event.Data.EventID
		if previous := c.deliveryIDs[id]; previous != "" && previous != deliveryID {
			t.Error("delivery retry changed identity")
			w.WriteHeader(400)
			return
		}
		c.deliveryIDs[id] = deliveryID
		c.attempts[id]++
		if c.attempts[id] == 1 || id == c.held && !c.released {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(receiver.Close)
	sealed, err := secretbox.SealBytes(f.webhookRecipient, api.AppWebhookSecretNamespace, []byte(signingSecret), api.AppWebhookSecretMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AccountByID(t.Context(), f.accountID)
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(account.Plan)
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: f.accountID, TargetURL: receiver.URL, SecretSealed: sealed, Enabled: true, EventFilter: []string{api.ExecutionFinishedWebhookEvent}, DeliveryFormat: state.AppWebhookDeliveryFormatCloudEvents}, limits)
	if err != nil {
		t.Fatal(err)
	}
	c.hookID = hook.ID
	return c
}
func (c *environmentCompletionReceiver) hold(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held = id
}
func (c *environmentCompletionReceiver) qualify(t *testing.T, f *runFixture, store *state.PgStore) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for {
		c.mu.Lock()
		held := c.held
		pendingWitness := c.attempts[held] > 0
		c.mu.Unlock()
		if pendingWitness {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion receiver never observed the held delivery")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := f.h.RestartSchedd(); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.released = true
	c.mu.Unlock()
	for {
		rows, _, err := store.ListAccountReleaseWebhookDeliveries(t.Context(), f.accountID, c.hookID, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		settled := len(rows) == len(f.runs)
		for _, row := range rows {
			settled = settled && row.Status == state.AppWebhookDeliverySucceeded
		}
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending completion deliveries did not settle after scheduler restart")
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.deliveryIDs) != len(f.runs) {
		t.Fatal("completion count mismatch")
	}
	for id := range f.runs {
		if c.attempts[id] < 2 {
			t.Fatal("missing signed retry", id)
		}
	}
	t.Logf("completion delivery: runs=%d signed=true retry=true scheduler_restart=true stable_identity=true", len(f.runs))
}

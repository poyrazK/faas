package product

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

func recoveryBody(sub productemail.Subscription) api.ProductEmailRecoveryRequest {
	yes := true
	return api.ProductEmailRecoveryRequest{ID: uuid.NewString(), ExpectedVersion: sub.Version, ExpectedGeneration: sub.Generation, WorkOutcomes: &yes, HelpReplies: &yes}
}
func TestProductEmailVerifiedReplacementRecovery(t *testing.T) {
	f := newEmailFixture(t)
	f.service.EmailWebhookSecret = mail.RandomResendSecretForTest()
	f.subscribe(t)
	providerIDs := []string{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerIDs = append(providerIDs, uuid.NewString())
		_ = json.NewEncoder(w).Encode(map[string]string{"id": providerIDs[len(providerIDs)-1]})
	}))
	defer provider.Close()
	var err error
	f.service.EmailTransport, err = productemail.NewResend(mail.ResendConfig{APIKey: "loopback-only", From: "fixture@example.test", BaseURL: provider.URL, HTTPClient: provider.Client()})
	if err != nil {
		t.Fatal(err)
	}
	f.reply(t)
	f.step(t)
	f.step(t)
	f.feedback(t, feedbackBody(providerIDs[0], "bounced", "Permanent"), "", 200)
	repo := f.store.ProductEmailRepository()
	sub, err := repo.Get(t.Context(), f.key)
	if err != nil || len(sub.PauseRecipient) == 0 {
		t.Fatal(sub, err)
	}
	request := recoveryBody(sub)
	f.call(t, "POST", "/email-recovery", request, 409) // The same verified address cannot clear its pause.
	f.address.Store("replacement@example.test")
	f.status.Store(403)
	f.call(t, "POST", "/email-recovery", request, 403) // Revoked worker authority prevents recovery.
	f.status.Store(200)
	w := f.call(t, "POST", "/email-recovery", request, 200)
	var receipt api.ProductEmailRecoveryReceipt
	if json.Unmarshal(w.Body.Bytes(), &receipt) != nil || !productemail.MatchesRecovery(receipt, request) {
		t.Fatal(w.Body.String())
	}
	f.call(t, "POST", "/email-recovery", request, 200) // Lost-response lookup must not renew consent or reset later state.
	current, err := repo.Get(t.Context(), f.key)
	if err != nil || current.Generation != 2 || current.PauseReason != "" {
		t.Fatal(current, err)
	}
	f.step(t)
	f.step(t)
	if len(providerIDs) != 1 {
		t.Fatal("recovery replayed old update", len(providerIDs))
	}
	f.reply(t)
	f.step(t)
	f.feedback(t, feedbackBody(providerIDs[0], "bounced", "Permanent"), "", 200)
	f.step(t)
	if len(providerIDs) != 2 {
		t.Fatal("old bounce cancelled new address", len(providerIDs))
	}
	w = f.call(t, "GET", "/email-recoveries", nil, 200)
	for _, private := range []string{"replacement@example.test", "verified-fixture@example.test", providerIDs[0], strings.Repeat("A", 43)} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private recovery projection", private)
		}
	}
	f.feedback(t, feedbackBody(providerIDs[0], "complained", ""), "", 200)
	f.call(t, "POST", "/email-recovery", request, 200)
	current, _ = repo.Get(t.Context(), f.key)
	if current.PauseReason != "complaint" {
		t.Fatal("receipt replay cleared complaint")
	}
	f.call(t, "POST", "/email-recovery", recoveryBody(current), 409)
	changed := request
	changed.ExpectedGeneration++
	f.call(t, "POST", "/email-recovery", changed, 409)
	f.call(t, "POST", "/email-recovery", map[string]any{"id": uuid.NewString(), "expected_version": 2, "expected_generation": 2, "work_outcomes": true, "help_replies": false, "email": "injected@example.test"}, 400)
	f.service.Enabled = false
	f.call(t, "POST", "/email-recovery", request, 404)
}
func TestProductEmailRecoveryLegacyEvidenceAndDecryption(t *testing.T) {
	f := newEmailFixture(t)
	f.subscribe(t)
	repo := f.store.ProductEmailRepository()
	sub, _ := repo.Get(t.Context(), f.key)
	now := time.Now().UTC()
	n := api.ProductNotification{ID: "work/" + uuid.NewString(), Kind: "work_failed", OccurredAt: now, Revision: strings.Repeat("b", 64)}
	if err := repo.Enqueue(t.Context(), sub, []api.ProductNotification{n}, now); err != nil {
		t.Fatal(err)
	}
	jobs, err := repo.Claim(t.Context(), now)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	id := uuid.NewString()
	if err = repo.Accept(t.Context(), jobs[0], id, now); err != nil {
		t.Fatal(err)
	}
	if err = repo.Feedback(t.Context(), productemail.Feedback{ProviderID: id, Kind: "bounced", HardBounce: true, OccurredAt: now}, now); err != nil {
		t.Fatal(err)
	}
	sub, _ = repo.Get(t.Context(), f.key)
	f.address.Store("replacement@example.test")
	f.call(t, "POST", "/email-recovery", recoveryBody(sub), 409)
	w := f.call(t, "GET", "/email-preferences", nil, 200)
	if !strings.Contains(w.Body.String(), "recipient_evidence_unavailable") {
		t.Fatal(w.Body.String())
	}
}

func TestProductEmailRecoverySerializesGrantRotation(t *testing.T) {
	f := newEmailFixture(t)
	f.service.EmailWebhookSecret = mail.RandomResendSecretForTest()
	f.subscribe(t)
	providerID := uuid.NewString()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": providerID})
	}))
	defer provider.Close()
	var err error
	f.service.EmailTransport, err = productemail.NewResend(mail.ResendConfig{APIKey: "loopback-only", From: "fixture@example.test", BaseURL: provider.URL, HTTPClient: provider.Client()})
	if err != nil {
		t.Fatal(err)
	}
	f.reply(t)
	f.step(t)
	f.step(t)
	f.feedback(t, feedbackBody(providerID, "bounced", "Permanent"), "", 200)
	sub, _ := f.store.ProductEmailRepository().Get(t.Context(), f.key)
	request := recoveryBody(sub)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var mu sync.Mutex
	currentToken := strings.Repeat("A", 43)
	consentCalls := 0
	auth := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/authz/v1/orgs" {
			_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": f.key.OrganizationID, "role": "owner", "name": "Customer"}}})
			return
		}
		value := emailRecipient{Protocol: 1, ActorID: f.key.ActorID, OrganizationID: f.key.OrganizationID, ProductID: f.app.ID, Role: "owner", Email: "replacement@example.test", ExpiresAt: time.Now().UTC().Add(time.Hour)}
		if r.Method == "POST" {
			mu.Lock()
			consentCalls++
			currentToken = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
			value.Token = currentToken
			first := consentCalls == 1
			mu.Unlock()
			if first {
				close(entered)
				<-release
			}
		}
		if strings.HasSuffix(r.URL.Path, "/"+f.app.ID) {
			mu.Lock()
			valid := r.Header.Get("Authorization") == "Product-Recipient "+currentToken
			mu.Unlock()
			if !valid {
				w.WriteHeader(403)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer auth.Close()
	f.service.AuthClient = auth.Client()
	f.service.AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
		return f.authApp, auth.URL, nil
	}
	send := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "https://portal.example/v1/product-control/customer/"+f.app.Slug+path, bytes.NewReader(raw))
		r.SetPathValue("slug", f.app.Slug)
		r.Header.Set("Origin", "https://portal.example")
		r.Header.Set("X-Product-Organization", f.key.OrganizationID)
		realm := customerRealm{app: f.app, authApp: f.authApp}
		r.AddCookie(&http.Cookie{Name: realm.cookieName("access"), Value: "customer.access.jwt"})
		w := httptest.NewRecorder()
		h := f.service.CustomerEmailRecover
		if method == "PUT" {
			h = f.service.CustomerEmailUpdate
		}
		f.service.Gate(f.service.Customer("", h))(w, r)
		return w
	}
	results := make(chan *httptest.ResponseRecorder, 8)
	var wg sync.WaitGroup
	wg.Go(func() { results <- send("POST", "/email-recovery", request) })
	<-entered
	for range 7 {
		wg.Go(func() { results <- send("POST", "/email-recovery", request) })
	}
	preferences := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		preferences <- send("PUT", "/email-preferences", map[string]any{"work_outcomes": true, "help_replies": true, "expected_version": sub.Version})
	}()
	close(release)
	wg.Wait()
	close(results)
	for w := range results {
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := <-preferences; w.Code != 409 {
		t.Fatal("stale preference save renewed grant", w.Code, w.Body.String())
	}
	mu.Lock()
	calls := consentCalls
	mu.Unlock()
	if calls != 1 {
		t.Fatal("duplicate confirmations rotated the saved grant", calls)
	}
	current, _ := f.store.ProductEmailRepository().Get(t.Context(), f.key)
	if _, e := f.service.emailResolve(t.Context(), current); e != nil {
		t.Fatal("recovery left an invalid recipient grant", e)
	}
}

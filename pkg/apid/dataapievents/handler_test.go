package dataapievents

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	app      state.App
	account  state.Account
	instance state.Instance
}

func (s fakeStore) AppByID(_ context.Context, id string) (state.App, error) {
	if id != s.app.ID {
		return state.App{}, state.ErrNotFound
	}
	return s.app, nil
}

func (s fakeStore) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	if id != s.instance.ID {
		return state.Instance{}, state.ErrNotFound
	}
	return s.instance, nil
}

func (s fakeStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	if id != s.account.ID {
		return state.Account{}, state.ErrNotFound
	}
	return s.account, nil
}

func (s fakeStore) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}

type fakePublisher struct {
	events   []appevents.AppProducerEvent
	outcome  func(int) api.PublishEventBatchResult
	notified int
}

func (p *fakePublisher) PublishAppProducerEvent(_ context.Context, _ state.Account, _ string, event appevents.AppProducerEvent) api.PublishEventBatchResult {
	p.events = append(p.events, event)
	if p.outcome != nil {
		return p.outcome(len(p.events) - 1)
	}
	return api.PublishEventBatchResult{Status: "accepted"}
}

func (p *fakePublisher) NotifyEventPublished(context.Context) { p.notified++ }

type fixture struct {
	handler   Handler
	publisher *fakePublisher
	signer    *workloadidentity.Signer
	store     *fakeStore
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "events-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	account, app := uuid.NewString(), uuid.NewString()
	store := &fakeStore{
		app:      state.App{ID: app, AccountID: account, Slug: "shop-data", Status: state.AppActive},
		account:  state.Account{ID: account, Status: state.AccountActive},
		instance: state.Instance{ID: uuid.NewString(), AppID: app, State: string(state.StateRunning)},
	}
	publisher := &fakePublisher{}
	return &fixture{handler: Handler{Verifier: verifier, Store: store, Publisher: publisher, Now: func() time.Time { return now }}, publisher: publisher, signer: signer, store: store}
}

func (f *fixture) token(t *testing.T, audience string) string {
	t.Helper()
	token, err := f.signer.Mint(now, f.store.account.ID, f.store.app.ID, f.store.instance.ID, audience)
	if err != nil {
		t.Fatal(err)
	}
	return token.AccessToken
}

func (f *fixture) serve(token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/runtime/data-api/events", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func event(n int) api.DataAPIChangeEvent {
	return api.DataAPIChangeEvent{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n), Type: "api.orders.inserted", Data: json.RawMessage(fmt.Sprintf(`{"key":{"id":%d}}`, n))}
}

func batch(t *testing.T, events ...api.DataAPIChangeEvent) string {
	t.Helper()
	body, err := json.Marshal(api.DataAPIChangeEventBatch{Version: 1, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func decode(t *testing.T, w *httptest.ResponseRecorder) api.DataAPIChangeEventBatchResponse {
	t.Helper()
	var out api.DataAPIChangeEventBatchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	return out
}

func TestHandlerPublishesUnderTheAppsOwnSourceAndKeys(t *testing.T) {
	f := newFixture(t)
	w := f.serve(f.token(t, workloadidentity.DataAPIEventsAudience), batch(t, event(1), event(2)))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	out := decode(t, w)
	if out.Source != "app."+f.store.app.ID || len(out.Results) != 2 || out.Results[1].Index != 1 || out.Results[1].Status != "accepted" {
		t.Fatalf("response %+v", out)
	}
	if got := f.publisher.events[0].Key; got != "data-api.change.00000000-0000-4000-8000-000000000001" {
		t.Fatalf("key %q", got)
	}
	if f.publisher.notified != 1 {
		t.Fatalf("notified %d times", f.publisher.notified)
	}
}

func TestHandlerAcceptsOnlyLiveWorkloadsOfActiveApps(t *testing.T) {
	body := batch(t, event(1))
	for _, tc := range []struct {
		name   string
		change func(*fixture)
		token  func(*testing.T, *fixture) string
	}{
		{"no token", nil, func(*testing.T, *fixture) string { return "" }},
		{"usage token", nil, func(t *testing.T, f *fixture) string { return f.token(t, workloadidentity.DataAPIUsageAudience) }},
		{"deleted app", func(f *fixture) { f.store.app.Status = state.AppDeleted }, nil},
		{"another account's app", func(f *fixture) { f.store.app.AccountID = uuid.NewString() }, nil},
		{"suspended account", func(f *fixture) { f.store.account.Status = state.AccountSuspended }, nil},
		{"parked instance", func(f *fixture) { f.store.instance.State = string(state.StateParked) }, nil},
		{"another app's instance", func(f *fixture) { f.store.instance.AppID = uuid.NewString() }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			token := f.token(t, workloadidentity.DataAPIEventsAudience)
			if tc.token != nil {
				token = tc.token(t, f)
			}
			if tc.change != nil {
				tc.change(f)
			}
			if w := f.serve(token, body); w.Code != http.StatusUnauthorized || len(f.publisher.events) != 0 {
				t.Fatalf("status %d, %d published", w.Code, len(f.publisher.events))
			}
		})
	}
}

func TestHandlerBoundsPublications(t *testing.T) {
	f := newFixture(t)
	token := f.token(t, workloadidentity.DataAPIEventsAudience)
	many := make([]api.DataAPIChangeEvent, api.DataAPIChangeEventsBatchMaxEvents+1)
	for i := range many {
		many[i] = event(i)
	}
	valid := batch(t, event(1))
	for name, tc := range map[string]struct {
		body   string
		status int
	}{
		"empty":         {batch(t), http.StatusBadRequest},
		"too many":      {batch(t, many...), http.StatusBadRequest},
		"version":       {strings.Replace(valid, `"version":1`, `"version":2`, 1), http.StatusBadRequest},
		"unknown field": {strings.Replace(valid, `"version":1`, `"version":1,"source":"x"`, 1), http.StatusBadRequest},
		"two values":    {valid + valid, http.StatusBadRequest},
		"too large":     {`{"version":1,"events":[{"id":"` + strings.Repeat("x", api.DataAPIChangeEventsBatchMaxBytes) + `"}]}`, http.StatusRequestEntityTooLarge},
	} {
		if w := f.serve(token, tc.body); w.Code != tc.status {
			t.Fatalf("%s: status %d: %s", name, w.Code, w.Body)
		}
	}
	if len(f.publisher.events) != 0 {
		t.Fatal("an invalid publication published events")
	}
}

func TestHandlerRejectsInvalidEventsAndStopsAtTheFirstRetryable(t *testing.T) {
	f := newFixture(t)
	f.publisher.outcome = func(n int) api.PublishEventBatchResult {
		if n == 2 {
			return api.PublishEventBatchResult{Status: "unknown", Retryable: true}
		}
		return api.PublishEventBatchResult{Status: "accepted"}
	}
	bad := event(2)
	bad.Type = "api.orders.created"
	huge := event(3)
	huge.Data = json.RawMessage(`"` + strings.Repeat("x", api.DataAPIChangeEventDataMaxBytes) + `"`)
	out := decode(t, f.serve(f.token(t, workloadidentity.DataAPIEventsAudience), batch(t, event(1), bad, huge, event(4), event(5), event(6))))
	want := []struct {
		status    string
		retryable bool
	}{{"accepted", false}, {"rejected", false}, {"rejected", false}, {"accepted", false}, {"unknown", true}, {"rejected", true}}
	for i, w := range want {
		if out.Results[i].Index != i || out.Results[i].Status != w.status || out.Results[i].Retryable != w.retryable {
			t.Fatalf("result %d = %+v, want %+v", i, out.Results[i], w)
		}
	}
	if out.Results[5].Problem == nil || out.Results[5].Problem.Code != "event_publish_not_attempted" {
		t.Fatalf("not attempted problem %+v", out.Results[5].Problem)
	}
	if len(f.publisher.events) != 3 {
		t.Fatalf("published %d events, want the valid ones up to the retryable failure", len(f.publisher.events))
	}
}

func TestChangeEventValidation(t *testing.T) {
	for name, e := range map[string]api.DataAPIChangeEvent{
		"uppercase id": {ID: "00000000-0000-4000-8000-00000000000A", Type: "api.t.inserted", Data: json.RawMessage(`{}`)},
		"type":         {ID: event(1).ID, Type: "api.t.created", Data: json.RawMessage(`{}`)},
		"space":        {ID: event(1).ID, Type: "api.my table.inserted", Data: json.RawMessage(`{}`)},
		"no data":      {ID: event(1).ID, Type: "api.t.deleted"},
		"bad json":     {ID: event(1).ID, Type: "api.t.updated", Data: json.RawMessage(`{`)},
	} {
		if e.Validate() == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if err := event(1).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUnavailablePublisher(t *testing.T) {
	f := newFixture(t)
	f.handler.Publisher = nil
	if w := f.serve(f.token(t, workloadidentity.DataAPIEventsAudience), batch(t, event(1))); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
	if _, err := appevents.AppProducerSource("not-a-uuid"); err == nil {
		t.Fatal("accepted an invalid app identity")
	}
}

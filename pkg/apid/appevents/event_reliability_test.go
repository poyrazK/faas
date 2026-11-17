package appevents_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appevents"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type auditHost struct{ store state.Store }

func (h auditHost) Store() state.Store                                         { return h.store }
func (h auditHost) WorkflowRuntimeEnabled() bool                               { return true }
func (h auditHost) Logger() *slog.Logger                                       { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (h auditHost) EmitAudit(context.Context, string, *string, map[string]any) {}
func (h auditHost) Notify(context.Context, string, string) error               { return nil }
func (h auditHost) PublishLimiter() *events.PublishLimiter                     { return nil }
func (h auditHost) LoadApp(w http.ResponseWriter, r *http.Request, a state.Account, slug string) (state.App, bool) {
	app, err := h.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != a.ID {
		w.WriteHeader(http.StatusNotFound)
		return state.App{}, false
	}
	return app, true
}

type companyFixture struct {
	store             state.Store
	service           appevents.Service
	creator           state.Account
	company, personal state.App
	org               state.Org
}

func newCompanyFixture(t *testing.T, s state.Store) companyFixture {
	t.Helper()
	ctx := t.Context()
	creator, err := s.CreateAccount(ctx, "creator-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateAccount(ctx, "owner-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateOrg(ctx, state.Org{Slug: "audit-" + uuid.NewString()[:8], Name: "Audit Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, &owner.ID); err != nil {
		t.Fatal(err)
	}
	newApp := func(prefix, orgID string) state.App {
		app, err := s.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: creator.ID, OrgID: orgID, Slug: prefix + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.(state.EventSubscriptionStore).UpsertEventSubscription(ctx, creator.ID, app.ID, "*", "*", nil); err != nil {
			t.Fatal(err)
		}
		return app
	}
	return companyFixture{store: s, service: appevents.Service{Host: auditHost{store: s}}, creator: creator, company: newApp("company-", org.ID), personal: newApp("personal-", ""), org: org}
}

func auditDataAPIChangeEventKeepsCompanyBoundary(t *testing.T, store state.Store) {
	f := newCompanyFixture(t, store)
	if err := f.store.RemoveOrgMember(t.Context(), f.org.ID, f.creator.ID); err != nil {
		t.Fatal(err)
	}
	result := f.service.PublishAppProducerEvent(t.Context(), f.creator, f.company.ID, appevents.AppProducerEvent{Key: api.DataAPIChangeEventKeyPrefix + uuid.NewString(), Type: "api.orders.inserted", Data: json.RawMessage(`{"private_customer":"company-secret"}`)})
	if result.Status != "accepted" {
		t.Fatalf("publication=%+v", result)
	}
	work, err := f.store.(state.PublishedEventWorkStore).ClaimDuePublishedEvent(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var envelope events.Envelope
	if err = json.Unmarshal(work.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	var leaked bool
	for _, recipient := range work.RecipientSnapshot {
		if recipient.AppID == f.personal.ID {
			leaked = true
		}
	}
	if leaked || envelope.Producer != eventcontract.AppProducer(f.company.ID) {
		t.Fatalf("Data API change event stored producer=%q and routed company payload to removed creator's personal app=%v; recipients=%+v", envelope.Producer, leaked, work.RecipientSnapshot)
	}
}

func auditRetentionHidesRevokedCompanyEvents(t *testing.T, store state.Store) {
	f := newCompanyFixture(t, store)
	envelope, err := (events.Envelope{ID: "private-company-order-492", Source: "company-orders", Type: "order.created", Producer: eventcontract.AppProducer(f.company.ID), Data: json.RawMessage(`{"id":492}`)}).Normalize(f.creator.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(envelope)
	if err = f.store.AppendEvent(t.Context(), "apid", "event.published", &f.creator.ID, payload); err != nil {
		t.Fatal(err)
	}
	work, err := f.store.(state.PublishedEventWorkStore).ClaimDuePublishedEvent(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, recipient := range work.RecipientSnapshot {
		if err = f.store.(state.PublishedEventRecipientProgressStore).RecordPublishedEventRecipientProgress(t.Context(), work.ID, work.ClaimToken, recipient.ID, state.PublishedEventRecipientProgress{State: state.PublishedEventRecipientFiltered, UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.store.(state.PublishedEventWorkStore).FinishPublishedEvent(t.Context(), work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	if err = f.store.RemoveOrgMember(t.Context(), f.org.ID, f.creator.ID); err != nil {
		t.Fatal(err)
	}
	receipt := httptest.NewRecorder()
	f.service.GetEventReceipt(receipt, httptest.NewRequest(http.MethodGet, "/v1/events/receipt?"+url.Values{"source": {envelope.Source}, "id": {envelope.ID}}.Encode(), nil), f.creator)
	if receipt.Code != http.StatusNotFound {
		t.Fatalf("control receipt should be hidden, got %d %s", receipt.Code, receipt.Body)
	}
	retention := httptest.NewRecorder()
	f.service.GetEventRetentionHealth(retention, httptest.NewRequest(http.MethodGet, "/v1/events/retention?window=720h", nil), f.creator)
	if retention.Code != http.StatusOK {
		t.Fatalf("retention=%d %s", retention.Code, retention.Body)
	}
	if strings.Contains(retention.Body.String(), envelope.ID) {
		t.Fatalf("receipt correctly returns 404 but retention exposes revoked company's event identity: %s", retention.Body)
	}
}

func auditAcceptedEventsRemainRetryableAfterSchemaRegistration(t *testing.T, store state.Store) {
	for _, mode := range []string{"single", "batch"} {
		t.Run(mode, func(t *testing.T) {
			s := store
			acct, err := s.CreateAccount(t.Context(), "schema-"+uuid.NewString()+"@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			service := appevents.Service{Host: auditHost{store: s}}
			body := `{"id":"accepted-before-schema","source":"orders","type":"order.created","data":{"id":1}}`
			first := httptest.NewRecorder()
			service.PublishEvent(first, httptest.NewRequest(http.MethodPost, "/v1/events:publish", strings.NewReader(body)), acct)
			if first.Code != http.StatusAccepted {
				t.Fatalf("first publication=%d %s", first.Code, first.Body)
			}
			_, err = s.(state.EventSchemaStore).PutEventSchema(t.Context(), state.EventSchema{AccountID: acct.ID, Source: "orders", Type: "order.created", Version: "v1", Schema: json.RawMessage(`{"type":"object"}`)})
			if err != nil {
				t.Fatal(err)
			}
			retry := httptest.NewRecorder()
			if mode == "single" {
				service.PublishEvent(retry, httptest.NewRequest(http.MethodPost, "/v1/events:publish", strings.NewReader(body)), acct)
				if retry.Code != http.StatusAccepted {
					t.Fatalf("same retained identity changed from 202 to %d after schema registration: %s", retry.Code, retry.Body)
				}
			} else {
				service.PublishEventBatch(retry, httptest.NewRequest(http.MethodPost, "/v1/events:publish-batch", strings.NewReader(`{"events":[`+body+`]}`)), acct)
				var response api.PublishEventBatchResponse
				if err = json.Unmarshal(retry.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Results) != 1 || response.Results[0].Status != "duplicate" {
					t.Fatalf("same retained identity rejected by batch after schema registration: %s", retry.Body)
				}
			}
		})
	}
}

func TestEventReliabilityDataAPIChangeEventKeepsCompanyBoundary(t *testing.T) {
	auditStores(t, auditDataAPIChangeEventKeepsCompanyBoundary)
}

func TestEventReliabilityRetentionHidesRevokedCompanyEvents(t *testing.T) {
	auditStores(t, auditRetentionHidesRevokedCompanyEvents)
}

func TestEventReliabilityAcceptedEventsRemainRetryableAfterSchemaRegistration(t *testing.T) {
	auditStores(t, auditAcceptedEventsRemainRetryableAfterSchemaRegistration)
}

func auditStores(t *testing.T, check func(*testing.T, state.Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestEventReliabilityDataAPIChangeEventsSupportRegisteredSchemas(t *testing.T) {
	auditStores(t, func(t *testing.T, store state.Store) {
		f := newCompanyFixture(t, store)
		source, err := appevents.AppProducerSource(f.company.ID)
		if err != nil {
			t.Fatal(err)
		}
		publish := func() api.PublishEventBatchResult {
			return f.service.PublishAppProducerEvent(t.Context(), f.creator, f.company.ID, appevents.AppProducerEvent{
				Key: api.DataAPIChangeEventKeyPrefix + uuid.NewString(), Type: "api.orders.inserted", Data: json.RawMessage(`{"key":{"id":1},"new":{"id":1}}`)})
		}
		if result := publish(); result.Status != "accepted" {
			t.Fatalf("control publication=%+v", result)
		}
		_, err = store.(state.EventSchemaStore).PutEventSchema(t.Context(), state.EventSchema{AccountID: f.creator.ID, Source: source, Type: "api.orders.inserted", Version: "v1", Schema: json.RawMessage(`{"type":"object"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if result := publish(); result.Status != "rejected" || result.Retryable {
			t.Fatalf("new unversioned event bypassed registered schema: %+v", result)
		}
		result := f.service.PublishAppProducerEvent(t.Context(), f.creator, f.company.ID, appevents.AppProducerEvent{
			Key: api.DataAPIChangeEventKeyPrefix + uuid.NewString(), Type: "api.orders.inserted", SchemaVersion: "v1", Data: json.RawMessage(`{"key":{"id":1},"new":{"id":1}}`)})
		if result.Status != "accepted" {
			t.Fatalf("versioned Data API change rejected: %+v", result)
		}
	})
}

func TestEventReliabilityPublishRejectsHTTPUnsafeIdentity(t *testing.T) {
	auditStores(t, func(t *testing.T, store state.Store) {
		account, err := store.CreateAccount(t.Context(), "headers-"+uuid.NewString()+"@example.test", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		service := appevents.Service{Host: auditHost{store: store}}
		for _, field := range []string{"id", "type"} {
			t.Run(field, func(t *testing.T) {
				value := map[string]any{"id": "order-1", "source": "orders", "type": "order.created", "data": map[string]any{"id": 1}}
				value[field] = "order\ncreated"
				body, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				response := httptest.NewRecorder()
				service.PublishEvent(response, httptest.NewRequest(http.MethodPost, "/v1/events:publish", strings.NewReader(string(body))), account)
				if response.Code == http.StatusAccepted {
					t.Fatalf("HTTP-unsafe %s was durably accepted for delivery: status=%d response=%s", field, response.Code, response.Body)
				}
				if response.Code != http.StatusUnprocessableEntity && response.Code != http.StatusBadRequest {
					t.Fatalf("unexpected admission status=%d response=%s", response.Code, response.Body)
				}
			})
		}
	})
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercises API-created intent and credentials through the gateway, with a
// warm HTTP guest substitute. No database, scheduler or KVM is needed.
func TestPlatformTenantAPIToGatewayIdentityLifecycle(t *testing.T) {
	e := setup(t, api.PlanHobby)
	const slug = "customer-api"
	required, operatorAuth := true, false
	created := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, PlatformTenantRequired: &required, RequireAuthn: &operatorAuth,
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", created.Code, created.Body.String())
	}
	consumerRec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/consumers", api.CreateAPIConsumerRequest{ExternalRef: "customer-42", Name: "Customer 42"}, nil)
	if consumerRec.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", consumerRec.Code, consumerRec.Body.String())
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(consumerRec.Body.Bytes(), &consumer); err != nil {
		t.Fatal(err)
	}
	key := seedPlatformTenantKey(t, e, slug, consumer.ID)
	var guestCalls atomic.Int32
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guestCalls.Add(1)
		_, _ = w.Write([]byte(r.Header.Get(api.PlatformTenantIDHeader)))
	}))
	defer guest.Close()
	backend := &tenantIngressBackend{store: e.store, slug: slug, address: guest.Listener.Addr().String()}
	edge := gateway.NewHandlerWith(backend, gateway.NewMetrics(), nil).WithConsumerAuth(tenantIngressConsumerStore{e.store})
	request := func(token string, status int, tenantID string) {
		t.Helper()
		before := guestCalls.Load()
		r := httptest.NewRequest(http.MethodGet, "http://customer-api.gregale.dev/resource", nil)
		r.Header.Set(api.PlatformTenantIDHeader, "forged-customer")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		edge.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("ingress: %d %s, want %d", w.Code, w.Body.String(), status)
		}
		if status == http.StatusOK {
			if w.Body.String() != tenantID || guestCalls.Load() != before+1 {
				t.Fatalf("guest identity=%q calls=%d", w.Body.String(), guestCalls.Load()-before)
			}
		} else if guestCalls.Load() != before {
			t.Fatal("denied request reached guest")
		}
	}
	request("", http.StatusForbidden, "")
	request(key.Key, http.StatusForbidden, "")
	createTenant := e.do(t, http.MethodPost, "/v1/account/platform-tenants", api.CreatePlatformTenantRequest{ExternalRef: "customer-42", Name: "Customer 42"}, nil)
	if createTenant.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d %s", createTenant.Code, createTenant.Body.String())
	}
	var tenant api.PlatformTenantResponse
	if err := json.Unmarshal(createTenant.Body.Bytes(), &tenant); err != nil {
		t.Fatal(err)
	}
	link := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/consumers", api.LinkPlatformTenantConsumerRequest{ConsumerID: consumer.ID}, nil)
	if link.Code != http.StatusOK {
		t.Fatalf("link consumer: %d %s", link.Code, link.Body.String())
	}
	request(key.Key, http.StatusOK, tenant.ID)
	for _, status := range []string{"suspended", "active"} {
		changed := e.do(t, http.MethodPatch, "/v1/account/platform-tenants/"+tenant.ID, api.SetPlatformTenantStatusRequest{Status: status}, nil)
		if changed.Code != http.StatusOK {
			t.Fatalf("tenant status: %d %s", changed.Code, changed.Body.String())
		}
		want := http.StatusOK
		if status == "suspended" {
			want = http.StatusUnauthorized
		}
		request(key.Key, want, tenant.ID)
	}
}

// These adapters project persisted intent and credentials using the gateway's
// public interfaces, including the store's tenant suspension checks.
type tenantIngressBackend struct {
	store   *state.MemStore
	slug    string
	address string
}

func (b *tenantIngressBackend) Lookup(ctx context.Context, _ string) (gateway.App, bool) {
	app, err := b.store.AppBySlug(ctx, b.slug)
	if err != nil {
		return gateway.App{}, false
	}
	account, err := b.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return gateway.App{}, false
	}
	return gateway.App{ID: app.ID, AccountID: app.AccountID, Plan: account.Plan,
		RequireAuthn: app.RequireAuthn, PlatformTenantRequired: app.PlatformTenantRequired,
		ConsumerAuthMode: string(app.ConsumerAuthMode), PublicAuth: gateway.PublicAuthConfig{Mode: app.PublicAuthMode},
		MaxConcurrency: 1}, true
}

func (b *tenantIngressBackend) Pick(string) gateway.PickResult {
	return gateway.PickResult{OK: true, Target: gateway.Target{NodeID: b.address, InstanceID: "warm-test-guest"}}
}
func (*tenantIngressBackend) HealthyCount(string) int { return 1 }
func (*tenantIngressBackend) Admit(context.Context, string, string, string, string, int) (string, gateway.WakeMethod, bool, error) {
	return "", gateway.WakeMethodUnspecified, true, nil
}
func (*tenantIngressBackend) LookupMirrorRules(context.Context, string) ([]gateway.MirrorRuleRow, bool) {
	return nil, false
}
func (*tenantIngressBackend) ScheduleMirror(context.Context, string, string, string) (string, string, error) {
	return "", "", errors.New("unexpected mirror scheduling")
}

type tenantIngressConsumerStore struct{ *state.MemStore }

func (s tenantIngressConsumerStore) ConsumerKeyByAppAndPrefix(ctx context.Context, accountID, appID, prefix string) (gateway.ConsumerAuthKey, error) {
	key, err := s.MemStore.ConsumerKeyByAppAndPrefix(ctx, accountID, appID, prefix)
	if errors.Is(err, state.ErrNotFound) {
		err = gateway.ErrConsumerAuthNotFound
	}
	return gateway.ConsumerAuthKey{ID: key.ID, AccountID: key.AccountID, AppID: key.AppID, ConsumerID: key.ConsumerID,
		Prefix: key.Prefix, Hash: key.Hash, Scopes: key.Scopes, ExpiresAt: key.ExpiresAt, RevokedAt: key.RevokedAt}, err
}
func (s tenantIngressConsumerStore) GetAPIConsumerByID(ctx context.Context, accountID, id string) (gateway.ConsumerAuthConsumer, error) {
	c, err := s.MemStore.GetAPIConsumerByID(ctx, accountID, id)
	if errors.Is(err, state.ErrNotFound) {
		err = gateway.ErrConsumerAuthNotFound
	}
	return gateway.ConsumerAuthConsumer{ID: c.ID, AccountID: c.AccountID, AppID: c.AppID, PlatformTenantID: c.PlatformTenantID,
		Status: string(c.Status), RevokedAt: c.RevokedAt}, err
}

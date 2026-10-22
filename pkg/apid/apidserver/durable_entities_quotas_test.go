package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/state"
)

func entityQuotaFixture(t *testing.T) (entityHTTPEnv, state.App, *entityDispatchFixture, *entitysocket.Quotas) {
	t.Helper()
	e, app, dispatch, bucket := entityAPIFixture(t, false)
	engine, err := durableentity.Open(t.Context(), bucket, durableentity.Options{RetainedBytesLimit: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	e.s.Durable().Engine = engine
	q := &entitysocket.Quotas{Store: bucket, Limits: entitysocket.TenantLimits{Connections: 1, GrantsPerMinute: 1, InvocationsPerMinute: 1, RetainedBytes: 16 << 20, EntityBytes: 1 << 20}}
	e.s.Durable().TenantQuotas = q
	e.s.Durable().Sockets = entitysocket.New()
	e.s.Durable().SessionGrants = &entitysocket.Grants{Store: bucket, Quotas: q}
	enabled := true
	if _, err = e.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWebSocketEnabled: true, WebSocketEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	return e, app, dispatch, q
}
func TestDurableEntitySocketTenantQuotaHTTPAdmissionAndUsage(t *testing.T) {
	e, app, dispatch, _ := entityQuotaFixture(t)
	path := "/v1/apps/" + app.Slug + "/entities/invoke"
	first := e.do(t, "POST", path, entityRequest("first"), nil)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	next := entityRequest("next")
	next.Namespace = "another-namespace"
	next.Key = "another-key"
	for _, request := range []api.DurableEntityInvokeRequest{next, entityRequest("first")} {
		response := e.do(t, "POST", path, request, nil)
		var problem api.Problem
		if response.Code != 429 || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.Code != "durable_entity_tenant_quota" || problem.Limit == nil || *problem.Limit != 1 || response.Header().Get("Retry-After") == "" {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	id := durableentity.ID{AccountID: e.acct.ID, AppID: app.ID, EnvironmentID: app.ID, Namespace: "counters", Key: "customer:456"}
	_, restoreErr := e.s.Durable().PerformRestore(t.Context(), e.acct, app, "prod", id, api.DurableEntityRestoreRequest{})
	var quota *entitysocket.QuotaError
	if !errors.As(restoreErr, &quota) || quota.Budget != "invocations" {
		t.Fatal("restore bypassed tenant admission", restoreErr)
	}
	if dispatch.calls.Load() != 1 {
		t.Fatal("quota failure dispatched", dispatch.calls.Load())
	}
	status := e.do(t, "GET", "/v1/apps/"+app.Slug+"/entities/quotas?namespace=counters&key=customer:456", nil, nil)
	var usage entitysocket.TenantUsage
	if status.Code != 200 || json.Unmarshal(status.Body.Bytes(), &usage) != nil || usage.Invocations != 1 || usage.EntityReservations != 1 || usage.ReservedBytes < 1<<20 {
		t.Fatal(status.Code, status.Body.String())
	}
}
func TestDurableEntitySocketTenantQuotaSharedBrowserAndServiceConnections(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	session := uuid.NewString()
	input := map[string]any{"namespace": "counters", "key": "a", "session_id": session, "generation": 0, "origin": "https://example.com", "commands": []string{"open"}, "ttl_seconds": 60}
	response := e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/session-grants", input, nil)
	var grant struct {
		ID         string `json:"grant_id"`
		Credential string `json:"credential"`
	}
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &grant) != nil || strings.Count(grant.Credential, ".") != 2 {
		t.Fatal(response.Code, response.Body.String())
	}
	second := e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/session-grants", input, nil)
	if second.Code != 429 {
		t.Fatal(second.Code, second.Body.String())
	}
	server := httptest.NewServer(e.h)
	defer server.Close()
	base := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/apps/" + app.Slug
	d := websocket.Dialer{Subprotocols: []string{entitysocket.BrowserProtocol, entitysocket.CredentialProtocol + grant.Credential}}
	conn, handshake, err := d.Dial(base+"/entities/session-connect", http.Header{"Origin": []string{"https://example.com"}})
	if handshake != nil {
		handshake.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, key := range []string{"a", "different-object"} {
		c, r, err := websocket.DefaultDialer.Dial(base+"/entities/connect?namespace=counters&key="+key, http.Header{"Authorization": []string{"Bearer " + e.key}})
		if r != nil {
			r.Body.Close()
		}
		if err == nil {
			c.Close()
			t.Fatal("service socket bypassed shared tenant capacity")
		} else if r == nil || r.StatusCode != 429 {
			t.Fatal(r, err)
		}
	}
	if err = conn.WriteJSON(entitysocket.Frame{ProtocolVersion: 1, RequestID: "session/g0/" + session + "/open", Payload: json.RawMessage(`{"kind":"open","session_id":"` + session + `","generation":0}`)}); err != nil {
		t.Fatal(err)
	}
	var reply entitysocket.Reply
	if err = conn.ReadJSON(&reply); err != nil || reply.Status != 200 {
		t.Fatal(reply, err)
	}
	if err = conn.WriteJSON(entitysocket.Frame{ProtocolVersion: 1, RequestID: "session/g0/" + session + "/open", Payload: json.RawMessage(`{"kind":"open","session_id":"` + session + `","generation":0}`)}); err != nil {
		t.Fatal(err)
	}
	if err = conn.ReadJSON(&reply); err != nil || reply.Status != 429 || dispatch.calls.Load() != 1 {
		t.Fatal("socket replay traffic bypassed rate", reply, err)
	}
	if status := e.do(t, "DELETE", "/v1/apps/"+app.Slug+"/entities/session-grants/"+grant.ID, nil, nil); status.Code != 204 {
		t.Fatal(status.Code, status.Body.String())
	}
	if err = conn.WriteJSON(entitysocket.Frame{ProtocolVersion: 1, RequestID: "session/g0/" + session + "/open", Payload: json.RawMessage(`{"kind":"open","session_id":"` + session + `","generation":0}`)}); err != nil {
		t.Fatal(err)
	}
	if err = conn.ReadJSON(&reply); err != nil || reply.Status != 403 {
		t.Fatal("quota-backed grant revocation", reply, err)
	}
	// Wait only for asynchronous socket teardown, bounded by the test deadline.
	id := durableentity.ID{AccountID: e.acct.ID, AppID: app.ID, EnvironmentID: app.ID, Namespace: "counters", Key: "a"}
	deadline := time.Now().Add(2 * time.Second)
	for {
		usage, err := q.Inspect(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if usage.Connections == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shared slot leaked", usage)
		}
		time.Sleep(time.Millisecond)
	}
	service, serviceResponse, err := websocket.DefaultDialer.Dial(base+"/entities/connect?namespace=counters&key=different-object", http.Header{"Authorization": []string{"Bearer " + e.key}})
	if serviceResponse != nil {
		serviceResponse.Body.Close()
	}
	if err != nil {
		t.Fatal("released slot not reusable", err)
	}
	service.Close()
}
func TestDurableEntitySocketTenantQuotaUncertainAdmissionNeverDispatches(t *testing.T) {
	e, app, dispatch, q := entityQuotaFixture(t)
	// The fixture store simulates a committed CAS whose acknowledgement is lost.
	q.Store = &entityQuotaLostStore{q.Store}
	response := e.do(t, "POST", "/v1/apps/"+app.Slug+"/entities/invoke", entityRequest("uncertain-admission"), nil)
	if response.Code != 503 || dispatch.calls.Load() != 0 {
		t.Fatal("uncertain admission dispatched", response.Code, dispatch.calls.Load())
	}
}
func TestDurableEntitySocketTenantQuotaConfiguration(t *testing.T) {
	limits, err := durable.TenantLimits(func(string) string { return "" }, 1<<20)
	if err != nil || limits.Connections != 8 || limits.GrantsPerMinute != 60 || limits.InvocationsPerMinute != 120 {
		t.Fatal(limits, err)
	}
	for _, entry := range []struct{ name, value string }{
		{"FAAS_DURABLE_ENTITY_TENANT_MAX_CONNECTIONS", "0"},
		{"FAAS_DURABLE_ENTITY_TENANT_MAX_CONNECTIONS", "33"},
		{"FAAS_DURABLE_ENTITY_TENANT_GRANTS_PER_MINUTE", "01"},
		{"FAAS_DURABLE_ENTITY_TENANT_INVOCATIONS_PER_MINUTE", "10001"},
		{"FAAS_DURABLE_ENTITY_TENANT_MAX_RETAINED_BYTES", "1024"},
	} {
		if _, err = durable.TenantLimits(func(name string) string {
			if name == entry.name {
				return entry.value
			}
			return ""
		}, 1<<20); err == nil {
			t.Fatal(entry)
		}
	}
	if _, err = durable.TenantLimits(func(string) string { return "" }, 0); err == nil {
		t.Fatal("unbounded entity storage admitted")
	}
	e := entityAPISetup(t)
	err = e.s.ConfigureDurableEntities(t.Context(), func(name string) string {
		if name == "FAAS_DURABLE_ENTITY_TENANT_QUOTAS_ENABLED" {
			return "1"
		}
		return ""
	})
	if err == nil {
		t.Fatal("quota flag missing invocation preview accepted")
	}
	var quota *entitysocket.QuotaError
	if errors.As(err, &quota) {
		t.Fatal("configuration should not return an admission error")
	}
}

type entityQuotaLostStore struct{ durableentity.ObjectStore }

func (s *entityQuotaLostStore) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	next, err := s.ObjectStore.Put(ctx, key, body, version)
	if err == nil && strings.HasPrefix(key, "tenant-quotas/v1/") {
		return "", errors.New("lost quota write acknowledgement")
	}
	return next, err
}

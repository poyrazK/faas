package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/triggerconfig"
)

func installTriggerSecretIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	previousRecipient := setSecretRecipient
	previousIdentities := mfaIdentities
	previousIdentity := mfaIdentity
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	mfaIdentity = func() *age.X25519Identity { return identity }
	t.Cleanup(func() {
		setSecretRecipient = previousRecipient
		mfaIdentities = previousIdentities
		mfaIdentity = previousIdentity
	})
	return identity
}

func kafkaConfig(password string) json.RawMessage {
	return json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g","sasl":{"mechanism":"PLAIN","username":"svc","password":"` + password + `"}}`)
}

func createKafkaTriggerForSecretTest(t *testing.T, e testEnv, password string) api.Trigger {
	t.Helper()
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "secret-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{
		AppID: app.ID, Kind: api.TriggerKindKafka, Slug: "orders", Config: kafkaConfig(password),
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var trigger api.Trigger
	if err := json.Unmarshal(rec.Body.Bytes(), &trigger); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return trigger
}

func TestTriggerResponseRedactsKafkaSecrets(t *testing.T) {
	row := sqlc.Trigger{
		Kind:   string(api.TriggerKindKafka),
		Config: []byte(`{"sasl":{"username":"svc","password":"plain","password_sealed":"YQ=="},"tls":{"client_cert":"cert","client_key":"key","client_key_sealed":"Yg=="}}`),
	}
	body, err := json.Marshal(triggerResponse(row))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, forbidden := range []string{"plain", `"password"`, `"password_sealed"`, `"client_key"`, `"client_key_sealed"`} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(string(body), `"password_set":true`) || !strings.Contains(string(body), `"client_key_set":true`) {
		t.Fatalf("response omitted set markers: %s", body)
	}
}

func TestCreateTriggerSealsStoredCredentialsAndRedactsResponse(t *testing.T) {
	identity := installTriggerSecretIdentity(t)
	e := setup(t, api.PlanPro)
	trigger := createKafkaTriggerForSecretTest(t, e, "create-secret")
	if bytes.Contains(trigger.Config, []byte("create-secret")) || !bytes.Contains(trigger.Config, []byte(`"password_set":true`)) {
		t.Fatalf("customer config was not redacted: %s", trigger.Config)
	}
	stored, err := e.store.TriggerByID(t.Context(), trigger.ID)
	if err != nil {
		t.Fatalf("TriggerByID: %v", err)
	}
	if bytes.Contains(stored.Config, []byte("create-secret")) || !bytes.Contains(stored.Config, []byte("password_sealed")) {
		t.Fatalf("stored config was not sealed: %s", stored.Config)
	}
	opened, err := triggerconfig.Open(api.TriggerKindKafka, stored.Config, []*age.X25519Identity{identity})
	if err != nil || !bytes.Contains(opened, []byte("create-secret")) {
		t.Fatalf("Open stored config = %s, %v", opened, err)
	}
}

func TestCreateTriggerFailsClosedWithoutSecretRecipient(t *testing.T) {
	previous := setSecretRecipient
	setSecretRecipient = func() *age.X25519Recipient { return nil }
	t.Cleanup(func() { setSecretRecipient = previous })
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "secret-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{
		AppID: app.ID, Kind: api.TriggerKindKafka, Slug: "orders", Config: kafkaConfig("must-not-store"),
	}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodeSecretStoreUnavailable || strings.Contains(rec.Body.String(), "must-not-store") {
		t.Fatalf("problem = %+v; body=%s", problem, rec.Body.String())
	}
}

func TestBatchCreateTriggerSealsCredentialsAndRedactsResponse(t *testing.T) {
	installTriggerSecretIdentity(t)
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "batch-secret-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/v1/triggers:batch_create", api.CreateTriggerBatchRequest{
		AppID: app.ID,
		ManifestYAML: `triggers:
  - kind: kafka
    app: batch-secret-app
    slug: orders
    config:
      brokers: ["b:9092"]
      topic: orders
      group: g
      sasl:
        mechanism: PLAIN
        username: svc
        password: batch-secret
`,
	}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "batch-secret") || !strings.Contains(rec.Body.String(), `"password_set":true`) {
		t.Fatalf("batch response was not redacted: %s", rec.Body.String())
	}
	stored, err := e.store.ListTriggersForApp(t.Context(), app.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("ListTriggersForApp = %d, %v", len(stored), err)
	}
	if bytes.Contains(stored[0].Config, []byte("batch-secret")) || !bytes.Contains(stored[0].Config, []byte("password_sealed")) {
		t.Fatalf("batch stored config was not sealed: %s", stored[0].Config)
	}
}

func TestUpdateTriggerPreservesRotatesAndRemovesKafkaPassword(t *testing.T) {
	identity := installTriggerSecretIdentity(t)
	e := setup(t, api.PlanPro)
	trigger := createKafkaTriggerForSecretTest(t, e, "old-secret")
	before, err := e.store.TriggerByID(t.Context(), trigger.ID)
	if err != nil {
		t.Fatalf("TriggerByID before patch: %v (id=%q)", err, trigger.ID)
	}
	if _, err := e.store.AppByID(t.Context(), uuidFromPgtype(before.AppID).String()); err != nil {
		t.Fatalf("AppByID before patch: %v (app_id=%q)", err, uuidFromPgtype(before.AppID).String())
	}

	preserveConfig := json.RawMessage(`{"brokers":["new:9092"],"topic":"orders","group":"g","sasl":{"mechanism":"PLAIN","username":"new-user","password_set":true}}`)
	rec := e.do(t, http.MethodPatch, "/v1/triggers/"+trigger.ID, api.UpdateTriggerRequest{Config: preserveConfig}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preserve status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stored, err := e.store.TriggerByID(t.Context(), trigger.ID)
	if err != nil {
		t.Fatalf("TriggerByID preserve: %v", err)
	}
	opened, err := triggerconfig.Open(api.TriggerKindKafka, stored.Config, []*age.X25519Identity{identity})
	if err != nil || !bytes.Contains(opened, []byte("old-secret")) || !bytes.Contains(opened, []byte("new-user")) {
		t.Fatalf("preserved config = %s, %v", opened, err)
	}

	rec = e.do(t, http.MethodPatch, "/v1/triggers/"+trigger.ID, api.UpdateTriggerRequest{Config: kafkaConfig("new-secret")}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stored, _ = e.store.TriggerByID(t.Context(), trigger.ID)
	opened, err = triggerconfig.Open(api.TriggerKindKafka, stored.Config, []*age.X25519Identity{identity})
	if err != nil || bytes.Contains(opened, []byte("old-secret")) || !bytes.Contains(opened, []byte("new-secret")) {
		t.Fatalf("rotated config = %s, %v", opened, err)
	}

	withoutSASL := json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`)
	rec = e.do(t, http.MethodPatch, "/v1/triggers/"+trigger.ID, api.UpdateTriggerRequest{Config: withoutSASL}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	stored, _ = e.store.TriggerByID(t.Context(), trigger.ID)
	opened, err = triggerconfig.Open(api.TriggerKindKafka, stored.Config, []*age.X25519Identity{identity})
	if err != nil || bytes.Contains(opened, []byte("sasl")) || bytes.Contains(opened, []byte("new-secret")) {
		t.Fatalf("removed config = %s, %v", opened, err)
	}
}

func TestUpdateTriggerCorruptStoredSecretReturnsSanitizedProblem(t *testing.T) {
	installTriggerSecretIdentity(t)
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "corrupt-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	created, err := e.store.CreateTriggerIfUnderQuota(t.Context(), app.ID, string(api.TriggerKindKafka), "orders", true,
		[]byte(`{"brokers":["b:9092"],"topic":"orders","group":"g","sasl":{"mechanism":"PLAIN","username":"svc","password_sealed":"bm90LWFnZQ=="}}`),
		64, 1000, 5, 6_291_456, "commit", limits)
	if err != nil {
		t.Fatalf("CreateTriggerIfUnderQuota: %v", err)
	}
	id := uuidFromPgtype(created.ID).String()
	if _, err := e.store.TriggerByID(t.Context(), id); err != nil {
		t.Fatalf("TriggerByID before corrupt patch: %v (id=%q, raw=%q)", err, id, created.ID.String())
	}
	rec := e.do(t, http.MethodPatch, "/v1/triggers/"+id, api.UpdateTriggerRequest{
		Config: json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g","sasl":{"mechanism":"PLAIN","username":"svc","password_set":true}}`),
	}, nil)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "bm90LWFnZQ") {
		t.Fatalf("status/body = %d %s, want sanitized 503", rec.Code, rec.Body.String())
	}
}

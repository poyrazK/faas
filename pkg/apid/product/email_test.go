package product

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

type emailFixture struct {
	*customerRecoveryFixture
	key     productemail.Key
	scope   state.ProductNotificationScope
	status  atomic.Int32
	role    atomic.Value
	actor   atomic.Value
	address atomic.Value
	sink    string
}

func newEmailFixture(t *testing.T) *emailFixture {
	t.Helper()
	f := &emailFixture{customerRecoveryFixture: newCustomerRecoveryFixture(t)}
	f.status.Store(200)
	f.role.Store("owner")
	tenant, _, err := f.store.CreatePlatformTenant(t.Context(), f.account.ID, "email-customer", "Email customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	f.key = productemail.Key{AccountID: f.account.ID, AppID: f.app.ID, TenantID: tenant.ID, AuthAppID: f.authApp.ID, OrganizationID: uuid.NewString(), ActorID: uuid.NewString()}
	f.actor.Store(f.key.ActorID)
	f.address.Store("verified-fixture@example.test")
	if _, err = f.store.SetProductCustomer(t.Context(), f.account.ID, f.app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: f.key.OrganizationID}); err != nil {
		t.Fatal(err)
	}
	f.scope = state.ProductNotificationScope{ProductPlanScope: state.ProductPlanScope{AccountID: f.key.AccountID, AppID: f.key.AppID, TenantID: f.key.TenantID, AuthAppID: f.key.AuthAppID, AuthApp: f.authApp.Slug, OrganizationID: f.key.OrganizationID, Role: "owner"}}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/authz/v1/orgs" {
			_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": f.key.OrganizationID, "name": "Customer", "role": f.role.Load().(string)}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/"+f.app.ID) {
			if r.Header.Get("Authorization") != "Product-Recipient "+strings.Repeat("A", 43) {
				t.Error("worker did not use scoped recipient grant")
			}
			if status := int(f.status.Load()); status != 200 {
				w.WriteHeader(status)
				return
			}
		} else if r.Header.Get("Authorization") != "Bearer customer.access.jwt" {
			w.WriteHeader(401)
			return
		}
		v := emailRecipient{Protocol: 1, ActorID: f.actor.Load().(string), OrganizationID: f.key.OrganizationID, ProductID: f.app.ID, Role: f.role.Load().(string), Email: f.address.Load().(string), ExpiresAt: time.Now().UTC().Add(time.Hour)}
		if r.Method == "POST" {
			v.Token = strings.Repeat("A", 43)
		}
		_ = json.NewEncoder(w).Encode(v)
	}))
	t.Cleanup(upstream.Close)
	f.service.AuthClient = upstream.Client()
	f.service.AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
		return f.authApp, upstream.URL, nil
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f.service.EmailSeal = func(id, token string) ([]byte, error) { return productemail.Seal(identity, id, token) }
	f.service.EmailOpen = func(id string, data []byte) (string, error) {
		return productemail.Open([]*age.X25519Identity{identity}, id, data)
	}
	f.sink = filepath.Join(t.TempDir(), "sink")
	f.service.EmailTransport, err = productemail.NewLocal(f.sink)
	if err != nil {
		t.Fatal(err)
	}
	f.service.EmailOrigin = "https://portal.example"
	return f
}
func (f *emailFixture) call(t *testing.T, method, path string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	var data bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&data).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "https://portal.example/v1/product-control/customer/"+f.app.Slug+path, &data)
	r.SetPathValue("slug", f.app.Slug)
	r.Header.Set("Origin", "https://portal.example")
	r.Header.Set("X-Product-Organization", f.key.OrganizationID)
	realm := customerRealm{app: f.app, authApp: f.authApp}
	r.AddCookie(&http.Cookie{Name: realm.cookieName("access"), Value: "customer.access.jwt"})
	h := f.service.CustomerEmailPreferences
	if method == "PUT" {
		h = f.service.CustomerEmailUpdate
	}
	if path == "/email-recovery" {
		h = f.service.CustomerEmailRecover
	}
	if strings.HasPrefix(path, "/email-recoveries") {
		h = f.service.CustomerEmailRecoveries
	}
	if strings.HasPrefix(path, "/email-opt-outs") {
		h = f.service.CustomerEmailOptOuts
	}
	if path == "/email-deliveries" {
		h = f.service.CustomerEmailDeliveries
	}
	w := httptest.NewRecorder()
	f.service.Gate(f.service.Customer("", h))(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable email API")
	}
	return w
}
func (f *emailFixture) subscribe(t *testing.T) {
	f.call(t, "PUT", "/email-preferences", map[string]any{"work_outcomes": true, "help_replies": true, "expected_version": 0}, 200)
}
func (f *emailFixture) reply(t *testing.T) api.ProductHelpRequest {
	t.Helper()
	help, err := f.store.CreateProductHelp(t.Context(), state.ProductHelpScope(f.scope), api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: "PRIVATE_CUSTOMER_QUESTION"})
	if err != nil {
		t.Fatal(err)
	}
	help, err = f.store.UpdateProductHelp(t.Context(), f.account.ID, f.app.ID, help.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "PRIVATE_HELP_RESPONSE"})
	if err != nil {
		t.Fatal(err)
	}
	return help
}
func (f *emailFixture) step(t *testing.T) {
	t.Helper()
	if _, err := f.service.EmailStep(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
}
func TestProductEmailPersonalConsentAndLocalDelivery(t *testing.T) {
	f := newEmailFixture(t)
	f.reply(t)
	f.subscribe(t)
	f.step(t) // no historic email
	if rows, _ := f.store.ProductEmailRepository().Recent(t.Context(), f.key); len(rows) != 0 {
		t.Fatal("old inbox items emailed")
	}
	f.reply(t)
	f.step(t)
	f.step(t)
	f.step(t)
	rows, err := f.store.ProductEmailRepository().Recent(t.Context(), f.key)
	if err != nil || len(rows) != 1 || rows[0].Status != "captured" || rows[0].Attempts != 1 {
		t.Fatal(rows, err)
	}
	files, err := os.ReadDir(f.sink)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	data, err := os.ReadFile(filepath.Join(f.sink, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE_HELP_RESPONSE", "PRIVATE_CUSTOMER_QUESTION", "customer.access.jwt", strings.Repeat("A", 43)} {
		if bytes.Contains(data, []byte(private)) {
			t.Fatal("private content in message")
		}
	}
	if !bytes.Contains(data, []byte("https://portal.example/v1/product-control/portal?app=")) {
		t.Fatal("missing safe portal link")
	}
	w := f.call(t, "GET", "/email-deliveries", nil, 200)
	if !strings.Contains(w.Body.String(), "captured") || strings.Contains(w.Body.String(), "@example.test") {
		t.Fatal(w.Body.String())
	}
	f.actor.Store(uuid.NewString())
	w = f.call(t, "GET", "/email-preferences", nil, 200)
	var p api.ProductEmailPreferences
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Version != 0 || p.WorkOutcomes {
		t.Fatal("shared personal preferences", w.Body.String())
	}
}
func TestProductEmailRechecksBeforeSending(t *testing.T) {
	for _, change := range []string{"membership", "role", "realm", "mapping", "optout", "source"} {
		t.Run(change, func(t *testing.T) {
			f := newEmailFixture(t)
			f.subscribe(t)
			help := f.reply(t)
			f.step(t)
			switch change {
			case "membership":
				f.status.Store(403)
			case "role":
				f.role.Store("unknown")
			case "realm":
				f.spec.AuthApp = "other-auth"
				if _, err := f.store.PutProductCatalog(t.Context(), f.account.ID, f.app.ID, f.spec); err != nil {
					t.Fatal(err)
				}
			case "mapping":
				if _, err := f.store.SetProductCustomer(t.Context(), f.account.ID, f.app.ID, api.ProductCustomer{TenantID: f.key.TenantID, Plan: "starter", AuthOrganizationID: uuid.NewString()}); err != nil {
					t.Fatal(err)
				}
			case "optout":
				f.call(t, "PUT", "/email-preferences", map[string]any{"work_outcomes": false, "help_replies": false, "expected_version": 1}, 200)
			case "source":
				if _, err := f.store.UpdateProductHelp(t.Context(), f.account.ID, f.app.ID, help.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 2, Status: "resolved", Response: "NEW_PRIVATE_RESPONSE"}); err != nil {
					t.Fatal(err)
				}
			}
			f.step(t)
			files, _ := os.ReadDir(f.sink)
			if len(files) != 0 {
				t.Fatal("sent after change", change)
			}
			rows, err := f.store.ProductEmailRepository().Recent(t.Context(), f.key)
			if err != nil || len(rows) < 1 || rows[len(rows)-1].Status != "cancelled" {
				t.Fatal(rows, err)
			}
		})
	}
}
func TestProductEmailAuthOutageAndDisabledGate(t *testing.T) {
	f := newEmailFixture(t)
	f.subscribe(t)
	f.reply(t)
	f.step(t)
	f.status.Store(503)
	_, _ = f.service.EmailStep(t.Context(), "")
	rows, err := f.store.ProductEmailRepository().Recent(t.Context(), f.key)
	if err != nil || len(rows) != 1 || rows[0].Status != "queued" || rows[0].Reason != "authorization_unavailable" {
		t.Fatal(rows, err)
	}
	f.service.Enabled = false
	f.step(t)
	f.call(t, "GET", "/email-preferences", nil, 404)
	files, _ := os.ReadDir(f.sink)
	if len(files) != 0 {
		t.Fatal("disabled worker sent")
	}
}
func TestProductEmailProviderRetriesHaveStableKeyAndAcceptanceStatus(t *testing.T) {
	f := newEmailFixture(t)
	f.subscribe(t)
	f.reply(t)
	f.step(t)
	var calls int
	var key string
	var message []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if key == "" {
			key = r.Header.Get("Idempotency-Key")
		}
		if key == "" || key != r.Header.Get("Idempotency-Key") {
			t.Error("changed idempotency key")
		}
		var value mail.ResendRequest
		_ = json.NewDecoder(r.Body).Decode(&value)
		data, _ := json.Marshal(value)
		if message != nil && !bytes.Equal(message, data) {
			t.Error("changed retry message")
		}
		message = data
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"id":"ca098456-6358-4018-b047-f7000be7fb66"}`))
	}))
	defer provider.Close()
	var err error
	f.service.EmailTransport, err = productemail.NewResend(mail.ResendConfig{APIKey: "local-fixture-only", From: "fixture@example.test", BaseURL: provider.URL, HTTPClient: provider.Client()})
	if err != nil {
		t.Fatal(err)
	}
	repo := f.store.ProductEmailRepository()
	jobs, err := repo.Claim(t.Context(), time.Now())
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	d := jobs[0]
	if err = f.service.deliverEmail(t.Context(), repo, d); err != nil {
		t.Fatal(err)
	}
	jobs, err = repo.Claim(t.Context(), time.Now().Add(api.ProductControlEmailRetryBase+time.Second))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	if err = f.service.deliverEmail(t.Context(), repo, jobs[0]); err != nil {
		t.Fatal(err)
	}
	rows, _ := repo.Recent(t.Context(), f.key)
	if len(rows) != 1 || rows[0].Status != "provider_accepted" || rows[0].Attempts != 2 || calls != 2 {
		t.Fatal(rows, calls)
	}
}

func TestProductEmailWorkOutcomePreservesUsage(t *testing.T) {
	f := newEmailFixture(t)
	f.subscribe(t)
	dep, err := f.store.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:email-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := f.store.PutOperationDefinition(t.Context(), state.OperationDefinition{AccountID: f.account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: f.app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"exporting"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := f.store.AdmitOperation(t.Context(), state.OperationAdmission{AccountID: f.account.ID, DefinitionID: def.ID, PlatformTenantID: f.key.TenantID, IdempotencyKey: uuid.NewString(), Input: json.RawMessage(`{}`), ProductControlEnabled: true, ProductFeature: "export", ProductAuthOrganizationID: f.key.OrganizationID, ProductAuthApp: f.authApp.Slug})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CancelOperation(t.Context(), f.account.ID, f.key.TenantID, op.ID, op.Generation); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.GetProductUsage(t.Context(), f.account.ID, f.app.ID, f.key.TenantID, time.Now().UTC().Format("2006-01"))
	if err != nil {
		t.Fatal(err)
	}
	f.step(t)
	f.step(t)
	rows, err := f.store.ProductEmailRepository().Recent(t.Context(), f.key)
	if err != nil || len(rows) != 1 || rows[0].Kind != "work_cancelled" || rows[0].Status != "captured" {
		t.Fatal(rows, err)
	}
	after, err := f.store.GetProductUsage(t.Context(), f.account.ID, f.app.ID, f.key.TenantID, before.Period)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if !bytes.Equal(a, b) {
		t.Fatal("notification changed commercial usage")
	}
}

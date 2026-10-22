// adr: 1139
package projectsecrets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsecrets"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeHost struct {
	store  *state.MemStore
	kid    string
	events []string
	data   []map[string]any
}

func (h *fakeHost) Store() state.Store { return h.store }

func (h *fakeHost) EmitAudit(_ context.Context, kind string, _ *string, data map[string]any) {
	h.events = append(h.events, kind)
	h.data = append(h.data, data)
}

func (h *fakeHost) Seal(key, value string, maxBytes int) (projectsecrets.Sealed, *api.Problem) {
	if len(value) > maxBytes {
		return projectsecrets.Sealed{}, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeSecretValueTooLarge, "Secret value too large", "too large")
	}
	return projectsecrets.Sealed{Ciphertext: []byte("sealed:" + key + "=" + value), Kid: h.kid, ValueHash: fmt.Sprintf("%x", len(value))}, nil
}

type fixture struct {
	host    *fakeHost
	mux     *http.ServeMux
	account state.Account
	project state.Project
}

func newFixture(t *testing.T, plan api.Plan) fixture {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "project-secrets-api@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	host := &fakeHost{store: store, kid: identity.Recipient().String()}
	service := projectsecrets.Service{Host: host}
	with := func(h func(http.ResponseWriter, *http.Request, state.Account)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { h(w, r, account) }
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects/{slug}/environments/{environment}/secrets", with(service.List))
	mux.HandleFunc("PUT /v1/projects/{slug}/environments/{environment}/secrets/{key}", with(service.Set))
	mux.HandleFunc("POST /v1/projects/{slug}/environments/{environment}/secrets/{key}/rotate", with(service.Rotate))
	mux.HandleFunc("DELETE /v1/projects/{slug}/environments/{environment}/secrets/{key}", with(service.Delete))
	return fixture{host: host, mux: mux, account: account, project: project}
}

func (f fixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(method, path, reader))
	return rec
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &problem)
	return problem.Code
}

func TestProjectSecretsLifecycle(t *testing.T) {
	f := newFixture(t, api.PlanPro)
	base := "/v1/projects/shop/environments/production/secrets"
	set := api.SetProjectEnvironmentSharedSecretRequest{Value: "s3cret"}

	rec := f.do(t, "PUT", base+"/DATABASE_PASSWORD", set)
	if rec.Code != http.StatusOK {
		t.Fatalf("set = %d %s", rec.Code, rec.Body)
	}
	var written api.ProjectEnvironmentSharedSecretResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &written); err != nil || written.Secret.Key != "DATABASE_PASSWORD" ||
		written.Secret.Version != 1 || written.Secret.Kid != f.host.kid || written.Environment != "production" {
		t.Fatalf("set response = %+v, %v", written, err)
	}
	if strings.Contains(rec.Body.String(), "s3cret") || strings.Contains(rec.Body.String(), "sealed:") {
		t.Fatalf("a write response carried the value: %s", rec.Body)
	}

	rec = f.do(t, "POST", base+"/DATABASE_PASSWORD/rotate", api.SetProjectEnvironmentSharedSecretRequest{Value: "n3w"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &written); err != nil || written.Secret.Version != 2 {
		t.Fatalf("rotate response = %+v, %v", written, err)
	}
	if rec = f.do(t, "POST", base+"/UNKNOWN_KEY/rotate", set); rec.Code != http.StatusNotFound {
		t.Fatalf("rotating an unknown key = %d %s", rec.Code, rec.Body)
	}

	rec = f.do(t, "GET", base, nil)
	var listed api.ProjectEnvironmentSharedSecretsResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Secrets) != 1 ||
		listed.Secrets[0].Version != 2 || listed.Limit != api.MustLimitsFor(api.PlanPro).ProjectSecretCountMax {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "n3w") || strings.Contains(rec.Body.String(), "sealed:") {
		t.Fatalf("a listing carried the value: %s", rec.Body)
	}

	if rec = f.do(t, "DELETE", base+"/DATABASE_PASSWORD", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if rec = f.do(t, "DELETE", base+"/DATABASE_PASSWORD", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d %s", rec.Code, rec.Body)
	}
	want := []string{"project.secret.set", "project.secret.rotated", "project.secret.deleted"}
	if fmt.Sprint(f.host.events) != fmt.Sprint(want) {
		t.Fatalf("audit events = %v, want %v", f.host.events, want)
	}
	for _, data := range f.host.data {
		if data["project_slug"] != "shop" || data["environment_slug"] != "production" || data["key"] != "DATABASE_PASSWORD" {
			t.Fatalf("audit data = %v", data)
		}
		for _, value := range data {
			if s, ok := value.(string); ok && (strings.Contains(s, "s3cret") || strings.Contains(s, "n3w")) {
				t.Fatalf("audit data carried the value: %v", data)
			}
		}
	}
}

func TestProjectSecretsRejections(t *testing.T) {
	f := newFixture(t, api.PlanFree)
	base := "/v1/projects/shop/environments/production/secrets"
	value := api.SetProjectEnvironmentSharedSecretRequest{Value: "v"}
	for _, tc := range []struct {
		name, method, path string
		body               any
		status             int
		code               string
	}{
		{"invalid key", "PUT", base + "/lower_case", value, http.StatusBadRequest, api.CodeSecretInvalidKey},
		{"platform identity key", "PUT", base + "/FAAS_APP_ID", value, http.StatusBadRequest, api.CodeSecretInvalidKey},
		{"empty value", "PUT", base + "/TOKEN_A", api.SetProjectEnvironmentSharedSecretRequest{}, http.StatusBadRequest, api.CodeValidation},
		{"value over the plan size", "PUT", base + "/TOKEN_A", api.SetProjectEnvironmentSharedSecretRequest{Value: strings.Repeat("x", api.MustLimitsFor(api.PlanFree).SecretValueMaxBytes+1)}, http.StatusRequestEntityTooLarge, api.CodeSecretValueTooLarge},
		{"unknown project", "PUT", "/v1/projects/nope/environments/production/secrets/TOKEN_A", value, http.StatusNotFound, api.CodeNotFound},
		{"unknown environment", "PUT", "/v1/projects/shop/environments/preview/secrets/TOKEN_A", value, http.StatusNotFound, api.CodeNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, tc.method, tc.path, tc.body)
			if rec.Code != tc.status || problemCode(t, rec) != tc.code {
				t.Fatalf("%s %s = %d %s, want %d %s", tc.method, tc.path, rec.Code, rec.Body, tc.status, tc.code)
			}
		})
	}
	if !api.IsPlatformIdentityEnvKey("FAAS_APP_ID") {
		t.Fatal("the test's platform identity key is not one")
	}

	// The plan limit counts keys, and replacing an existing key is not a new one.
	max := api.MustLimitsFor(api.PlanFree).ProjectSecretCountMax
	for i := 0; i < max; i++ {
		if rec := f.do(t, "PUT", fmt.Sprintf("%s/KEY_%d", base, i), value); rec.Code != http.StatusOK {
			t.Fatalf("key %d = %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := f.do(t, "PUT", base+"/ONE_TOO_MANY", value)
	if rec.Code != http.StatusForbidden || problemCode(t, rec) != api.CodePlanLimitProjectSecrets {
		t.Fatalf("over the limit = %d %s", rec.Code, rec.Body)
	}
	if rec = f.do(t, "PUT", base+"/KEY_0", value); rec.Code != http.StatusOK {
		t.Fatalf("replacing at the limit = %d %s", rec.Code, rec.Body)
	}
}

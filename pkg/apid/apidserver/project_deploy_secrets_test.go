package apidserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

type projectSecretTestLog struct{ t *testing.T }

func (w projectSecretTestLog) Write(p []byte) (int, error) { w.t.Logf("%s", p); return len(p), nil }

// Inspect storage at actual build admission, rather than after the HTTP receipt.
type projectSecretBuildStore struct {
	MemStore *state.MemStore
	onBuild  func(state.Deployment)
}

func (s *projectSecretBuildStore) CreateBuildWithID(ctx context.Context, buildID, id string, kind state.DeploymentKind, n int64, path string) (state.Build, error) {
	dep, err := s.MemStore.DeploymentByID(ctx, id)
	if err != nil {
		return state.Build{}, err
	}
	if s.onBuild != nil {
		s.onBuild(dep)
	}
	return s.MemStore.CreateBuildWithID(ctx, buildID, id, kind, n, path)
}

func projectSecretArchive(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"back", "worker"} {
		body := []byte("FROM alpine\n")
		if err := tw.WriteHeader(&tar.Header{Name: "repo/" + name + "/Dockerfile", Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	compose := []byte("services:\n  back:\n    build: ./back\n    ports: ['8080:8080']\n  worker:\n    build: ./worker\n    ports: ['8081:8080']\n")
	if err := tw.WriteHeader(&tar.Header{Name: "repo/docker-compose.yml", Mode: 0600, Size: int64(len(compose))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(compose); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func projectSecretsTestServer(t *testing.T, scopes []string) (*server, *projectSecretBuildStore, state.Account, string) {
	t.Helper()
	spool := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	store := &projectSecretBuildStore{MemStore: state.NewMemStore()}
	acct, err := store.MemStore.CreateAccount(t.Context(), "secrets@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MemStore.CreateAPIKey(t.Context(), acct.ID, hash, "test", scopes); err != nil {
		t.Fatal(err)
	}
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	prevRecipient, prevIdent, prevHMAC := setSecretRecipient, mfaIdentities, hostHMACKey
	setSecretRecipient = func() *age.X25519Recipient { return ident.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{ident} }
	hostHMACKey = func() []byte { return bytes.Repeat([]byte{1}, 32) }
	t.Cleanup(func() { setSecretRecipient, mfaIdentities, hostHMACKey = prevRecipient, prevIdent, prevHMAC })
	s := newServer(statetest.NewMem(store), slog.New(slog.NewTextHandler(projectSecretTestLog{t}, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(t.Context(), wire.NewOpsMetrics("project_secret_test"))
	return s, store, acct, key
}

func projectSecretsRequest(t *testing.T, s *server, key, path string, secrets string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := w.WriteField("project_slug", "demo"); err != nil {
		t.Fatal(err)
	}
	if secrets != "" {
		if err := w.WriteField("secrets", secrets); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range fields {
		if k == "attempt_id" || k == "retry_targets" {
			continue
		}
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	p, err := w.CreateFormFile("source", "source.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(projectSecretArchive(t)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, &b)
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", w.FormDataContentType())
	if fields["attempt_id"] != "" {
		r.Header.Set(api.DeploymentAttemptHeader, fields["attempt_id"])
	}
	if fields["retry_targets"] != "" {
		r.Header.Set(api.ProjectRetryHeader, fields["retry_targets"])
	}
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, r)
	return rec
}

func TestProjectDeploySecretsBeforeEveryBuildAndRetry(t *testing.T) {
	s, store, acct, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	builds := 0
	store.onBuild = func(dep state.Deployment) {
		builds++
		app, err := store.MemStore.AppByID(t.Context(), dep.AppID)
		if err != nil {
			t.Fatal(err)
		}
		apps, err := store.MemStore.AppsForProject(t.Context(), acct.ID, app.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		if len(apps) != 2 {
			t.Fatalf("project apps=%d", len(apps))
		}
		for _, selected := range apps {
			rows, err := store.MemStore.ListAppSecretsInScope(t.Context(), acct.ID, selected.ID, api.DefaultEnvScope)
			if err != nil || len(rows) != 1 || rows[0].Key != "TOKEN" || len(rows[0].Ciphertext) == 0 {
				t.Fatalf("first build admitted before all workloads have sealed secrets: app=%s rows=%v err=%v", selected.Slug, rows, err)
			}
			if bytes.Contains(rows[0].Ciphertext, []byte("fixture-secret")) {
				t.Error("stored plaintext")
			}
		}
	}
	for i := 0; i < 2; i++ {
		rec := projectSecretsRequest(t, s, key, "/v1/projects", `{"TOKEN":"fixture-secret"}`, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("apply %d: %d %s", i, rec.Code, rec.Body)
		}
		var result api.ApplyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Builds) != 2 {
			t.Fatalf("build results=%+v", result.Builds)
		}
		for _, b := range result.Builds {
			if b.DeploymentID == "" || b.Error != "" {
				t.Fatalf("build=%+v", b)
			}
		}
		if strings.Contains(rec.Body.String(), "fixture-secret") {
			t.Error("response leaked value")
		}
	}
	if builds != 4 {
		t.Fatalf("builds=%d, want 4 including secret-only retry", builds)
	}
}

func TestProjectDeploySecretsRejectBeforeBuild(t *testing.T) {
	for _, tc := range []struct {
		name         string
		scopes       []string
		path, bundle string
		unavailable  bool
		want         int
	}{
		{"missing permission", []string{api.ScopeDeployWrite}, "/v1/projects", `{"TOKEN":"fixture-secret"}`, false, 403},
		{"scan", api.ScopesAdminOnly, "/v1/projects/scan", `{"TOKEN":"fixture-secret"}`, false, 400},
		{"invalid key", api.ScopesAdminOnly, "/v1/projects", `{"bad-key":"fixture-secret"}`, false, 400},
		{"invalid value", api.ScopesAdminOnly, "/v1/projects", `{"TOKEN":42}`, false, 400},
		{"empty", api.ScopesAdminOnly, "/v1/projects", `{"TOKEN":""}`, false, 400},
		{"storage unavailable", api.ScopesAdminOnly, "/v1/projects", `{"TOKEN":"fixture-secret"}`, true, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, _, key := projectSecretsTestServer(t, tc.scopes)
			builds := 0
			store.onBuild = func(state.Deployment) { builds++ }
			if tc.unavailable {
				setSecretRecipient = func() *age.X25519Recipient { return nil }
			}
			rec := projectSecretsRequest(t, s, key, tc.path, tc.bundle, nil)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body)
			}
			if builds != 0 {
				t.Fatalf("admitted %d builds after secret failure", builds)
			}
			if strings.Contains(rec.Body.String(), "fixture-secret") {
				t.Error("error leaked value")
			}
		})
	}
}

func TestProjectDeploySecretsSelectedEnvironment(t *testing.T) {
	s, store, acct, key := projectSecretsTestServer(t, api.ScopesAdminOnly)
	rec := projectSecretsRequest(t, s, key, "/v1/projects", "", nil)
	if rec.Code != 200 {
		t.Fatalf("initial apply: %d %s", rec.Code, rec.Body)
	}
	project, err := store.MemStore.ProjectBySlug(t.Context(), acct.ID, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MemStore.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	builds := 0
	store.onBuild = func(dep state.Deployment) {
		builds++
		if dep.Scope != "staging" {
			t.Errorf("scope=%q", dep.Scope)
		}
		rows, err := store.MemStore.ListAppSecretsInScope(t.Context(), acct.ID, dep.AppID, "staging")
		if err != nil || len(rows) != 1 {
			t.Fatalf("build missing staging secret: %v %v", rows, err)
		}
	}
	rec = projectSecretsRequest(t, s, key, "/v1/projects", `{"TOKEN":"staging-secret"}`, map[string]string{"only": "back", "environment": "staging"})
	if rec.Code != 200 {
		t.Fatalf("staging apply: %d %s", rec.Code, rec.Body)
	}
	if builds != 1 {
		t.Fatalf("selected builds=%d", builds)
	}
	apps, err := store.MemStore.AppsForProject(t.Context(), acct.ID, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		for _, scope := range []string{"default", "staging"} {
			rows, err := store.MemStore.ListAppSecretsInScope(t.Context(), acct.ID, app.ID, scope)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if app.WorkloadName == "back" && scope == "staging" {
				want = 1
			}
			if len(rows) != want {
				t.Errorf("app=%s scope=%s secrets=%d want=%d", app.Slug, scope, len(rows), want)
			}
		}
	}
}

func TestProjectDeploySecretsPlanLimits(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	secrets := map[string]string{}
	for i := 0; i <= limits.SecretCountMax; i++ {
		secrets["TOKEN_"+strings.Repeat("A", i)] = "value"
	}
	payload, err := json.Marshal(secrets)
	if err != nil {
		t.Fatal(err)
	}
	if _, prob := readProjectDeploySecrets(bytes.NewReader(payload), limits); prob == nil || prob.Code != api.CodePlanLimitSecrets {
		t.Fatalf("secret count problem=%+v", prob)
	}
	payload, err = json.Marshal(map[string]string{"TOKEN": strings.Repeat("x", limits.SecretValueMaxBytes+1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, prob := readProjectDeploySecrets(bytes.NewReader(payload), limits); prob == nil {
		t.Fatal("oversize secret accepted")
	}
	payload, err = json.Marshal(map[string]string{"TOKEN": strings.Repeat("\x01", limits.SecretValueMaxBytes)})
	if err != nil {
		t.Fatal(err)
	}
	if _, prob := readProjectDeploySecrets(bytes.NewReader(payload), limits); prob != nil {
		t.Fatalf("valid escaped value rejected: %+v", prob)
	}
}

// adr: 20261108090000
package apidserver_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// applyProjectWithSecrets applies a Compose project with a secrets bundle.
func applyProjectWithSecrets(t *testing.T, h http.Handler, key, compose string, secrets map[string]string) api.ApplyResponse {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if err := mw.WriteField("project_slug", "secret-shop"); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(secrets)
	if err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("secrets", string(raw)); err != nil {
		t.Fatal(err)
	}
	part, err := mw.CreateFormFile("source", "source.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(environmentProjectArchive(t, compose)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/projects", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	var out api.ApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A secrets bundle key goes only to the services whose Compose environment
// names it; a key no service names still reaches every service.
func TestProjectDeploySecretsGoOnlyToServicesThatNameThem(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	prevRecipient, prevIDs, prevHMAC := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities, *apidserver.HostHMACKey
	t.Cleanup(func() {
		*apidserver.SetSecretRecipient, *apidserver.MfaIdentities, *apidserver.HostHMACKey = prevRecipient, prevIDs, prevHMAC
	})
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return ident.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{ident} }
	*apidserver.HostHMACKey = func() []byte { return bytes.Repeat([]byte{1}, 32) }
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	compose := "services:\n  billing:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n    environment:\n      STRIPE_KEY: ${STRIPE_KEY}\n" +
		"  web:\n    image: nginx:1.27\n    ports: [\"8081:80\"]\n"
	out := applyProjectWithSecrets(t, *e.H(), *e.Key(), compose, map[string]string{"STRIPE_KEY": "sk_live_sentinel", "SHARED_TOKEN": "shared-sentinel"})
	store, acct := *e.Store(), *e.Acct()
	keys := map[string]map[string]bool{}
	for _, build := range out.Builds {
		rows, err := store.ListAppSecretsInScope(t.Context(), acct.ID, build.AppID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		// Slugs may carry the project prefix (secret-shop-billing).
		service := build.Slug[strings.LastIndex(build.Slug, "-")+1:]
		keys[service] = map[string]bool{}
		for _, row := range rows {
			keys[service][row.Key] = true
		}
	}
	if !keys["billing"]["STRIPE_KEY"] || keys["web"]["STRIPE_KEY"] {
		t.Fatalf("STRIPE_KEY reached %v; want billing only", keys)
	}
	if !keys["billing"]["SHARED_TOKEN"] || !keys["web"]["SHARED_TOKEN"] {
		t.Fatalf("SHARED_TOKEN reached %v; want every service", keys)
	}
}

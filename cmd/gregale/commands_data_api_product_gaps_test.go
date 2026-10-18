package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type recordedRequest struct {
	method, path, query, body string
}

// settingsAPI serves the secret and restart endpoints the Data API settings
// commands use, recording every call in order.
type settingsAPI struct {
	mu       sync.Mutex
	requests []recordedRequest
	secrets  []api.AppSecretResponse
}

func newSettingsAPI(t *testing.T, secrets ...api.AppSecretResponse) *settingsAPI {
	t.Helper()
	s := &settingsAPI{secrets: secrets}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(body)})
		s.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/secrets"):
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: s.secrets, Count: len(s.secrets)})
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/DATA_API_AGGREGATES"):
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not found","status":404,"code":"not_found"}`))
		case strings.Contains(r.URL.Path, "restart"):
			_, _ = w.Write([]byte(`{"wake_id":"wake-1"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_API", server.URL)
	return s
}

func (s *settingsAPI) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, r := range s.requests {
		out = append(out, r.method+" "+r.path)
	}
	return out
}

func (s *settingsAPI) bodyFor(suffix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.requests {
		if strings.HasSuffix(r.path, suffix) && r.method == http.MethodPut {
			return r.body
		}
	}
	return ""
}

func TestDataAPIPublicReadValidation(t *testing.T) {
	for value, want := range map[string]bool{
		"api.products":                true,
		"api.products,api.Categories": true,
		"":                            false,
		"products":                    false,
		"api.products,api.products":   false,
		"API.products":                false,
		"api.bad-name":                false,
		"api.a.b":                     false,
	} {
		if got := validDataAPIPublicRead(value); got != want {
			t.Errorf("validDataAPIPublicRead(%q) = %v", value, got)
		}
	}
	tooMany := make([]string, api.DataAPIPublicReadMaxRelations+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("api.t%d", i)
	}
	if validDataAPIPublicRead(strings.Join(tooMany, ",")) {
		t.Fatal("accepted more relations than the canonical bound")
	}
}

func TestDataAPIConfigureStoresSettingsAndRestarts(t *testing.T) {
	resetJSONOut(t)
	server := newSettingsAPI(t)
	out, restore := captureStdout(t)
	defer restore()
	if code := cmdDataAPIConfigure([]string{"shop-data", "--public-read", "api.products,api.categories", "--aggregates", "on"}); code != 0 {
		t.Fatalf("exit = %d, output %s", code, out.String())
	}
	want := []string{"PUT /v1/apps/shop-data/secrets/DATA_API_PUBLIC_READ", "PUT /v1/apps/shop-data/secrets/DATA_API_SQL_DEADLINE", "PUT /v1/apps/shop-data/secrets/DATA_API_AGGREGATES"}
	calls := server.calls()
	if len(calls) != 4 || strings.Join(calls[:3], "|") != strings.Join(want, "|") || !strings.Contains(calls[3], "restart") {
		t.Fatalf("calls = %v", calls)
	}
	if !strings.Contains(server.bodyFor("/DATA_API_PUBLIC_READ"), "api.products,api.categories") {
		t.Fatalf("public read value not stored: %s", server.bodyFor("/DATA_API_PUBLIC_READ"))
	}
	if !strings.Contains(out.String(), "SQL deadline functions") {
		t.Fatalf("aggregates on must point at the SQL deadline installation: %s", out.String())
	}
}

func TestDataAPIConfigureDisablesWithoutRestartWhenAsked(t *testing.T) {
	resetJSONOut(t)
	server := newSettingsAPI(t)
	out, restore := captureStdout(t)
	defer restore()
	if code := cmdDataAPIConfigure([]string{"shop-data", "--no-public-read", "--aggregates", "off", "--no-restart"}); code != 0 {
		t.Fatalf("exit = %d, output %s", code, out.String())
	}
	calls := server.calls()
	if strings.Join(calls, "|") != "DELETE /v1/apps/shop-data/secrets/DATA_API_PUBLIC_READ|DELETE /v1/apps/shop-data/secrets/DATA_API_AGGREGATES" {
		t.Fatalf("calls = %v (an absent setting must count as removed, and no restart)", calls)
	}
	if !strings.Contains(out.String(), "data-api refresh shop-data") {
		t.Fatalf("missing refresh guidance: %s", out.String())
	}
}

func TestDataAPIConfigureRejectsInvalidFlags(t *testing.T) {
	resetJSONOut(t)
	server := newSettingsAPI(t)
	for _, args := range [][]string{
		{"shop-data"},
		{"shop-data", "--public-read", "api.a", "--no-public-read"},
		{"shop-data", "--aggregates", "maybe"},
		{"shop-data", "--public-read", "products"},
		{"--public-read", "api.a"},
	} {
		if code := cmdDataAPIConfigure(args); code != 1 {
			t.Fatalf("%v exit = %d", args, code)
		}
	}
	if calls := server.calls(); len(calls) != 0 {
		t.Fatalf("invalid flags reached the API: %v", calls)
	}
}

func TestDataAPIServerKeyLifecycle(t *testing.T) {
	resetJSONOut(t)
	server := newSettingsAPI(t, api.AppSecretResponse{Key: "DATA_API_SERVER_KEY_A_SHA256", Scope: api.DefaultEnvScope, UpdatedAt: "2026-10-01T00:00:00Z"})
	out, restore := captureStdout(t)
	defer restore()
	if code := cmdDataAPIServerKey([]string{"create", "shop-data"}); code != 0 {
		t.Fatalf("exit = %d, output %s", code, out.String())
	}
	key := regexp.MustCompile(`gdsk_[A-Za-z0-9_-]{43}`).FindString(out.String())
	if key == "" {
		t.Fatalf("key not shown once: %s", out.String())
	}
	sum := sha256.Sum256([]byte(key))
	stored := server.bodyFor("/DATA_API_SERVER_KEY_B_SHA256")
	if !strings.Contains(stored, hex.EncodeToString(sum[:])) || strings.Contains(stored, key) {
		t.Fatalf("slot b must store only the hash: %s", stored)
	}
	calls := server.calls()
	if len(calls) != 3 || !strings.HasSuffix(calls[0], "/secrets") || !strings.Contains(calls[2], "restart") {
		t.Fatalf("calls = %v", calls)
	}
}

func TestDataAPIServerKeySlotsRefuseSilentReplacement(t *testing.T) {
	both := []dataAPIServerKeySlot{{Slot: "a", Present: true, UpdatedAt: "2026-10-02T00:00:00Z"}, {Slot: "b", Present: true, UpdatedAt: "2026-10-01T00:00:00Z"}}
	if _, err := chooseDataAPIServerKeySlot(both, false); err == nil {
		t.Fatal("full slots replaced without --replace-oldest")
	}
	if slot, _ := chooseDataAPIServerKeySlot(both, true); slot != "b" {
		t.Fatalf("replaced %s, want the older slot b", slot)
	}
	if slot, _ := chooseDataAPIServerKeySlot([]dataAPIServerKeySlot{{Slot: "a"}, {Slot: "b"}}, false); slot != "a" {
		t.Fatalf("first free slot = %s", slot)
	}
	key, hash, err := newDataAPIServerKey()
	sum := sha256.Sum256([]byte(key))
	if err != nil || !regexp.MustCompile(`^gdsk_[A-Za-z0-9_-]{43}$`).MatchString(key) || hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("key %q hash %q err %v", key, hash, err)
	}
	resetJSONOut(t)
	server := newSettingsAPI(t)
	for _, args := range [][]string{{"rotate", "shop-data"}, {"revoke", "shop-data"}, {"revoke", "shop-data", "--slot", "c"}, {"create", "shop-data", "--slot", "a"}, {"list", "shop-data", "--replace-oldest"}} {
		if code := cmdDataAPIServerKey(args); code != 1 {
			t.Fatalf("%v exit = %d", args, code)
		}
	}
	if calls := server.calls(); len(calls) != 0 {
		t.Fatalf("invalid flags reached the API: %v", calls)
	}
	if code := cmdDataAPIServerKey([]string{"revoke", "shop-data", "--slot", "all", "--no-restart"}); code != 0 {
		t.Fatal("revoke all failed")
	}
	if calls := server.calls(); strings.Join(calls[1:], "|") != "DELETE /v1/apps/shop-data/secrets/DATA_API_SERVER_KEY_A_SHA256|DELETE /v1/apps/shop-data/secrets/DATA_API_SERVER_KEY_B_SHA256" {
		t.Fatalf("revoke calls = %v", calls)
	}
}

func readArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(tr)
		files[hdr.Name] = string(content)
	}
}

func writeMigrationFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "migrations")
	for name, content := range map[string]string{
		"release.mjs":           "import './migrate.mjs'\n",
		"migrate.mjs":           "export {}\n",
		"sql/0001_init.sql":     "CREATE TABLE api.notes (id bigint);\n",
		".env":                  "SECRET=1\n",
		"node_modules/pg/x.js":  "ignored\n",
		".git/config":           "ignored\n",
		"examples/seed.example": "seed\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDataAPISingleAppArchiveAddsMigrationsAndReleaseCommand(t *testing.T) {
	dir := writeMigrationFixture(t)
	archive := filepath.Join(t.TempDir(), "api.tar.gz")
	if err := buildDataAPISingleAppArchive(dir, dataAPIDefaultReleaseCommand, archive); err != nil {
		t.Fatal(err)
	}
	files := readArchive(t, archive)
	for _, name := range []string{"data-api/server.mjs", "data-api/access.mjs", "data-api/config.mjs", "data-api/package-lock.json", "data-api/migrations/release.mjs", "data-api/migrations/sql/0001_init.sql", "data-api/migrations/examples/seed.example"} {
		if _, ok := files[name]; !ok {
			t.Errorf("archive lacks %s", name)
		}
	}
	for name := range files {
		if strings.Contains(name, "/test/") || strings.Contains(name, ".env") || strings.Contains(name, "node_modules") || strings.Contains(name, ".git") {
			t.Errorf("archive must not contain %s", name)
		}
	}
	if files["data-api/Procfile"] != "web: node server.mjs\nrelease: node migrations/release.mjs\n" {
		t.Fatalf("Procfile = %q", files["data-api/Procfile"])
	}
	dockerfile := files["data-api/Dockerfile"]
	if !strings.Contains(dockerfile, "COPY migrations ./migrations\nEXPOSE 8080") || !strings.Contains(dockerfile, "postgrest/postgrest@sha256:") {
		t.Fatalf("Dockerfile = %q", dockerfile)
	}
	template, err := os.ReadFile("templates/data-api/server.mjs")
	if err != nil || files["data-api/server.mjs"] != string(template) {
		t.Fatal("runtime files must be the embedded template bytes")
	}
}

func TestDataAPISingleAppArchiveRejectsUnsafeInput(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "api.tar.gz")
	empty := t.TempDir()
	if err := buildDataAPISingleAppArchive(empty, dataAPIDefaultReleaseCommand, archive); err == nil {
		t.Fatal("empty migrations accepted")
	}
	noRelease := t.TempDir()
	if err := os.WriteFile(filepath.Join(noRelease, "migrate.mjs"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := buildDataAPISingleAppArchive(noRelease, dataAPIDefaultReleaseCommand, archive); err == nil || !strings.Contains(err.Error(), "release.mjs") {
		t.Fatalf("missing release.mjs: %v", err)
	}
	if err := buildDataAPISingleAppArchive(noRelease, "node migrations/migrate.mjs", archive); err != nil {
		t.Fatalf("custom release command: %v", err)
	}
	if err := buildDataAPISingleAppArchive(noRelease, "node a.mjs\nrm -rf /", archive); err == nil {
		t.Fatal("multi-line release command accepted")
	}
	linked := writeMigrationFixture(t)
	if err := os.Symlink("/etc/hosts", filepath.Join(linked, "hosts.sql")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := buildDataAPISingleAppArchive(linked, dataAPIDefaultReleaseCommand, archive); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func TestDataAPITypesRendersClientsFromSnapshotFile(t *testing.T) {
	resetJSONOut(t)
	snapshot := []byte(`{"version":4,"schemas":["api"],"tables":[{"schema":"api","name":"notes","view":false,"columns":[{"name":"id","type":"number","nullable":false,"optional":true,"insertable":true,"updatable":false},{"name":"body","type":"string","nullable":false,"optional":false,"insertable":true,"updatable":true}],"relationships":[]}],"functions":[],"enums":{},"composites":[]}`)
	var compact bytes.Buffer
	if err := json.Compact(&compact, snapshot); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(compact.Bytes())
	contract, _ := json.Marshal(map[string]any{"version": 1, "fingerprint": hex.EncodeToString(sum[:]), "snapshot": json.RawMessage(compact.Bytes())})
	dir := t.TempDir()
	snapshotFile := filepath.Join(dir, "contract.json")
	if err := os.WriteFile(snapshotFile, contract, 0o644); err != nil {
		t.Fatal(err)
	}
	out, restore := captureStdout(t)
	defer restore()
	goFile, pyFile := filepath.Join(dir, "client.go"), filepath.Join(dir, "client.py")
	if code := cmdDataAPITypes([]string{"--snapshot-file", snapshotFile, "--language", "go", "--package", "notesdata", "--output", goFile}); code != 0 {
		t.Fatalf("go exit = %d: %s", code, out.String())
	}
	if code := cmdDataAPITypes([]string{"--snapshot-file", snapshotFile, "--language", "python", "--output", pyFile}); code != 0 {
		t.Fatalf("python exit = %d: %s", code, out.String())
	}
	goSource, _ := os.ReadFile(goFile)
	pySource, _ := os.ReadFile(pyFile)
	if !strings.Contains(string(goSource), "package notesdata") || !strings.Contains(string(goSource), hex.EncodeToString(sum[:])) || !strings.Contains(string(pySource), `NotesRow = TypedDict("NotesRow"`) {
		t.Fatal("rendered clients lack the package, fingerprint or row type")
	}
	if code := cmdDataAPITypes([]string{"--snapshot-file", snapshotFile, "--language", "go", "--package", "notesdata", "--output", goFile, "--check"}); code != 0 {
		t.Fatal("unchanged contract failed --check")
	}
	if err := os.WriteFile(goFile, append(goSource, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdDataAPITypes([]string{"--snapshot-file", snapshotFile, "--language", "go", "--package", "notesdata", "--output", goFile, "--check"}); code == 0 {
		t.Fatal("stale client passed --check")
	}
	for _, args := range [][]string{
		{"--snapshot-file", snapshotFile, "--language", "rust"},
		{"--snapshot-file", snapshotFile, "--language", "go", "--package", "Bad"},
		{"--snapshot-file", snapshotFile, "--language", "python", "--package", "x"},
		{"shop-data", "--snapshot-file", snapshotFile, "--language", "go"},
		{"--language", "go"},
		{"--snapshot-file", snapshotFile, "--language", "go", "--validators"},
		{"shop-data", "--snapshot-file", snapshotFile},
	} {
		if code := cmdDataAPITypes(args); code != 1 {
			t.Fatalf("%v exit = %d", args, code)
		}
	}
}

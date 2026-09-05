package main

import (
	"archive/tar"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
)

func TestDevSourceDeployFullThenDelta(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e := setup(t, api.PlanHobby)
	created := e.do(t, "PUT", "/v1/dev/sessions/source-sync", api.UpsertDevSessionRequest{}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create dev session: %d %s", created.Code, created.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}

	baseRaw := buildTestTarGz(t, []tar.Header{{Name: "package.json"}, {Name: "index.js"}}, map[string][]byte{
		"package.json": []byte(`{"name":"dev-source-test"}`),
		"index.js":     []byte("console.log('old')\n"),
	})
	basePath := filepath.Join(spool, "client-base.tar.gz")
	if err := os.WriteFile(basePath, baseRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	baseFile, err := openDevSourceArchive(basePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = baseFile.Close() }()
	limits := sourcedelta.Limits{MaxEntries: api.SourceArchiveMaxEntries, MaxCompressedBytes: 100 << 20}
	base, err := sourcedelta.Inspect(baseFile, limits)
	if err != nil {
		t.Fatal(err)
	}
	full := postDevSource(t, e, session.App.Slug, baseRaw, map[string]string{"dev_source_target": base.Revision})
	if full.Code != http.StatusAccepted {
		t.Fatalf("full sync: %d %s", full.Code, full.Body.String())
	}

	targetRaw := buildTestTarGz(t, []tar.Header{{Name: "package.json"}, {Name: "index.js"}, {Name: "new.js"}}, map[string][]byte{
		"package.json": []byte(`{"name":"dev-source-test"}`),
		"index.js":     []byte("console.log('new')\n"),
		"new.js":       []byte("export default true\n"),
	})
	targetPath := filepath.Join(spool, "client-target.tar.gz")
	deltaPath := filepath.Join(spool, "client-delta.tar.gz")
	if err := os.WriteFile(targetPath, targetRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	targetFile, err := openDevSourceArchive(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = targetFile.Close() }()
	deltaFile, err := os.OpenFile(deltaPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = deltaFile.Close() }()
	delta, err := sourcedelta.Create(base, targetFile, deltaFile, limits)
	if err != nil {
		t.Fatal(err)
	}
	deltaRaw, err := os.ReadFile(deltaPath)
	if err != nil {
		t.Fatal(err)
	}
	deleted, _ := json.Marshal(delta.Deleted)
	rec := postDevSource(t, e, session.App.Slug, deltaRaw, map[string]string{
		"dev_source_base":    base.Revision,
		"dev_source_target":  delta.Target.Revision,
		"dev_source_deleted": string(deleted),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delta sync: %d %s", rec.Code, rec.Body.String())
	}
	app, err := e.store.AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	cachePath, ok := e.s.devSourceBasePath(e.acct, app, delta.Target.Revision)
	if !ok {
		t.Fatal("target revision was not published to developer source cache")
	}
	cacheFile, err := openDevSourceArchive(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cacheFile.Close() }()
	cached, err := sourcedelta.Inspect(cacheFile, limits)
	if err != nil || cached.Revision != delta.Target.Revision {
		t.Fatalf("cached target = %+v, err=%v", cached, err)
	}
}

func TestDevSourceDeployMissingBaseReturnsRetrySignal(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e := setup(t, api.PlanHobby)
	created := e.do(t, "PUT", "/v1/dev/sessions/source-miss", api.UpsertDevSessionRequest{}, nil)
	var session api.DevSessionResponse
	_ = json.Unmarshal(created.Body.Bytes(), &session)
	raw := buildTestTarGz(t, []tar.Header{{Name: "index.js"}}, map[string][]byte{"index.js": []byte("ok\n")})
	rec := postDevSource(t, e, session.App.Slug, raw, map[string]string{
		"dev_source_base":   strings.Repeat("a", 64),
		"dev_source_target": strings.Repeat("b", 64),
	})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeDevSourceBaseMissing) {
		t.Fatalf("missing base response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPruneDevSourceCacheRemovesStaleEntries(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "app", "old.tar.gz")
	current := filepath.Join(root, "app", "current.tar.gz")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-api.DevSourceCacheTTL - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := pruneDevSourceCache(root, current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale cache still exists: %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("preserved cache missing: %v", err)
	}
}

func postDevSource(t *testing.T, e testEnv, slug string, source []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	parts := map[string]multipartPart{"source": {filename: "source.tar.gz", body: source}}
	for name, value := range fields {
		parts[name] = multipartPart{body: []byte(value)}
	}
	body, contentType := multipartUpload(t, parts)
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+slug+"/deployments/dev-source", body)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

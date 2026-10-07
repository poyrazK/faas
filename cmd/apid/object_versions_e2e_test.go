package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 638
func TestObjectVersionsControlJourneyMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	objectVersionsControlJourney(t, e.s, e.store, e.acct, e.key, nil)
}
func TestObjectVersionsControlJourneyPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	objectVersionsControlJourney(t, e.s, e.store, e.acct, e.key, e.pool)
}

func objectVersionsControlJourney(t *testing.T, s *server, st state.Store, acct state.Account, bearer string, pool *pgxpool.Pool) {
	identity, teardown := withTestIdentities(t)
	defer teardown()
	const key = "目录 /+%.txt"
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Has("versions") {
			q := r.URL.Query()
			version, latest := "private-new/+%", "true"
			if q.Get("version-id-marker") != "" {
				if q.Get("key-marker") != key || q.Get("version-id-marker") != version {
					t.Error("native cursor changed", q)
				}
				version, latest = "private-old/+%", "false"
			}
			cursor := ""
			if latest == "true" {
				cursor = "<NextKeyMarker>" + url.PathEscape(key) + "</NextKeyMarker><NextVersionIdMarker>" + version + "</NextVersionIdMarker>"
			}
			_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>%t</IsTruncated>%s<Version><Key>%s</Key><VersionId>%s</VersionId><IsLatest>%s</IsLatest><ETag>&quot;etag&quot;</ETag><Size>3</Size><LastModified>2026-10-07T10:00:00Z</LastModified></Version></ListVersionsResult>`, latest == "true", cursor, url.PathEscape(key), version, latest)
			return
		}
		v := r.URL.Query().Get("versionId")
		if v != "private-old/+%" && v != "private-new/+%" {
			t.Error("read not pinned to native generation", v)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("X-Amz-Version-Id", v)
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("Content-Length", "3")
		if r.Method == "GET" {
			if v == "private-old/+%" {
				_, _ = io.WriteString(w, "old")
			} else {
				_, _ = io.WriteString(w, "new")
			}
		}
	}))
	defer native.Close()
	_, bucket, _, _ := seedEncryptionJournalStorage(t, s, st, acct, native.URL)
	if _, err := st.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: runtimeConfigS3, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: boolJSON(true), ApplyMode: state.RuntimeConfigApplyHot, Reason: "version read test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.runtimeConfig.reconcile(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	public := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer public.Close()
	s.objectStorage.PublicEndpoint = public.URL
	makeGateway := func(store state.Store) http.Handler {
		h, err := s3gateway.New(s3gateway.Config{Registry: s.objectStorage, Store: store.(s3gateway.Store), RequestMetrics: store.(state.ObjectStorageProviderUsageStore), SpoolDir: t.TempDir(), OpenSecret: func(blob []byte) (string, error) {
			ns, plain, err := secretbox.OpenBytes(identity, blob)
			if err != nil || ns != s3gateway.CredentialSecretNamespace {
				return "", errors.New("invalid credential")
			}
			return string(plain), nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	control := s.handler()
	base := "/v1/apps/encrypted-journal/buckets/" + bucket.ID
	send := func(method, path, token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var data io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			data = strings.NewReader(string(raw))
		}
		r := httptest.NewRequest(method, path, data)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		control.ServeHTTP(w, r)
		return w
	}
	list := func(q url.Values) api.ObjectVersionList {
		t.Helper()
		w := send("GET", base+"/objects/versions?"+q.Encode(), bearer, nil)
		var out api.ObjectVersionList
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Items) != 1 || strings.Contains(w.Body.String(), "private-") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("version list", w.Code, w.Body.String())
		}
		return out
	}
	first := list(url.Values{"limit": {"1"}, "prefix": {"目录"}})
	second := list(url.Values{"limit": {"1"}, "prefix": {"目录"}, "key_marker": {first.NextKeyMarker}, "version_id_marker": {first.NextVersionIDMarker}})
	if !first.Items[0].IsLatest || second.Items[0].IsLatest || second.Items[0].VersionID == first.Items[0].VersionID || second.NextKeyMarker != "" {
		t.Fatal(first, second)
	}
	old := second.Items[0].VersionID
	before := calls.Load()
	for _, q := range []string{"limit=0", "limit=1&limit=2", "unknown=yes", "version_id_marker=" + old, "key_marker=foreign&version_id_marker=" + old} {
		if w := send("GET", base+"/objects/versions?"+q, bearer, nil); w.Code < 400 || calls.Load() != before {
			t.Fatal("invalid query dispatched", q, w.Code)
		}
	}
	for _, req := range []api.ObjectSignRequest{{Method: "PUT", Key: key, VersionID: old}, {Method: "GET", Key: key, VersionID: "null"}, {Method: "GET", Key: "foreign", VersionID: old}, {Method: "GET", Key: key, VersionID: uuid.NewString()}} {
		if w := send("POST", base+"/signed-url", bearer, req); w.Code < 400 || calls.Load() != before {
			t.Fatal("invalid selector dispatched", req, w.Code)
		}
	}
	issue := send("POST", base+"/signed-url", bearer, api.ObjectSignRequest{Method: "GET", Key: key, VersionID: old})
	var signed api.ObjectSignedRequest
	if issue.Code != 200 || json.Unmarshal(issue.Body.Bytes(), &signed) != nil {
		t.Fatal(issue.Code, issue.Body.String())
	}
	u, err := url.Parse(signed.URL)
	if err != nil || u.Query().Get("versionId") != old {
		t.Fatal("selector absent from signed URL", err)
	}
	access := strings.Split(u.Query().Get("X-Amz-Credential"), "/")[0]
	readStore := st
	if pool != nil {
		readStore = state.NewPgStore(pool)
	}
	saved, _, err := readStore.(state.ObjectS3CredentialBindingStore).ResolveObjectS3Credential(t.Context(), access)
	if err != nil || saved.URL == nil || saved.URL.Request.VersionID != old {
		t.Fatal("persistent authority lost", saved, err)
	}
	if pool != nil {
		if _, err := pool.Exec(t.Context(), `UPDATE object_storage_s3_credentials SET url_request=jsonb_set(url_request,'{version_id}','"null"') WHERE access_key_id=$1`, access); err == nil {
			t.Fatal("database accepted mutable historical authority")
		}
		data, err := migrations.FS.ReadFile("20261007170933372_object_versioned_read_capabilities.sql")
		if err != nil {
			t.Fatal(err)
		}
		down := strings.SplitN(string(data), "-- +goose Down", 2)[1]
		if _, err = pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "Cannot discard persisted version-bound read authority") {
			t.Fatal("unsafe rollback accepted", err)
		}
	}
	// Resolve the saved authority through a separately constructed gateway/store.
	read := httptest.NewRecorder()
	makeGateway(readStore).ServeHTTP(read, httptest.NewRequest("GET", signed.URL, nil))
	if read.Code != 200 || read.Body.String() != "old" || read.Header().Get("X-Amz-Version-Id") != old {
		t.Fatal("historical read", read.Code, read.Body.String())
	}
	// A different account cannot select this bucket or its public version.
	foreign, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	token, hash, _ := api.GenerateAPIKey()
	if _, err = st.CreateAPIKey(context.Background(), foreign.ID, hash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	before = calls.Load()
	if w := send("GET", base+"/objects/versions", token, nil); w.Code < 400 || calls.Load() != before {
		t.Fatal("foreign listing dispatched", w.Code)
	}
	if w := send("POST", base+"/signed-url", token, api.ObjectSignRequest{Method: "GET", Key: key, VersionID: old}); w.Code < 400 || calls.Load() != before {
		t.Fatal("foreign historical read dispatched", w.Code)
	}
}

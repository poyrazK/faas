package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 407
func TestObjectTaggingControlEndToEndPG(t *testing.T) {
	const key = "目录 /+%.txt"
	const native = "private-version/+%?"
	var mu sync.Mutex
	tags := map[string]string{"team": "original"}
	calls, lose := 0, true
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if !r.URL.Query().Has("tagging") || r.URL.Query().Get("versionId") != native || r.Header.Get("Authorization") == "" || strings.TrimPrefix(r.URL.Path, "/physical/") != key {
			t.Error("unowned provider tag request", r.URL)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("X-Amz-Version-Id", native)
		switch r.Method {
		case "PUT":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			tags, err = objectstorage.ParseObjectTaggingXML(body)
			if err != nil {
				t.Error(err)
			}
			if lose {
				lose = false
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			}
		case "GET":
			if len(tags) == 0 {
				_, _ = io.WriteString(w, `<Tagging><TagSet/></Tagging>`)
			} else {
				_, _ = io.WriteString(w, `<Tagging><TagSet><Tag><Key>team</Key><Value>replacement</Value></Tag></TagSet></Tagging>`)
			}
		case "DELETE":
			tags = map[string]string{}
			w.WriteHeader(204)
		default:
			t.Error(r.Method)
		}
	}), 5)
	count := func() int { mu.Lock(); defer mu.Unlock(); return calls }
	refs, err := f.st.RecordObjectVersions(t.Context(), f.account.ID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: key, ProviderVersionID: native}})
	if err != nil {
		t.Fatal(err)
	}
	version := refs[0].ID
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.CreateAPIKey(t.Context(), f.account.ID, hash, "tag-admin", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	var runtime *runtimeConfigManager
	management := func() *httptest.Server {
		runtime = newRuntimeConfigManager(nil)
		if err := runtime.reconcile(t.Context(), f.st); err != nil {
			t.Fatal(err)
		}
		s := newServer(state.NewPgStore(f.pool), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithObjectStorage(f.registry).WithRuntimeConfigManager(runtime)
		srv := httptest.NewServer(s.handler())
		t.Cleanup(srv.Close)
		return srv
	}
	setFlag := func(enabled bool) {
		_, err := f.st.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: runtimeConfigS3, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: boolJSON(enabled), ApplyMode: state.RuntimeConfigApplyHot, Reason: "tagging test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.reconcile(t.Context(), f.st); err != nil {
			t.Fatal(err)
		}
	}
	before, err := f.st.ObjectUsage(t.Context(), f.account.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	srv := management()
	setFlag(true)
	client := api.NewClient(srv.URL, token)
	input := api.ObjectTaggingRequest{Tags: map[string]string{"team": "replacement"}}
	if _, err = client.PutObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version, input); err == nil || count() != 1 {
		t.Fatal("lost tag acknowledgment replayed", err, count())
	}
	srv.Close()
	srv = management()
	client = api.NewClient(srv.URL, token)
	read, err := client.GetObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version)
	if err != nil || read.VersionID != version || !maps.Equal(read.Tags, input.Tags) || count() != 2 {
		t.Fatal("restart changed tags or identity", read, err, count())
	}
	if out, err := client.PutObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version, input); err != nil || out.VersionID != version || !maps.Equal(out.Tags, input.Tags) {
		t.Fatal(out, err)
	}
	for _, tc := range []struct{ key, version string }{{"foreign-key", version}, {key, uuid.NewString()}, {key, native}} {
		if _, err = client.GetObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, tc.key, tc.version); err == nil || count() != 3 {
			t.Fatal("unowned selector dispatched", tc, err, count())
		}
	}
	scopedToken, scopedHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	apiKey, err := f.st.CreateAPIKey(t.Context(), f.account.ID, scopedHash, "tag-reader", []string{api.ScopeStorageRead, api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	scoped := api.NewClient(srv.URL, scopedToken)
	if _, err = scoped.GetObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version); err == nil || count() != 3 {
		t.Fatal("storage scope bypassed bucket grant", err)
	}
	if _, err = client.SetObjectBucketAccessGrant(t.Context(), f.app.Slug, f.bucket.ID, apiKey.ID, api.SetObjectBucketAccessGrantRequest{Permission: api.ObjectBucketPermissionRead}); err != nil {
		t.Fatal(err)
	}
	if _, err = scoped.GetObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version); err != nil || count() != 4 {
		t.Fatal("read grant", err, count())
	}
	if _, err = scoped.DeleteObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version); err == nil || count() != 4 {
		t.Fatal("read grant mutated tags", err)
	}
	setFlag(false)
	if _, err = client.GetObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version); err == nil || count() != 4 {
		t.Fatal("disabled ingress served tags", err)
	}
	if out, err := client.DeleteObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version); err != nil || out.VersionID != version || out.Tags == nil || len(out.Tags) != 0 {
		t.Fatal("disabled cleanup", out, err)
	}
	setFlag(true)
	if out, err := client.GetObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version); err != nil || len(out.Tags) != 0 {
		t.Fatal("empty tags after clear", out, err)
	}
	countBefore := count()
	for _, query := range []string{"key=" + url.QueryEscape(key) + "&version_id=%ZZ", "key=" + url.QueryEscape(key) + "&version_id=null&version_id=null", "key=" + url.QueryEscape(key) + "&unknown=1"} {
		r, _ := http.NewRequestWithContext(t.Context(), "GET", srv.URL+"/v1/apps/"+f.app.Slug+"/buckets/"+f.bucket.ID+"/objects/tags?"+query, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 400 || count() != countBefore {
			t.Fatal("ambiguous selector dispatched", query, resp.StatusCode, count())
		}
	}
	for _, raw := range []string{`{}`, `{"tags":null}`, `{"tags":{},"unknown":true}`, `{"tags":{"team":"bad\u0000"}}`} {
		r, _ := http.NewRequestWithContext(t.Context(), "PUT", srv.URL+"/v1/apps/"+f.app.Slug+"/buckets/"+f.bucket.ID+"/objects/tags?"+url.Values{"key": {key}, "version_id": {version}}.Encode(), strings.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 400 || count() != countBefore {
			t.Fatal("invalid tags dispatched", raw, resp.StatusCode, count())
		}
	}
	after, err := f.st.ObjectUsage(t.Context(), f.account.ID, time.Now())
	if err != nil || before.Buckets[0].BaselineBytes != after.Buckets[0].BaselineBytes || before.Buckets[0].GrantedBytes != after.Buckets[0].GrantedBytes {
		t.Fatal("tags changed data accounting", before, after, err)
	}
	other, err := f.st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, otherHash, _ := api.GenerateAPIKey()
	if _, err = f.st.CreateAPIKey(context.Background(), other.ID, otherHash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	if _, err = api.NewClient(srv.URL, otherToken).GetObjectBucketTags(t.Context(), f.app.Slug, f.bucket.ID, key, version); err == nil || count() != countBefore {
		t.Fatal("tenant isolation", err)
	}
	var encoded map[string]any
	body, _ := json.Marshal(read)
	if json.Unmarshal(body, &encoded) != nil || strings.Contains(string(body), native) {
		t.Fatal("private identity exposed", string(body))
	}
}

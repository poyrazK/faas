//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// TestObjectLockChosenAtBucketCreation pins ADR-20261119065000 on the control
// API: `object_lock: true` creates the provider bucket with the creation flag
// and journals the request; a backend without Object Lock refuses it; a
// creation-only backend refuses enabling Object Lock on an existing bucket.
//
// adr: 20261119065000
func TestObjectLockChosenAtBucketCreation(t *testing.T) {
	enabled := objectstorage.ObjectLockConfig{Enabled: true}
	creationOnly := objectstorage.ObjectLockConfig{Enabled: true, CreationOnly: true}
	for _, tc := range []struct {
		name        string
		flags       objectstorage.ObjectLockConfig
		method      string
		path        string // relative to the app; {bucket} is the seeded plain bucket
		body        string
		wantCode    int
		wantProblem string
		wantCreate  string // the created provider bucket's Object Lock header
		wantLocked  bool
	}{
		{"create with object lock", creationOnly, "POST", "buckets", `{"name":"vault","object_lock":true}`, 201, "", "true", true},
		{"create with object lock, later-enable backend", enabled, "POST", "buckets", `{"name":"vault","object_lock":true}`, 201, "", "true", true},
		{"create without object lock", creationOnly, "POST", "buckets", `{"name":"vault"}`, 201, "", "", false},
		{"object lock not offered", objectstorage.ObjectLockConfig{}, "POST", "buckets", `{"name":"vault","object_lock":true}`, 501, "object_storage_unsupported", "-", false},
		{"enable existing bucket on creation-only backend", creationOnly, "PUT", "buckets/{bucket}/object-lock", `{"configuration":{"enabled":true}}`, 409, "object_lock_requires_new_bucket", "-", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			s, st, account := *e.S(), *e.Store(), *e.Acct()
			var mu sync.Mutex
			var creates []string
			var lockPuts int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				q := r.URL.Query()
				switch {
				case r.Method == http.MethodPut && r.URL.RawQuery == "" && strings.Count(strings.Trim(r.URL.Path, "/"), "/") == 0:
					creates = append(creates, r.Header.Get("X-Amz-Bucket-Object-Lock-Enabled"))
				case q.Has("object-lock") && r.Method == http.MethodPut:
					lockPuts++
				case q.Has("object-lock"):
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`))
				case q.Has("versioning"):
					_, _ = w.Write([]byte(`<VersioningConfiguration/>`))
				}
			}))
			defer upstream.Close()
			_, plain, _, policy := seedEncryptionJournalStorage(t, s, st, account, upstream.URL)
			// Creation needs a usage feed for the backend.
			registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: upstream.URL, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true, AccessKeyEnv: "TEST_KEY", SecretKeyEnv: "TEST_SECRET", UsageReportsPath: "/var/spool/faas/test-usage.json", ObjectLock: tc.flags}}}, func(string) string { return "fixture-secret" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
			if err != nil {
				t.Fatal(err)
			}
			s.WithObjectStorage(registry)
			if err = (*s.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
				t.Fatal(err)
			}
			key, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = st.CreateAPIKey(t.Context(), account.ID, hash, "lock-create", api.ScopesAdminOnly); err != nil {
				t.Fatal(err)
			}
			path := "/v1/apps/encrypted-journal/" + strings.ReplaceAll(tc.path, "{bucket}", plain.ID)
			r := httptest.NewRequest(tc.method, path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+key)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.wantCode {
				t.Fatalf("%s %s = %d %s, want %d", tc.method, path, w.Code, w.Body, tc.wantCode)
			}
			if tc.wantProblem != "" {
				var p api.Problem
				if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Code != tc.wantProblem {
					t.Fatalf("problem = %+v (%v), want %s", p, err, tc.wantProblem)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if lockPuts != 0 {
				t.Fatalf("Object Lock reached the provider as %d PUTs", lockPuts)
			}
			if tc.wantCreate == "-" {
				if len(creates) != 0 {
					t.Fatalf("refused request created provider buckets: %q", creates)
				}
				if j, err := st.GetObjectBucketObjectLock(t.Context(), account.ID, plain.AppID, plain.ID); err != nil || j.Revision != 0 || j.EnabledRequired {
					t.Fatalf("refused request recorded intent: %+v (%v)", j, err)
				}
				return
			}
			if len(creates) != 1 || creates[0] != tc.wantCreate {
				t.Fatalf("provider CreateBucket Object Lock headers = %q, want one %q", creates, tc.wantCreate)
			}
			var created api.ObjectBucket
			if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			j, err := st.GetObjectBucketObjectLock(t.Context(), account.ID, plain.AppID, created.ID)
			if err != nil || j.EnabledRequired != tc.wantLocked {
				t.Fatalf("journal EnabledRequired = %v (%v), want %v", j.EnabledRequired, err, tc.wantLocked)
			}
		})
	}
}

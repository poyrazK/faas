package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 627
func TestObjectGatewaySafetyE2EMem(t *testing.T) {
	e := setup(t, api.PlanScale)
	objectGatewaySafetyE2E(t, e.s, e.store, e.acct, e.key)
}

// adr: 627
func TestObjectGatewaySafetyE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanScale)
	objectGatewaySafetyE2E(t, e.s, e.store, e.acct, e.key)
}

func objectGatewaySafetyE2E(t *testing.T, s *server, st state.Store, acct state.Account, bearer string) {
	identity, teardown := withTestIdentities(t)
	t.Cleanup(teardown)
	native := &signedURLNative{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Count(strings.Trim(r.URL.Path, "/"), "/") == 0 {
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.Method == "GET" {
				if r.URL.Query().Has("versioning") {
					_, _ = io.WriteString(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>`)
				} else {
					_, _ = io.WriteString(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsTruncated>false</IsTruncated></ListBucketResult>`)
				}
			}
			return
		}
		if r.Method == "DELETE" {
			native.mu.Lock()
			native.headers = nil
			native.mu.Unlock()
			w.WriteHeader(204)
			return
		}
		if r.Method == "GET" && r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Length", "1")
			w.Header().Set("Content-Range", "bytes 0-0/3")
			w.Header().Set("ETag", `"signed-url"`)
			w.WriteHeader(206)
			_, _ = io.WriteString(w, "a")
			return
		}
		native.serve(w, r)
	}))
	t.Cleanup(upstream.Close)
	var gateway http.Handler
	public := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.ServeHTTP(w, r) }))
	t.Cleanup(public.Close)
	since := time.Now().UTC().Add(-time.Second)
	p := api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since, MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 10, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 5, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 3600}
	config := objectstorage.Config{Accounting: &p, PublicEndpoint: public.URL, Transfer: objectstorage.ObjectTransferConfig{Profile: "proxied"}, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: upstream.URL, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true, AccessKeyEnv: "TEST_KEY", SecretKeyEnv: "TEST_SECRET"}}}
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "fixture-secret" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	s.WithObjectStorage(registry)
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	gateway, err = s3gateway.New(s3gateway.Config{Registry: registry, Store: st.(s3gateway.Store), RequestMetrics: st.(state.ObjectStorageProviderUsageStore), SpoolDir: t.TempDir(), OpenSecret: func(blob []byte) (string, error) {
		ns, plain, err := secretbox.OpenBytes(identity, blob)
		if err != nil || ns != s3gateway.CredentialSecretNamespace {
			return "", errors.New("invalid fixture credential")
		}
		return string(plain), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(s.handler())
	t.Cleanup(control.Close)
	client := api.NewClient(control.URL, bearer)
	app, err := client.CreateApp(t.Context(), api.CreateAppRequest{Slug: "gateway-safety"})
	if err != nil {
		t.Fatal(err)
	}
	var bucket api.ObjectBucket
	if cli := os.Getenv("GREGALE_OBJECT_STORAGE_E2E_CLI"); cli != "" {
		result := runGatewaySafetyCLI(t, cli, control.URL, bearer, "add", "bucket", "assets", "--app", app.Slug, "--env", "default", "--region", "us-east-1", "--wait-timeout", "3s")
		if err := json.Unmarshal(result["bucket"], &bucket); err != nil || bucket.ID == "" {
			t.Fatal("CLI did not provision bucket", err)
		}
		again := runGatewaySafetyCLI(t, cli, control.URL, bearer, "add", "bucket", "assets", "--app", app.Slug, "--env", "default", "--region", "us-east-1", "--wait-timeout", "3s")
		if string(again["bucket_created"]) != "false" || string(again["binding_created"]) != "false" {
			t.Fatal("CLI retry created duplicate resources")
		}
		runGatewaySafetyCLI(t, cli, control.URL, bearer, "bindings", "object-storage", "list", app.Slug, bucket.ID)
		runGatewaySafetyCLI(t, cli, control.URL, bearer, "bucket", "writes", "list", app.Slug, bucket.ID)
	} else {
		bucket, err = client.CreateObjectBucket(t.Context(), app.Slug, api.CreateObjectBucketRequest{Name: "assets"})
		if err != nil {
			t.Fatal(err)
		}
	}
	accounting := st.(state.ObjectStorageAccountingStore)
	catalog, err := st.(state.ObjectBucketStore).GetObjectBucket(t.Context(), acct.ID, app.ID, bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounting.ClaimObjectInventory(t.Context(), bucket.ID, "initial"); err != nil {
		t.Fatal(err)
	}
	if err := s.scanObjectInventory(t.Context(), accounting, catalog, "initial"); err != nil {
		t.Fatal(err)
	}
	size := int64(3)
	put, err := client.SignBucketObject(t.Context(), app.Slug, bucket.ID, api.ObjectSignRequest{Method: "PUT", Key: "file", SizeBytes: &size})
	if err != nil {
		t.Fatal("unsigned provider-report-free upload", err)
	}
	send := func(out api.ObjectSignedRequest, ranged bool) (int, string) {
		t.Helper()
		var body io.Reader
		if out.Method == "PUT" {
			body = strings.NewReader("abc")
		}
		r, err := http.NewRequestWithContext(t.Context(), out.Method, out.URL, body)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range out.Headers {
			r.Header.Set(key, value)
		}
		if ranged {
			r.Header.Set("Range", "bytes=0-0")
		}
		response, err := public.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(data)
	}
	if status, _ := send(put, false); status != 200 {
		t.Fatal("upload", status)
	}
	get, err := client.SignBucketObject(t.Context(), app.Slug, bucket.ID, api.ObjectSignRequest{Method: "GET", Key: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if status, data := send(get, false); status != 200 || data != "abc" {
		t.Fatal("download", status)
	}
	head, err := client.SignBucketObject(t.Context(), app.Slug, bucket.ID, api.ObjectSignRequest{Method: "HEAD", Key: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if status, data := send(head, false); status != 200 || data != "" {
		t.Fatal("HEAD", status)
	}
	if status, data := send(get, true); status != 206 || data != "a" {
		t.Fatal("range", status)
	}
	usage, err := client.GetObjectStorageUsage(t.Context())
	if err != nil || !usage.Usage.Fresh || usage.Usage.EgressBytes != 4 || usage.Usage.RequestCount < 4 || len(usage.Usage.UnavailableMeters) != 2 || usage.Charges != nil || usage.BillingMode != "off" {
		t.Fatal("gateway meters did not reach customer API", usage, err)
	}
	if status, _ := send(get, false); status != 402 {
		t.Fatal("replayed URL exceeded remaining egress budget", status)
	}
	usage, err = client.GetObjectStorageUsage(t.Context())
	if err != nil || usage.Usage.EgressBytes != 4 {
		t.Fatal("failed reservation changed egress", usage, err)
	}
	if status, _ := send(get, true); status != http.StatusPartialContent {
		t.Fatal("last remaining egress byte", status)
	}
	if _, err := client.SignBucketObject(t.Context(), app.Slug, bucket.ID, api.ObjectSignRequest{Method: "GET", Key: "file"}); err == nil {
		t.Fatal("exhausted egress budget still admitted a new URL")
	}
	if err := client.DeleteBucketObject(t.Context(), app.Slug, bucket.ID, "file"); err != nil {
		t.Fatal("cleanup after admission closure", err)
	}
	if err := client.DeleteObjectBucket(t.Context(), app.Slug, bucket.ID); err != nil {
		t.Fatal("bucket cleanup", err)
	}
	snapshot, err := accounting.ObjectUsage(t.Context(), acct.ID, time.Now())
	if err != nil || len(snapshot.Reports) != 0 {
		t.Fatal("journey required fabricated provider reports", err)
	}
	t.Log("provider-report-free API/gateway journey passed; no customer charges; fixture cleaned up")
}

func runGatewaySafetyCLI(t *testing.T, cli, endpoint, bearer string, args ...string) map[string]json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, cli, append([]string{"--json"}, args...)...)
	command.Dir = t.TempDir()
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "FAAS_") && !strings.HasPrefix(value, "GREGALE_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "FAAS_API="+endpoint, "FAAS_TOKEN="+bearer)
	output, err := command.Output()
	if err != nil {
		t.Fatal(fmt.Errorf("CLI %s failed: %w", strings.Join(args, " "), err))
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal("CLI response is not JSON", err)
	}
	return result
}

// adr: 627
func TestGatewaySafetyRejectsUnmeteredNativePaths(t *testing.T) {
	e := setup(t, api.PlanScale)
	provider := &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, provider, &fakeObjectProvider{}, "external"))
	since := time.Now().UTC().Add(-time.Second)
	e.s.objectStorage.Accounting.AccountingMode = api.ObjectStorageGatewaySafetyV1
	e.s.objectStorage.Accounting.GatewayMeteringSince = &since
	e.s.objectStorage.Accounting.MaxMonthlyCostMillicents = 0
	setS3Flag(t, e, true)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/fixture", nil)
	uri := "obj://" + uuid.NewString() + "/" + uuid.NewString() + "/file"
	if _, _, _, ok := e.s.loadJobManagedObject(w, r, e.acct, uri); ok || len(provider.accessed) != 0 || w.Code < 400 {
		t.Fatal("unmetered native job path remained enabled", w.Code)
	}
	if _, err := e.s.ensureProjectEnvironmentObjectStorageCloneBucket(t.Context(), e.acct, "test", projectEnvironmentBindingClone{}, nil); !errors.Is(err, errIsolatedObjectStorageCloneUnsupported) {
		t.Fatal("unmetered full-copy clone remained enabled", err)
	}
}

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type conditionalGCSWire struct {
	mu          sync.Mutex
	bytes       string
	headers     http.Header
	generation  int64
	heads, puts int
}

func (f *conditionalGCSWire) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/token":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"local-fixture-token","token_type":"Bearer","expires_in":3600}`)
		return
	case "/iam":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"signedBlob":"bG9jYWwtZml4dHVyZS1zaWduYXR1cmU="}`)
		return
	}
	if r.URL.Path != "/physical/key" {
		w.WriteHeader(404)
		return
	}
	if r.Method == "HEAD" {
		f.heads++
	} else if r.Method == "PUT" {
		f.puts++
		expected := r.Header.Get("X-Goog-If-Generation-Match")
		if expected != "" && expected != strconv.FormatInt(f.generation, 10) {
			w.WriteHeader(412)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		f.bytes, f.headers = string(body), r.Header.Clone()
		f.generation++
	} else {
		w.WriteHeader(405)
		return
	}
	if f.generation == 0 {
		w.WriteHeader(404)
		return
	}
	for name, values := range f.headers {
		if strings.HasPrefix(strings.ToLower(name), "x-goog-meta-") {
			w.Header()[name] = append([]string(nil), values...)
		}
	}
	w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, f.generation))
	w.Header().Set("X-Goog-Generation", strconv.FormatInt(f.generation, 10))
	if r.Method == "HEAD" {
		w.Header().Set("Content-Length", strconv.Itoa(len(f.bytes)))
	}
}

// adr: 732
func TestConditionalGCSCLIJourneyMem(t *testing.T) {
	e := setup(t, api.PlanScale)
	conditionalGCSCLIJourney(t, e.s, e.store, e.acct, e.key)
}

// adr: 732
func TestConditionalGCSCLIJourneyPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanScale)
	conditionalGCSCLIJourney(t, e.s, e.store, e.acct, e.key)
}

func conditionalGCSCLIJourney(t *testing.T, s *server, st state.Store, account state.Account, bearer string) {
	t.Helper()
	cli := os.Getenv("GREGALE_OBJECT_STORAGE_E2E_CLI")
	if cli == "" {
		t.Skip("built CLI required: GREGALE_OBJECT_STORAGE_E2E_CLI")
	}
	identity, teardown := withTestIdentities(t)
	defer teardown()
	native := &conditionalGCSWire{}
	provider := httptest.NewServer(http.HandlerFunc(native.serve))
	defer provider.Close()
	adc := filepath.Join(t.TempDir(), "local-adc.json")
	if err := os.WriteFile(adc, []byte(`{"type":"authorized_user","client_id":"fixture","client_secret":"fixture","refresh_token":"fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	// The real GCS adapter and its OAuth/IAM signer run against bounded local
	// wire fixtures. Refuse every remote destination; no provider is contacted.
	previous := http.DefaultTransport
	u, _ := url.Parse(provider.URL)
	http.DefaultTransport = objectCLIWireTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "iamcredentials.googleapis.com", "oauth2.googleapis.com", "accounts.google.com":
			isIAM := r.URL.Hostname() == "iamcredentials.googleapis.com"
			r = r.Clone(r.Context())
			copyURL := *r.URL
			r.URL = &copyURL
			r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
			r.URL.Path = "/token"
			if isIAM {
				r.URL.Path = "/iam"
			}
			r.URL.RawPath, r.URL.RawQuery, r.Host = "", "", u.Host
		case "127.0.0.1", "localhost", "::1":
		default:
			return nil, errors.New("remote destination forbidden in local GCS qualification")
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	var gateway http.Handler
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.ServeHTTP(w, r) }))
	defer public.Close()
	since := time.Now().UTC().Add(-time.Second)
	policy := api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since, MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyRequests: 5, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 900}
	config := objectstorage.Config{Transfer: objectstorage.ObjectTransferConfig{Profile: "proxied"}, PublicEndpoint: strings.Replace(public.URL, "http://", "https://", 1), PublicRegion: "us-east-1", Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "gcs"}, Backends: []objectstorage.BackendConfig{{ID: "gcs", Driver: "gcs", Region: "us-east-1", Namespace: "fixture-project", GCSLocation: "US-EAST1", GCSServiceAccount: "fixture@fixture-project.iam.gserviceaccount.com", Endpoint: provider.URL, AllowHTTP: true}}}
	registry, err := objectstorage.NewRegistry(config, os.Getenv, map[string]objectstorage.Factory{"gcs": objectstorage.NewGCS})
	if err != nil {
		t.Fatal(err)
	}
	s.WithObjectStorage(registry)
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	backend, _ := registry.Default("us-east-1")
	app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "conditional-cli", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	store := st.(state.ObjectStorageAccountingStore)
	b, err := st.(state.ObjectBucketStore).ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", PhysicalName: "physical", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.(state.ObjectBucketStore).ClaimObjectBucket(t.Context(), account.ID, app.ID, b.ID, "fixture", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := st.(state.ObjectBucketStore).FinishObjectBucket(t.Context(), b.ID, "fixture", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimObjectInventory(t.Context(), b.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectInventory(t.Context(), b.ID, "fixture", 0, 0); err != nil {
		t.Fatal(err)
	}
	gateway, err = s3gateway.New(s3gateway.Config{Registry: registry, Store: st.(s3gateway.Store), RequestMetrics: st.(state.ObjectStorageProviderUsageStore), HTTPClient: &http.Client{Transport: http.DefaultTransport, Timeout: registry.TransferTimeout()}, SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, OpenSecret: func(blob []byte) (string, error) {
		ns, plain, err := secretbox.OpenBytes(identity, blob)
		if err != nil || ns != s3gateway.CredentialSecretNamespace {
			return "", errors.New("invalid local credential")
		}
		return string(plain), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := httptest.NewRecorder()
		s.handler().ServeHTTP(out, r)
		for k, v := range out.Header() {
			w.Header()[k] = v
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(out.Code)
		_, _ = w.Write(bytes.ReplaceAll(out.Body.Bytes(), []byte(config.PublicEndpoint), []byte(public.URL)))
	}))
	defer control.Close()
	file := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(file, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(flags ...string) (map[string]json.RawMessage, error) {
		args := append([]string{"--json", "bucket", "upload", app.Slug, b.ID, "key", file}, flags...)
		command := exec.CommandContext(t.Context(), cli, args...)
		command.Dir = t.TempDir()
		command.Env = append(os.Environ(), "FAAS_API="+control.URL, "FAAS_TOKEN="+bearer)
		output, err := command.Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("CLI %q: %w (%s)", args, err, exit.Stderr)
		}
		var result map[string]json.RawMessage
		if err == nil {
			err = json.Unmarshal(output, &result)
		}
		return result, err
	}
	if out, err := run("--if-none-match", "*"); err != nil || string(out["status"]) != `"completed"` {
		t.Fatal("conditional create CLI", err)
	}
	// A native generation activates the existing all-version accounting fence.
	// Establish the fixture's one-version baseline through the durable inventory
	// contract before testing replacement; this is not provider-scan qualification.
	capacity := st.(state.ObjectCapacityStore)
	job, err := capacity.RequestObjectCapacityReconciliation(t.Context(), account.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal("request native baseline", err)
	}
	job, err = capacity.ClaimObjectCapacityReconciliation(t.Context(), job.ID, "fixture-native-baseline")
	if err != nil || job.State != "scanning" || job.InventoryScope != state.ObjectInventoryAllVersions {
		t.Fatal("claim native baseline", job, err)
	}
	identityHash := sha256.Sum256([]byte("key\x001"))
	job, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), job.ID, job.Token, "", []state.ObjectVersionInventoryRecord{{Identity: hex.EncodeToString(identityHash[:]), Bytes: 3}})
	if err != nil || job.State != "completed" {
		t.Fatal("establish native baseline", job, err)
	}
	if _, err := run("--if-none-match", "*"); err == nil || !strings.Contains(err.Error(), "HTTP 412") {
		t.Fatal("create-only did not reject the existing GCS object", err)
	}
	if _, err := run("--if-match", `"etag-1"`); err != nil {
		t.Fatal("conditional replace CLI", err)
	}
	// Admit the capability before another request consumes the last budget
	// slot. Its later ETag observation must be denied without contacting GCS.
	size := int64(3)
	blocked, err := api.NewClient(control.URL, bearer).SignBucketObject(t.Context(), app.Slug, b.ID, api.ObjectSignRequest{Method: "PUT", Key: "blocked", SizeBytes: &size, IfMatch: "*"})
	if err != nil {
		t.Fatal("prepare budget-bound conditional URL", err)
	}
	if _, err := run("--if-match", `"etag-1"`); err == nil || !strings.Contains(err.Error(), "HTTP 412") {
		t.Fatal("stale ETag did not produce a precondition rejection", err)
	}
	r, err := http.NewRequestWithContext(t.Context(), "PUT", blocked.URL, strings.NewReader("new"))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range blocked.Headers {
		r.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusPaymentRequired {
		t.Fatal("budget-exhausted observation admitted", response.StatusCode)
	}
	native.mu.Lock()
	if native.generation != 2 || native.puts != 3 || native.heads != 2 {
		t.Fatal("GCS CLI request bounds", native.generation, native.puts, native.heads)
	}
	native.mu.Unlock()
	usage, err := store.ObjectUsage(t.Context(), account.ID, time.Now())
	if err != nil || apiUsageRequests(usage, registry.Accounting) != 5 {
		t.Fatal("unmetered GCS request", err)
	}
	pending, err := st.(state.ObjectWriteReceiptStore).ListObjectWriteReceipts(t.Context(), account.ID, app.ID, b.ID, "pending", 10, "")
	if err != nil || len(pending.Items) != 0 {
		t.Fatal("known preflight rejection retained a pending receipt", err)
	}
}

type objectCLIWireTransport func(*http.Request) (*http.Response, error)

func (f objectCLIWireTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func apiUsageRequests(snapshot state.ObjectUsageSnapshot, policy api.ObjectStoragePolicy) int64 {
	return state.SummarizeObjectUsage(snapshot, policy, time.Now().UTC()).RequestCount
}

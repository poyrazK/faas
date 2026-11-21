package apidserver_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRunArtifactDeliveryHTTPMem(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	testRunArtifactDeliveryHTTP(t, *e.S(), *e.Store(), *e.Acct(), *e.Key())
}

func TestRunArtifactDeliveryHTTPPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	testRunArtifactDeliveryHTTP(t, *e.S(), *e.Store(), *e.Acct(), *e.Key())
}

func testRunArtifactDeliveryHTTP(t *testing.T, server *apidserver.Server, st state.Store, account state.Account, token string) {
	t.Helper()
	ctx := t.Context()
	identity, teardown := apidserver.WithTestIdentities(t)
	defer teardown()
	server.WithExecutionAPIEnabled(true).WithExecutionV2Enabled(true)
	var mu sync.Mutex
	var body []byte
	var headers http.Header
	writes := 0
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPut {
			body, _ = io.ReadAll(r.Body)
			headers = r.Header.Clone()
			writes++
		}
		if headers == nil {
			w.WriteHeader(404)
			return
		}
		for name, values := range headers {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") || name == "Content-Type" {
				w.Header()[name] = append([]string(nil), values...)
			}
		}
		w.Header().Set("ETag", `"delivered-report"`)
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	}))
	defer native.Close()
	_, bucket, _, _ := seedEncryptionJournalStorage(t, server, st, account, native.URL)
	routes := st.(state.ObjectUploadRouteStore)
	if _, err := routes.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: account.ID, AppID: bucket.AppID, BucketID: bucket.ID, Name: "reports", KeyPrefix: "reports", MaxBytes: 100, AllowedContentTypes: []string{"text/html"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: apidserver.RuntimeConfigS3, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: apidserver.BoolJSON(true), ApplyMode: state.RuntimeConfigApplyHot, Reason: "Run artifact delivery test"}); err != nil {
		t.Fatal(err)
	}
	if err := (*server.RuntimeConfig()).Reconcile(ctx, st); err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	request := func(method, path string, value any, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	definition := apidserver.ExecutionRequest()
	definition.OutputFiles = []string{"report.html"}
	accepted := request(http.MethodPost, "/v1/executions", definition, token)
	var run api.ExecutionResponse
	if accepted.Code != 202 || json.Unmarshal(accepted.Body.Bytes(), &run) != nil {
		t.Fatal("admit Run", accepted.Code, accepted.Body.String())
	}
	runs := st.(state.ExecutionStore)
	claim, err := runs.ClaimExecution(ctx, "test-node", time.Now().UTC(), time.Minute)
	if err != nil || claim.ID != run.ID {
		t.Fatal(claim.ID, err)
	}
	if _, err = runs.MarkExecutionRunning(ctx, run.ID, *claim.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	data := []byte("<h1>Customer report</h1>")
	sha := sha256.Sum256(data)
	artifact := api.ExecutionArtifact{Name: "report.html", Content: data, SizeBytes: len(data), SHA256: "sha256:" + hex.EncodeToString(sha[:])}
	if _, err = runs.CompleteExecution(ctx, state.CompleteExecutionParams{ID: run.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null"), Artifacts: []api.ExecutionArtifact{artifact}, Usage: api.ExecutionUsage{WallTimeMS: 1}, FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/encrypted-journal/run-artifact-deliveries/reports"
	req := api.DeliverRunArtifactRequest{IdempotencyKey: "report-one", ExecutionID: run.ID, ArtifactName: artifact.Name, SizeBytes: int64(artifact.SizeBytes), SHA256: artifact.SHA256, ContentType: "text/html"}
	promoted := request(http.MethodPost, path, req, token)
	var delivery api.RunArtifactDelivery
	if promoted.Code != 200 || json.Unmarshal(promoted.Body.Bytes(), &delivery) != nil || delivery.Status != "completed" || delivery.Object == nil {
		t.Fatal("deliver", promoted.Code, promoted.Body.String())
	}
	if writes != 1 || !bytes.Equal(body, data) {
		t.Fatal("wrong destination write", writes)
	}
	server.WithExecutionV2Enabled(false)
	replay := request(http.MethodPost, path, req, token)
	var again api.RunArtifactDelivery
	_ = json.Unmarshal(replay.Body.Bytes(), &again)
	if replay.Code != 200 || again.ID != delivery.ID || writes != 1 {
		t.Fatal("writer-disabled replay", replay.Code, replay.Body.String())
	}
	if got := request(http.MethodGet, delivery.ReceiptURL, nil, token); got.Code != 200 {
		t.Fatal("read receipt", got.Code, got.Body.String())
	}
	// URL issuance reads only destination authority. It works after Run writers stop.
	var edge http.Handler
	public := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { edge.ServeHTTP(w, r) }))
	defer public.Close()
	(*server.ObjectStorage()).PublicEndpoint = public.URL
	edge, err = s3gateway.New(s3gateway.Config{Registry: *server.ObjectStorage(), Store: st.(s3gateway.Store), RequestMetrics: st.(state.ObjectStorageProviderUsageStore), SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, OpenSecret: func(blob []byte) (string, error) {
		ns, plain, err := secretbox.OpenBytes(identity, blob)
		if err != nil || ns != s3gateway.CredentialSecretNamespace {
			return "", errors.New("invalid test credential")
		}
		return string(plain), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	signed := request(http.MethodPost, delivery.ReceiptURL+"/download-url", nil, token)
	var download api.ObjectSignedRequest
	if signed.Code != 200 || json.Unmarshal(signed.Body.Bytes(), &download) != nil {
		t.Fatal("sign download", signed.Code, signed.Body.String())
	}
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, download.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range download.Headers {
		get.Header.Set(name, value)
	}
	response, err := public.Client().Do(get)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck
	received, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !bytes.Equal(received, data) {
		t.Fatal("download", response.StatusCode, string(received), err)
	}
	other, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateAPIKey(ctx, account.ID, hash, "no-bucket-grant", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	if denied := request(http.MethodGet, delivery.ReceiptURL, nil, other); denied.Code != 403 {
		t.Fatal("missing bucket grant", denied.Code, denied.Body.String())
	}
	if writes != 1 {
		t.Fatal("download or replay repeated write")
	}
}

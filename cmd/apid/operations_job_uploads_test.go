//go:build !no_pg

// adr: 649
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const directJobCSV = "id,count\nalice,1\n"

func directJobDeclaration() api.OperationArtifactUploadRequest {
	return api.OperationArtifactUploadRequest{ReportID: "csv", Name: "export.csv", SizeBytes: int64(len(directJobCSV)), SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(directJobCSV)))}
}

func directJobServer(t *testing.T, kind string) (jobArtifactHTTPFixture, *api.Client, *api.Client) {
	t.Helper()
	f := newJobArtifactHTTPFixture(t, kind)
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.server.WithOperationArtifactStorage(backend)
	key, hash, _ := api.GenerateAPIKey()
	if _, err := f.store.CreateAPIKey(t.Context(), f.account.ID, hash, "direct-files", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(f.server.handler())
	t.Cleanup(host.Close)
	owner := api.NewClient(host.URL, key)
	token, err := owner.CreatePlatformTenantAccessToken(t.Context(), f.tenant.ID, api.CreatePlatformTenantAccessTokenRequest{Name: "browser", Scopes: []string{api.ScopePlatformTenantOperationsRead}})
	if err != nil {
		t.Fatal(err)
	}
	return f, api.NewClient(host.URL, ""), api.NewClient(host.URL, token.Token)
}

type directUploadLoss struct {
	base http.RoundTripper
	lost bool
}

func (r *directUploadLoss) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(req)
	if err == nil && !r.lost && strings.HasSuffix(req.URL.Path, "/artifact-uploads") && response.StatusCode == 200 {
		r.lost = true
		_ = response.Body.Close()
		return nil, errors.New("lost direct-upload acknowledgement")
	}
	return response, err
}

func TestOperationJobDirectUploadHTTP(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f, runtime, customer := directJobServer(t, kind)
			ctx, req := t.Context(), directJobDeclaration()
			absent, err := runtime.ReuseJobOperationUpload(ctx, f.op.ID, f.proof, req)
			if err != nil || absent.Available {
				t.Fatal("false receipt", err)
			}
			loss := &directUploadLoss{base: http.DefaultTransport}
			runtime.HTTPClient().Transport = loss
			if _, err := runtime.UploadJobOperationArtifact(ctx, f.op.ID, f.proof, req, strings.NewReader(directJobCSV)); err == nil || !loss.lost {
				t.Fatal("response loss not exercised", err)
			}
			found, err := runtime.ReuseJobOperationUpload(ctx, f.op.ID, f.proof, req)
			if err != nil || !found.Available || found.Artifact == nil {
				t.Fatal("durable receipt missing", err)
			}
			a := found.Artifact
			if a.URI != "operation://"+f.op.ID+"/artifacts/"+a.ID {
				t.Fatal("physical key exposed", a.URI)
			}
			// A replay returns the receipt without consuming a now-invalid body.
			repeated, err := runtime.UploadJobOperationArtifact(ctx, f.op.ID, f.proof, req, strings.NewReader("changed bytes"))
			if err != nil || repeated.Artifact.ID != a.ID {
				t.Fatal("receipt changed", err)
			}
			changed := req
			changed.Name = "changed.csv"
			if _, err := runtime.ReuseJobOperationUpload(ctx, f.op.ID, f.proof, changed); err == nil {
				t.Fatal("changed declaration accepted")
			}
			if _, err := customer.DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, a.ID, io.Discard); err == nil {
				t.Fatal("unconfirmed private file exposed")
			}
			if _, err := runtime.PrepareJobOperationResult(ctx, f.op.ID, f.proof, api.OperationJobReportRequest{ReportID: "result", Result: json.RawMessage(`{"file":"export.csv"}`)}); err != nil {
				t.Fatal(err)
			}
			if err := f.store.JobTaskCompleteClaimedWithLogs(ctx, f.op.JobRunID, 0, f.proof.InstanceID, f.lease, "succeeded", 0, "", "", "", false, time.Now()); err != nil {
				t.Fatal(err)
			}
			var downloaded bytes.Buffer
			if _, err := customer.DownloadPlatformTenantSelfOperationArtifact(ctx, f.op.ID, a.ID, &downloaded); err != nil || downloaded.String() != directJobCSV {
				t.Fatal("private download changed", err)
			}
			if _, err := runtime.ReuseJobOperationUpload(ctx, f.op.ID, f.proof, req); err == nil {
				t.Fatal("closed task proof accepted")
			}
		})
	}
}

type competingDirectStorage struct {
	storage.StorageBackend
	mu    sync.Mutex
	keys  []string
	ready chan struct{}
}

func (s *competingDirectStorage) Put(ctx context.Context, key string, reader io.Reader) error {
	if err := s.StorageBackend.Put(ctx, key, reader); err != nil {
		return err
	}
	s.mu.Lock()
	s.keys = append(s.keys, key)
	if len(s.keys) == 2 {
		close(s.ready)
	}
	s.mu.Unlock()
	select {
	case <-s.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestOperationJobDirectUploadConcurrentCopies(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f, runtime, _ := directJobServer(t, kind)
			backend := &competingDirectStorage{StorageBackend: f.server.operationArtifactStorage, ready: make(chan struct{})}
			f.server.WithOperationArtifactStorage(backend)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			results := make(chan api.OperationJobArtifactResponse, 2)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, err := runtime.UploadJobOperationArtifact(ctx, f.op.ID, f.proof, directJobDeclaration(), strings.NewReader(directJobCSV))
					if err != nil {
						t.Error(err)
						return
					}
					results <- r
				}()
			}
			wg.Wait()
			close(results)
			var id string
			count := 0
			for r := range results {
				count++
				if r.Artifact == nil || id != "" && r.Artifact.ID != id {
					t.Fatal("competing receipts")
				}
				id = r.Artifact.ID
			}
			if count != 2 || len(backend.keys) != 2 || backend.keys[0] == backend.keys[1] {
				t.Fatal("concurrent uploads overwrote one object")
			}
			op, _, err := f.store.(state.JobOperationStore).OperationForJobRun(ctx, f.op.JobRunID)
			if err != nil || len(op.ArtifactStorageKeys) != 1 || op.ReportCount != 1 || len(op.Artifacts) != 0 {
				t.Fatal("duplicate or public binding", err)
			}
			gc := f.store.(state.OperationResultBlobStore)
			orphan, err := gc.ClaimOperationArtifactCleanup(ctx, "losing-copy", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second))
			if err != nil || orphan.StorageKey == op.ArtifactStorageKeys[id] {
				t.Fatal("winner collected", err)
			}
			if err := gc.CompleteOperationArtifactCleanup(ctx, orphan.ID, orphan.LeaseToken); err != nil {
				t.Fatal(err)
			}
			if _, err := gc.ClaimOperationArtifactCleanup(ctx, "winner", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("retained winner collected", err)
			}
		})
	}
}

func TestOperationJobDirectUploadRevokedDuringCopy(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f, runtime, _ := directJobServer(t, kind)
			f.server.WithOperationArtifactStorage(&jobArtifactAfterCopy{StorageBackend: f.server.operationArtifactStorage, after: func() {
				if _, err := f.store.(state.PlatformTenantStore).SetPlatformTenantStatus(t.Context(), f.account.ID, f.tenant.ID, state.PlatformTenantSuspended); err != nil {
					t.Error(err)
				}
			}})
			if _, err := runtime.UploadJobOperationArtifact(t.Context(), f.op.ID, f.proof, directJobDeclaration(), strings.NewReader(directJobCSV)); err == nil {
				t.Fatal("revoked customer committed receipt")
			}
			op, _, err := f.store.(state.JobOperationStore).OperationForJobRun(t.Context(), f.op.JobRunID)
			if err != nil || len(op.JobArtifactReceipts) != 0 || len(op.ArtifactStorageKeys) != 0 {
				t.Fatal("revoked copy bound", err)
			}
			if _, err := f.store.(state.OperationResultBlobStore).ClaimOperationArtifactCleanup(t.Context(), "abandoned", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); err != nil {
				t.Fatal("abandoned upload lost", err)
			}
		})
	}
}

func TestOperationJobDirectUploadMetadata(t *testing.T) {
	valid := "report_id=csv&name=export.csv&size_bytes=3&sha256=sha256%3A" + strings.Repeat("a", 64)
	for _, query := range []string{valid + "&name=other", valid + "&uri=obj", strings.Replace(valid, "size_bytes=3", "size_bytes=-1", 1), strings.Replace(valid, "size_bytes=3", "size_bytes=03", 1), strings.Replace(valid, "size_bytes=3", "size_bytes=999999999999999999999", 1), valid + "%zz"} {
		r := httptest.NewRequest("POST", "/?"+query, nil)
		r.Header.Set("Content-Type", "application/octet-stream")
		if _, err := operationUploadDeclaration(r); err == nil {
			t.Fatalf("invalid metadata accepted: %q", query)
		}
	}
	r := httptest.NewRequest("POST", "/?"+valid, nil)
	r.Header.Set("Content-Type", "text/csv")
	if _, err := operationUploadDeclaration(r); err == nil {
		t.Fatal("wrong content type accepted")
	}
	r.Header.Set("Content-Type", "application/octet-stream")
	if _, err := operationUploadDeclaration(r); err != nil {
		t.Fatal(err)
	}
}

type interruptedDirectStorage struct {
	storage.StorageBackend
	mu   sync.Mutex
	keys []string
}

func (s *interruptedDirectStorage) Put(ctx context.Context, key string, reader io.Reader) error {
	if err := s.StorageBackend.Put(ctx, key, reader); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
	if len(s.keys) == 1 {
		return errors.New("private storage acknowledgement lost")
	}
	return nil
}

func TestOperationJobDirectUploadInterruptedStorage(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f, runtime, _ := directJobServer(t, kind)
			backend := &interruptedDirectStorage{StorageBackend: f.server.operationArtifactStorage}
			f.server.WithOperationArtifactStorage(backend)
			if _, err := runtime.UploadJobOperationArtifact(t.Context(), f.op.ID, f.proof, directJobDeclaration(), strings.NewReader(directJobCSV)); err == nil {
				t.Fatal("lost storage acknowledgement not exercised")
			}
			found, err := runtime.ReuseJobOperationUpload(t.Context(), f.op.ID, f.proof, directJobDeclaration())
			if err != nil || found.Available {
				t.Fatal("uncommitted storage write returned receipt", err)
			}
			winner, err := runtime.UploadJobOperationArtifact(t.Context(), f.op.ID, f.proof, directJobDeclaration(), strings.NewReader(directJobCSV))
			if err != nil || !winner.Available {
				t.Fatal("interrupted transfer could not recover", err)
			}
			backend.mu.Lock()
			keys := append([]string(nil), backend.keys...)
			backend.mu.Unlock()
			if len(keys) != 2 || keys[0] == keys[1] {
				t.Fatal("private upload retried over existing object")
			}
			orphan, err := f.store.(state.OperationResultBlobStore).ClaimOperationArtifactCleanup(t.Context(), "interrupted-copy", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second))
			if err != nil || orphan.StorageKey != keys[0] {
				t.Fatal("interrupted storage copy leaked", err)
			}
		})
	}
}

type cancelDirectRead struct {
	io.Reader
	once   sync.Once
	cancel func()
}

func (r *cancelDirectRead) Read(p []byte) (int, error) { r.once.Do(r.cancel); return r.Reader.Read(p) }

func TestOperationJobDirectUploadCancellationWhileReceiving(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f, _, _ := directJobServer(t, kind)
			backend := &interruptedDirectStorage{StorageBackend: f.server.operationArtifactStorage}
			f.server.WithOperationArtifactStorage(backend)
			body := io.NopCloser(&cancelDirectRead{Reader: strings.NewReader(directJobCSV), cancel: func() {
				if _, err := f.store.(state.OperationStore).CancelOperation(t.Context(), f.account.ID, f.tenant.ID, f.op.ID, 1); err != nil {
					t.Error(err)
				}
			}})
			a := state.JobOperationAuthority{RunID: f.proof.RunID, InstanceID: f.proof.InstanceID, Generation: f.proof.Generation, Attempt: f.proof.Attempt, Capability: f.proof.Capability}
			if _, err := f.server.retainJobOperationUpload(t.Context(), f.store.(state.OperationJobArtifactStore), f.op.ID, a, directJobDeclaration(), body); err == nil {
				t.Fatal("cancelled task committed a receipt")
			}
			backend.mu.Lock()
			count := len(backend.keys)
			backend.mu.Unlock()
			if count != 0 {
				t.Fatal("cancelled task wrote private storage")
			}
			f.server.operationArtifactBudget.mu.Lock()
			transfers := f.server.operationArtifactBudget.transfers
			f.server.operationArtifactBudget.mu.Unlock()
			if transfers != 0 {
				t.Fatal("cancelled upload leaked spool budget")
			}
			if _, err := f.store.(state.OperationResultBlobStore).ClaimOperationArtifactCleanup(t.Context(), "cancelled-upload", time.Now().Add(api.OperationArtifactStagingLifetime+time.Second)); err != nil {
				t.Fatal("cancelled staging intent lost", err)
			}
		})
	}
}

func TestOperationJobDirectUploadIntegrity(t *testing.T) {
	for _, kind := range []string{"memory", "postgres"} {
		t.Run(kind, func(t *testing.T) {
			f, runtime, _ := directJobServer(t, kind)
			for _, data := range []string{"short", directJobCSV + "extra", strings.ReplaceAll(directJobCSV, "alice", "other")} {
				if _, err := runtime.UploadJobOperationArtifact(t.Context(), f.op.ID, f.proof, directJobDeclaration(), strings.NewReader(data)); err == nil {
					t.Fatal("changed bytes accepted")
				}
			}
			proof := f.proof
			proof.Generation++
			if _, err := runtime.UploadJobOperationArtifact(t.Context(), f.op.ID, proof, directJobDeclaration(), strings.NewReader(directJobCSV)); err == nil {
				t.Fatal("stale proof accepted")
			}
			found, err := runtime.ReuseJobOperationUpload(t.Context(), f.op.ID, f.proof, directJobDeclaration())
			if err != nil || found.Available {
				t.Fatal("invalid upload retained", err)
			}
		})
	}
}

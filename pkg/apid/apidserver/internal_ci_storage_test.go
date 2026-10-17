package apidserver_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

type apiCIPhysical struct {
	mu      sync.Mutex
	data    map[string][]byte
	retired map[string]bool
}

func (p *apiCIPhysical) Publish(_ context.Context, key string, _ ci.RuntimeObject, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired[key] {
		return errors.New("retired")
	}
	if old, ok := p.data[key]; ok && !bytes.Equal(old, data) {
		return errors.New("changed")
	}
	p.data[key] = bytes.Clone(data)
	return nil
}
func (p *apiCIPhysical) Open(_ context.Context, key string) (io.ReadCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, ok := p.data[key]
	if !ok || p.retired[key] {
		return nil, errors.New("missing")
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(data))), nil
}
func (p *apiCIPhysical) Retire(_ context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.data, key)
	p.retired[key] = true
	return nil
}

type apiCISource struct{ blobs *ci.ManagedBlobs }

func (s apiCISource) Stage(ctx context.Context, req ci.JobsSourceRequest) (ci.SourceArchive, error) {
	data := []byte("synthetic verified source; no command is executed")
	digest := apiCIDigest(data)
	key := fmt.Sprintf("ci/accounts/%s/executions/%s/attempts/%s/source/%s.tar.gz", req.Key.AccountID, req.Key.ExecutionID, req.Key.AttemptID, strings.TrimPrefix(digest, "sha256:"))
	err := s.blobs.Put(ctx, key, bytes.NewReader(data))
	return ci.SourceArchive{Key: key, Digest: digest, Bytes: int64(len(data))}, err
}
func apiCIDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestInternalCIStorageRoutes(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := (*e.Store()).(*state.PgStore)
	ctx := t.Context()
	image := "registry.example/ci@sha256:" + strings.Repeat("a", 64)
	job, err := store.JobCreate(ctx, e.Acct().ID, "ci-wire", "batch", image, []string{ci.JobsRunnerPath, ci.JobsRunnerProtocol}, api.CIJobsRAMMB, 600, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	pool := *e.Pool()
	if _, err := pool.Exec(ctx, "UPDATE jobs SET image_materialization_status='ready',image_resolved_digest=$2,image_storage_key=$3 WHERE id=$1", job.ID, "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+"/image.ext4"); err != nil {
		t.Fatal(err)
	}
	ciStore := ci.NewPostgresStore(pool)
	physical := &apiCIPhysical{data: map[string][]byte{}, retired: map[string]bool{}}
	blobs := &ci.ManagedBlobs{Store: ciStore, Physical: physical}
	cfg := ci.ConfigFromEnv(func(key string) string {
		switch key {
		case ci.InternalEnv, ci.GitHubEnv, ci.DispatchEnv, ci.TransportEnv:
			return "1"
		case ci.AccountsEnv:
			return e.Acct().ID
		}
		return ""
	})
	revision, err := ciStore.PutRevision(ctx, e.Acct().ID, "wire", ci.Definition{Version: 1, Repository: "gregale/wire", Jobs: map[string]ci.Job{"test": {Image: image, TimeoutSeconds: 600, Steps: []ci.Step{{Name: "unit", Run: "synthetic step; never executed"}}, Artifacts: []string{"reports/**"}}}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ciStore.PutGitHubBinding(ctx, e.Acct().ID, "source", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 12, RepositoryID: 34, PushBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	verify := func(context.Context, string, int64, int64, string) error { return nil }
	row, _, err := ci.AdmitJobs(ctx, cfg, ciStore, verify, e.Acct().ID, "fresh", ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{RevisionID: revision.ID, CommitSHA: strings.Repeat("a", 40)}, BindingID: binding.ID, Templates: map[string]string{"test": job.ID}})
	if err != nil {
		t.Fatal(err)
	}
	backend := ci.JobsBackend{Config: cfg, Store: ciStore, Source: apiCISource{blobs}, Output: ci.SealedJobsOutput{Store: ciStore, Blobs: blobs}}
	row, err = (ci.Engine{Store: ciStore, Backend: backend}).Tick(ctx, row.AccountID, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := row.Jobs[0].Attempt.ID
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, lease := uuid.NewString(), uuid.NewString()
	if _, err := store.CreateAndClaimJobInstance(ctx, instance, job.ID, run, 0, string(state.StateColdBooting), api.CIJobsRAMMB, node.ID, instance, lease, time.Now().Add(10*time.Minute), node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishInstanceRuntime(ctx, instance, string(state.StateColdBooting), "ci-wire", "10.100.0.25", 20025); err != nil {
		t.Fatal(err)
	}
	proof, err := ciStore.IssueJobsRuntime(ctx, cfg, ci.RuntimeHostProof{RunID: run, InstanceID: instance, LeaseToken: lease})
	if err != nil {
		t.Fatal(err)
	}
	view, err := ciStore.AuthorizeJobsRuntime(ctx, cfg, proof, ci.RuntimeControl)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "https://" + server.Listener.Addr().String()
	disabledConfig := cfg
	disabledConfig.TransportEnabled = false
	(*e.S()).WithInternalCIConfig(disabledConfig).WithInternalCIStorage(blobs, origin)
	disabled := httptest.NewRecorder()
	disabledRequest := httptest.NewRequest(http.MethodGet, origin+"/v1/internal/ci/runtime/"+run+"/source", nil)
	disabledRequest.Header.Set(ci.RuntimeInstanceHeader, proof.InstanceID)
	disabledRequest.Header.Set(ci.RuntimeCapabilityHeader, proof.Capability)
	(*e.S()).Handler().ServeHTTP(disabled, disabledRequest)
	if disabled.Code != http.StatusNotFound {
		t.Fatal("disabled CI transport exposed a mounted route")
	}
	disabledProgress := httptest.NewRecorder()
	disabledProgressRequest := httptest.NewRequest(http.MethodPut, origin+"/v1/internal/ci/runtime/"+run+"/progress", strings.NewReader(`{}`))
	disabledProgressRequest.Header.Set(ci.RuntimeInstanceHeader, proof.InstanceID)
	disabledProgressRequest.Header.Set(ci.RuntimeCapabilityHeader, proof.Capability)
	(*e.S()).Handler().ServeHTTP(disabledProgress, disabledProgressRequest)
	if disabledProgress.Code != http.StatusNotFound {
		t.Fatal("disabled CI transport exposed progress")
	}
	disabledInput := httptest.NewRecorder()
	disabledInputRequest := httptest.NewRequest(http.MethodGet, origin+"/v1/internal/ci/runtime/"+run+"/inputs/report", nil)
	(*e.S()).Handler().ServeHTTP(disabledInput, disabledInputRequest)
	if disabledInput.Code != http.StatusNotFound {
		t.Fatal("disabled CI transport exposed dependency inputs")
	}
	(*e.S()).WithInternalCIConfig(cfg).WithInternalCIStorage(blobs, origin)
	server.Config.Handler = (*e.S()).Handler()
	server.StartTLS()
	defer server.Close()
	request := func(method, suffix, contentType string, data []byte) *http.Request {
		r, err := http.NewRequestWithContext(ctx, method, origin+"/v1/internal/ci/runtime/"+run+"/"+suffix, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set(ci.RuntimeInstanceHeader, proof.InstanceID)
		r.Header.Set(ci.RuntimeCapabilityHeader, proof.Capability)
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		return r
	}
	response, err := server.Client().Do(request(http.MethodGet, "source", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	source, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || apiCIDigest(source) != view.Manifest.Source.Digest {
		t.Fatal("TLS source route did not use scoped managed storage")
	}
	bad := request(http.MethodGet, "source", "", nil)
	bad.Header.Set("Authorization", "Bearer ignored")
	response, err = server.Client().Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("guest route accepted mixed account authority")
	}
	progressRaw, _ := json.Marshal(ci.ProgressUpdate{Sequence: 1, Logs: []byte("wire "), Steps: []ci.StepResult{{Name: "unit", Status: ci.StatusRunning}}})
	for range 2 {
		response, err = server.Client().Do(request(http.MethodPut, "progress", "application/json", progressRaw))
		if err != nil {
			t.Fatal(err)
		}
		var receipt ci.ProgressReceipt
		decodeErr := json.NewDecoder(response.Body).Decode(&receipt)
		_ = response.Body.Close()
		if decodeErr != nil || response.StatusCode != http.StatusOK || receipt.Sequence != 1 || receipt.NextOffset != 5 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("mounted HTTPS progress: %d %+v %v", response.StatusCode, receipt, decodeErr)
		}
	}
	row, err = (ci.Engine{Store: ciStore, Backend: backend}).Tick(ctx, row.AccountID, row.ID)
	if err != nil || row.Status != ci.StatusRunning || string(row.Jobs[0].Attempt.Logs) != "wire " {
		t.Fatalf("live progress did not reconcile: %v", err)
	}
	liveURL := origin + "/v1/internal/ci/executions/" + row.ID + "/attempts/" + run + "/logs?offset=0&limit=16"
	liveRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, liveURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	liveRequest.Header.Set("Authorization", "Bearer "+*e.Key())
	e.AddAdminSession(t, liveRequest)
	response, err = server.Client().Do(liveRequest)
	if err != nil {
		t.Fatal(err)
	}
	var livePage ci.LogPage
	decodeErr := json.NewDecoder(response.Body).Decode(&livePage)
	_ = response.Body.Close()
	if decodeErr != nil || response.StatusCode != http.StatusOK || livePage.Complete || livePage.NextOffset != 5 || string(livePage.Data) != "wire " {
		t.Fatal("authenticated live logs missing or claimed completion")
	}
	prefix, _, _ := strings.Cut(view.Manifest.Source.Key, "source/")
	zero := 0
	result := ci.JobsResultManifest{Version: 1, AccountID: row.AccountID, ExecutionID: row.ID, AttemptID: run, CommitSHA: row.CommitSHA, Status: ci.StatusSucceeded, ExitCode: &zero, Steps: []ci.StepResult{{Name: "unit", Status: ci.StatusSucceeded, ExitCode: &zero}}}
	for name, data := range map[string][]byte{"logs.bin": []byte("wire logs"), "artifacts/reports/result.txt": []byte("wire report")} {
		r := request(http.MethodPut, "objects", "application/octet-stream", data)
		r.Header.Set(ci.RuntimeObjectHeader, name)
		r.Header.Set(ci.RuntimeDigestHeader, apiCIDigest(data))
		response, err = server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("scoped upload failed: %d", response.StatusCode)
		}
		key := prefix + "output/" + name
		if name == "logs.bin" {
			result.Logs = ci.SourceArchive{Key: key, Digest: apiCIDigest(data), Bytes: int64(len(data))}
		} else {
			result.Artifacts = []ci.JobsArtifact{{ArtifactRef: ci.ArtifactRef{Path: "reports/result.txt", Digest: apiCIDigest(data), Bytes: int64(len(data))}, Key: key}}
		}
	}
	raw, _ := json.Marshal(result)
	response, err = server.Client().Do(request(http.MethodPut, "result", "application/json", raw))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("seal failed: %d", response.StatusCode)
	}
	if _, err := pool.Exec(ctx, "UPDATE job_tasks SET status='succeeded',exit_code=0,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", run); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE instances SET state='stopped' WHERE id=$1", instance); err != nil {
		t.Fatal(err)
	}
	row, err = (ci.Engine{Store: ciStore, Backend: backend}).Tick(ctx, row.AccountID, row.ID)
	if err != nil || row.Status != ci.StatusSucceeded {
		t.Fatalf("managed output did not reconcile: %v", err)
	}
	download := origin + "/v1/internal/ci/executions/" + row.ID + "/jobs/test/artifacts/reports/result.txt"
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, download, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+*e.Key())
	e.AddAdminSession(t, r)
	response, err = server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(artifact) != "wire report" || !strings.Contains(response.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("authenticated artifact unavailable: %d", response.StatusCode)
	}
	readKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, e.Acct().ID, hash, "ci-low-scope", []string{"apps:read"}); err != nil {
		t.Fatal(err)
	}
	r, err = http.NewRequestWithContext(ctx, http.MethodGet, download, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+readKey)
	response, err = server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("artifact accepted insufficient scope: %d", response.StatusCode)
	}
}

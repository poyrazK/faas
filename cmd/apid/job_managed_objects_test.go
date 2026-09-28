package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jobresult"
)

type jobObjectReader struct{ body string }

func (r jobObjectReader) ReadObject(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.body)), nil
}

func TestJobManagedObjectManifestAndVerification(t *testing.T) {
	uri := "obj://123e4567-e89b-12d3-a456-426614174000/123e4567-e89b-12d3-a456-426614174001/results/task.json"
	app, bucket, key, err := parseJobManagedObjectURI(uri)
	if err != nil || app == "" || bucket == "" || key != "results/task.json" {
		t.Fatalf("parse = %q %q %q, %v", app, bucket, key, err)
	}
	if _, _, _, err := parseJobManagedObjectURI("obj://other-account/bucket/key"); err == nil {
		t.Fatal("accepted non-UUID bucket identity")
	}
	body := `[{"input_id":"first","input_ref":"data/first"}]`
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
	inputs, err := readJobInputManifest(context.Background(), jobObjectReader{body}, bucket, key, digest)
	if err != nil || len(inputs) != 1 || inputs[0].ID != "first" {
		t.Fatalf("inputs = %+v, %v", inputs, err)
	}
	if _, err := readJobInputManifest(context.Background(), jobObjectReader{body}, bucket, key, "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("accepted wrong input checksum")
	}
	artifact := jobresult.Artifact{Name: "part", URI: uri, SizeBytes: int64(len(body)), SHA256: digest}
	if _, err := verifyJobManagedArtifact(context.Background(), jobObjectReader{body}, bucket, key, artifact); err != nil {
		t.Fatal(err)
	}
	artifact.SizeBytes++
	if _, err := verifyJobManagedArtifact(context.Background(), jobObjectReader{body}, bucket, key, artifact); err == nil {
		t.Fatal("accepted wrong artifact size")
	}
}

func TestJobManagedObjectsThroughRunAndDownloadAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	provider := &fakeObjectProvider{objects: map[string][]byte{}}
	e.s.WithObjectStorage(objectRegistry(t, provider, &fakeObjectProvider{}, "external"))
	setS3Flag(t, e, true)
	bucket := reserveRecoveryBucket(t, e)
	ctx := context.Background()
	if _, err := e.store.ClaimObjectBucket(ctx, bucket.AccountID, bucket.AppID, bucket.ID, "job-seed", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.FinishObjectBucket(ctx, bucket.ID, "job-seed", "ready"); err != nil {
		t.Fatal(err)
	}
	qualifyObjectAccounting(t, e, bucket.ID)

	name := seedJob(t, e, "managed-object-job", "registry.example/worker:v1")
	uriPrefix := fmt.Sprintf("obj://%s/%s/", bucket.AppID, bucket.ID)
	inputsBody := []byte(`[{"input_id":"first","input_ref":"data/first"},{"input_id":"second","input_ref":"data/second"}]`)
	provider.objects["inputs.json"] = inputsBody
	inputSHA := fmt.Sprintf("sha256:%x", sha256.Sum256(inputsBody))
	request := api.CreateJobRunRequest{InputManifestURI: uriPrefix + "inputs.json", InputManifestSHA256: inputSHA}
	path := "/v1/jobs/" + name + "/runs"
	bad := request
	bad.InputManifestSHA256 = "sha256:" + strings.Repeat("0", 64)
	if rec := e.do(t, http.MethodPost, path, bad, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong manifest digest = %d: %s", rec.Code, rec.Body.String())
	}
	missing := request
	missing.InputManifestURI = uriPrefix + "missing.json"
	if rec := e.do(t, http.MethodPost, path, missing, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing input manifest = %d: %s", rec.Code, rec.Body.String())
	}
	rec := e.do(t, http.MethodPost, path, request, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create external-input run = %d: %s", rec.Code, rec.Body.String())
	}
	var run api.JobRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Tasks != 2 || run.InputManifestURI != request.InputManifestURI || run.InputManifestSHA256 != inputSHA || run.InputDigest == "" {
		t.Fatalf("run manifest evidence = %+v", run)
	}
	rec = e.do(t, http.MethodGet, path+"/"+run.ID+"/tasks", nil, nil)
	var listed api.ListJobTasksResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Tasks) != 2 ||
		listed.Tasks[0].InputID != "first" || listed.Tasks[1].InputID != "second" {
		t.Fatalf("assigned inputs = %d %s", rec.Code, rec.Body.String())
	}

	output := []byte("verified result bytes")
	provider.objects["outputs/first.bin"] = output
	outputSHA := fmt.Sprintf("sha256:%x", sha256.Sum256(output))
	manifest, err := json.Marshal(jobresult.Manifest{Version: 1, Artifacts: []jobresult.Artifact{{
		Name: "result", URI: uriPrefix + "outputs/first.bin", SizeBytes: int64(len(output)), SHA256: outputSHA,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	instanceID, lease := uuid.NewString(), uuid.NewString()
	if err := e.store.JobTaskMarkClaimed(ctx, run.ID, 0, instanceID, lease, time.Now().Add(time.Minute), "test-node"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.JobTaskCompleteClaimedWithLogs(ctx, run.ID, 0, instanceID, lease,
		"succeeded", 0, "", "", "", false, time.Now(), manifest); err != nil {
		t.Fatal(err)
	}
	downloadPath := fmt.Sprintf("%s/%s/tasks/0/artifacts/result/download", path, run.ID)
	rec = e.do(t, http.MethodGet, downloadPath, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("managed download = %d: %s", rec.Code, rec.Body.String())
	}
	var download api.JobArtifactDownloadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &download); err != nil || download.SHA256 != outputSHA || download.SizeBytes != int64(len(output)) || download.Download.URL == "" {
		t.Fatalf("download response = %+v, %v", download, err)
	}
	provider.objects["outputs/first.bin"] = []byte("changed")
	if rec := e.do(t, http.MethodGet, downloadPath, nil, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("changed output object = %d: %s", rec.Code, rec.Body.String())
	}
	delete(provider.objects, "outputs/first.bin")
	if rec := e.do(t, http.MethodGet, downloadPath, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing output object = %d: %s", rec.Code, rec.Body.String())
	}
}

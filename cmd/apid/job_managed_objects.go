package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jobresult"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

var errJobManagedArtifactMismatch = errors.New("output artifact size or checksum mismatch")
var errJobInputManifestInvalid = errors.New("invalid job input manifest")

// obj://<app-id>/<bucket-id>/<key> is an account-scoped reference to an
// existing Gregale object. UUIDs keep parsing unambiguous; object keys are
// passed to the provider without path normalization.
func parseJobManagedObjectURI(raw string) (appID, bucketID, key string, err error) {
	if len(raw) > 2048 || strings.Contains(raw, "%") {
		return "", "", "", fmt.Errorf("invalid managed object URI")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "obj" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", "", "", fmt.Errorf("invalid managed object URI")
	}
	appID = u.Hostname()
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", "", fmt.Errorf("invalid managed object URI")
	}
	bucketID, key = parts[0], parts[1]
	if _, err := uuid.Parse(appID); err != nil {
		return "", "", "", fmt.Errorf("invalid app id")
	}
	if _, err := uuid.Parse(bucketID); err != nil {
		return "", "", "", fmt.Errorf("invalid bucket id")
	}
	if err := (objectstorage.SignRequest{Method: "GET", Key: key}).Validate(1); err != nil {
		return "", "", "", err
	}
	return appID, bucketID, key, nil
}

func (s *server) loadJobManagedObject(w http.ResponseWriter, r *http.Request, acct state.Account, uri string) (state.ObjectBucket, objectstorage.Provider, string, bool) {
	appID, bucketID, key, err := parseJobManagedObjectURI(uri)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid object URI", err.Error()))
		return state.ObjectBucket{}, nil, "", false
	}
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return state.ObjectBucket{}, nil, "", false
	}
	if s.objectStorage.Accounting.GatewaySafety() {
		// These paths read directly and can return reusable native URLs;
		// neither is covered by the prospective gateway safety meter.
		bucketProblem(w, objectstorage.ErrUnsupported)
		return state.ObjectBucket{}, nil, "", false
	}
	st, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return state.ObjectBucket{}, nil, "", false
	}
	bucket, err := st.GetObjectBucket(r.Context(), acct.ID, appID, bucketID)
	if err != nil {
		bucketProblem(w, err)
		return state.ObjectBucket{}, nil, "", false
	}
	if bucket.State != "ready" {
		bucketProblem(w, state.ErrConflict)
		return state.ObjectBucket{}, nil, "", false
	}
	if !s.authorizeBucketData(w, r, bucket, state.ObjectBucketPermissionRead) {
		return state.ObjectBucket{}, nil, "", false
	}
	backend, err := s.objectStorage.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		bucketProblem(w, err)
		return state.ObjectBucket{}, nil, "", false
	}
	return bucket, backend.Provider, key, true
}

func readJobInputManifest(ctx context.Context, reader objectstorage.ObjectReader, bucket, key, expectedDigest string) ([]api.JobRunInput, error) {
	if len(expectedDigest) != len("sha256:")+64 || !strings.HasPrefix(expectedDigest, "sha256:") {
		return nil, fmt.Errorf("%w: sha256 is required", errJobInputManifestInvalid)
	}
	stream, err := reader.ReadObject(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	data, err := io.ReadAll(io.LimitReader(stream, api.JobInputManifestMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > api.JobInputManifestMaxBytes {
		return nil, fmt.Errorf("%w: exceeds %d bytes", errJobInputManifestInvalid, api.JobInputManifestMaxBytes)
	}
	if fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != expectedDigest {
		return nil, fmt.Errorf("%w: checksum mismatch", errJobInputManifestInvalid)
	}
	var inputs []api.JobRunInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inputs); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON: %w", errJobInputManifestInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON content", errJobInputManifestInvalid)
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: empty input set", errJobInputManifestInvalid)
	}
	return inputs, nil
}

func verifyJobManagedArtifact(ctx context.Context, reader objectstorage.ObjectReader, bucket, key string, artifact jobresult.Artifact) (int64, error) {
	if artifact.SizeBytes < 0 {
		return 0, errJobManagedArtifactMismatch
	}
	stream, err := reader.ReadObject(ctx, bucket, key)
	if err != nil {
		return 0, err
	}
	hasher := sha256.New()
	// One byte past the declared size is enough to reject an oversized object.
	// This keeps a bogus small manifest from forcing a full read of a very
	// large object merely to prove the size does not match.
	readLimit := artifact.SizeBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	size, copyErr := io.Copy(hasher, io.LimitReader(stream, readLimit))
	closeErr := stream.Close()
	if copyErr != nil {
		return 0, copyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if size != artifact.SizeBytes || fmt.Sprintf("sha256:%x", hasher.Sum(nil)) != artifact.SHA256 {
		return 0, errJobManagedArtifactMismatch
	}
	return size, nil
}

// downloadJobArtifact verifies the current managed object bytes before
// returning a short-lived, account-authorized GET capability. A changed or
// missing object never receives a download URL.
func (s *server) downloadJobArtifact(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	taskIndex, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil || taskIndex < 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid task index", "task index must be non-negative"))
		return
	}
	job, run, ok, err := s.resolveJobRun(r.Context(), r.PathValue("id"), acct)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve job run"))
		return
	}
	if !ok || job.Name != r.PathValue("name") {
		s.notFound(w, "no such run")
		return
	}
	task, err := s.store.JobTaskGet(r.Context(), run.ID, taskIndex)
	if err != nil {
		s.notFound(w, "no such task")
		return
	}
	if task.Status != "succeeded" || len(task.OutputManifest) == 0 {
		s.notFound(w, "no such output artifact")
		return
	}
	manifest, err := jobresult.Validate(task.OutputManifest)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("invalid stored output manifest"))
		return
	}
	var artifact *jobresult.Artifact
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Name == r.PathValue("artifact") {
			artifact = &manifest.Artifacts[i]
			break
		}
	}
	if artifact == nil {
		s.notFound(w, "no such output artifact")
		return
	}
	if !strings.HasPrefix(artifact.URI, "obj://") {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Externally managed artifact", "download links are available for obj:// artifacts"))
		return
	}
	bucket, provider, key, ok := s.loadJobManagedObject(w, r, acct, artifact.URI)
	if !ok {
		return
	}
	reader, ok := provider.(objectstorage.ObjectReader)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return
	}
	size, err := verifyJobManagedArtifact(r.Context(), reader, bucket.PhysicalName, key, *artifact)
	if err != nil {
		if errors.Is(err, errJobManagedArtifactMismatch) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Output artifact verification failed", "the object bytes differ from the task's output manifest"))
		} else {
			bucketProblem(w, err)
		}
		return
	}
	sign := objectstorage.SignRequest{Method: http.MethodGet, Key: key, ExpiresIn: api.JobArtifactDownloadURLExpiresSec}
	if err := s.admitObjectURL(r.Context(), bucket, sign); err != nil {
		bucketProblem(w, err)
		return
	}
	signed, err := provider.Presign(r.Context(), bucket.PhysicalName, sign)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.JobArtifactDownloadResponse{
		Name: artifact.Name, SizeBytes: size, SHA256: artifact.SHA256,
		VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano), Download: signed,
	})
}

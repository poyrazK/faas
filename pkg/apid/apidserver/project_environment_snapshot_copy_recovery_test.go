package apidserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type snapshotRecoveryStore interface {
	state.ProjectEnvironmentCloneObjectManifestStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateProject(context.Context, state.Project) (state.Project, error)
	CreateProjectEnvironmentCloneOperation(context.Context, state.ProjectEnvironmentCloneOperation) (state.ProjectEnvironmentCloneOperation, error)
	AdvanceProjectEnvironmentCloneOperation(context.Context, string, string, string, string, string, int64, []state.ProjectEnvironmentCloneResource, string) (state.ProjectEnvironmentCloneOperation, error)
}

type snapshotCheckpointFailure struct {
	backing state.ProjectEnvironmentCloneObjectManifestStore
	fail    bool
}

func (s *snapshotCheckpointFailure) ProjectEnvironmentCloneObjectManifest(ctx context.Context, account, project, operation, source string) (state.ProjectEnvironmentCloneObjectManifest, error) {
	return s.backing.ProjectEnvironmentCloneObjectManifest(ctx, account, project, operation, source)
}
func (s *snapshotCheckpointFailure) PutProjectEnvironmentCloneObjectManifest(ctx context.Context, account, project string, manifest state.ProjectEnvironmentCloneObjectManifest) (state.ProjectEnvironmentCloneObjectManifest, error) {
	return s.backing.PutProjectEnvironmentCloneObjectManifest(ctx, account, project, manifest)
}
func (s *snapshotCheckpointFailure) MarkProjectEnvironmentCloneObjectCopied(ctx context.Context, account, project, operation, source, key, version, etag, sum string) error {
	if s.fail {
		s.fail = false
		return errors.New("checkpoint persistence unavailable")
	}
	return s.backing.MarkProjectEnvironmentCloneObjectCopied(ctx, account, project, operation, source, key, version, etag, sum)
}

type cloneSnapshotNative struct {
	mu      sync.Mutex
	size    int64
	copies  int
	receipt string
}

func (f *cloneSnapshotNative) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Query().Has("versioning"):
		_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
	case r.URL.Query().Has("versions"):
		_, _ = fmt.Fprintf(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>v1</VersionId><IsLatest>true</IsLatest><LastModified>2026-11-01T00:00:00Z</LastModified><ETag>&quot;source&quot;</ETag><Size>%d</Size></Version></ListVersionsResult>`, f.size)
	case r.Method == http.MethodHead:
		if r.URL.Path == "/target/key" && f.copies == 0 {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(f.size))
		w.Header().Set("Content-Type", "text/plain")
		if r.URL.Path == "/target/key" {
			w.Header().Set("ETag", `"target"`)
			w.Header().Set("X-Amz-Version-Id", "target-v1")
			w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, f.receipt)
		} else {
			w.Header().Set("ETag", `"source"`)
			w.Header().Set("X-Amz-Version-Id", "v1")
		}
	case r.Method == http.MethodPut:
		if r.Header.Get("If-None-Match") != "*" || f.copies != 0 {
			t.Error("clone checkpoint retry dispatched another copy")
			w.WriteHeader(412)
			return
		}
		f.copies++
		f.receipt = r.Header.Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
		w.Header().Set("X-Amz-Version-Id", "target-v1")
		_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;target&quot;</ETag></CopyObjectResult>`)
	case r.Method == http.MethodGet:
		version := "v1"
		if r.URL.Path == "/target/key" {
			version = "target-v1"
		}
		if r.URL.Query().Get("versionId") != version {
			t.Error("clone verification read live bytes", r.URL)
		}
		_, _ = io.WriteString(w, "data")
	default:
		t.Error("unexpected native clone request", r.Method, r.URL)
		w.WriteHeader(500)
	}
}

func newCloneSnapshotS3(t *testing.T, endpoint string) objectstorage.Provider {
	t.Helper()
	p, err := objectstorage.NewS3(objectstorage.BackendConfig{ID: "native", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "fixture", Endpoint: endpoint, PathStyle: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}, func(string) string { return "fixture" })
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func snapshotRecoveryOperation(t *testing.T, st snapshotRecoveryStore) (state.Account, state.Project, state.ProjectEnvironmentCloneOperation) {
	t.Helper()
	a, err := st.CreateAccount(t.Context(), uuid.NewString()+"@snapshot.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.CreateProjectEnvironmentCloneOperation(t.Context(), state.ProjectEnvironmentCloneOperation{AccountID: a.ID, ProjectID: p.ID, SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: uuid.NewString(), SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	op, err = st.AdvanceProjectEnvironmentCloneOperation(t.Context(), a.ID, p.ID, op.ID, state.CloneOperationPending, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return a, p, op
}

func TestProjectEnvironmentSnapshotNativeCopySurvivesCheckpointFailure(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint("postgres=", pg), func(t *testing.T) {
			var st snapshotRecoveryStore = state.NewMemStore()
			restart := func() snapshotRecoveryStore { return st }
			if pg {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				st = state.NewPgStore(pool)
				restart = func() snapshotRecoveryStore { return state.NewPgStore(pool) }
			}
			a, project, op := snapshotRecoveryOperation(t, st)
			native := &cloneSnapshotNative{size: 4}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }))
			t.Cleanup(server.Close)
			source, target := uuid.NewString(), uuid.NewString()
			_, err := captureProjectEnvironmentObjectStorageSnapshot(t.Context(), st, newCloneSnapshotS3(t, server.URL), a.ID, project.ID, op.ID, source, target, "source", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.AdvanceProjectEnvironmentCloneOperation(t.Context(), a.ID, project.ID, op.ID, state.CloneOperationCapturing, state.CloneOperationCopying, op.Revision, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			failing := &snapshotCheckpointFailure{backing: st, fail: true}
			if _, err := copyProjectEnvironmentObjectStorageSnapshot(t.Context(), failing, newCloneSnapshotS3(t, server.URL), a.ID, project.ID, op.ID, source, target, "source", "target"); err == nil {
				t.Fatal("failed checkpoint was accepted")
			}
			st = restart()
			for range 2 {
				copied, err := copyProjectEnvironmentObjectStorageSnapshot(t.Context(), st, newCloneSnapshotS3(t, server.URL), a.ID, project.ID, op.ID, source, target, "source", "target")
				if err != nil || copied != 1 {
					t.Fatal("checkpoint recovery", copied, err)
				}
			}
			native.mu.Lock()
			copies := native.copies
			native.mu.Unlock()
			if copies != 1 {
				t.Fatal("lost checkpoint retained another destination version", copies)
			}
		})
	}
}

func TestProjectEnvironmentSnapshotCaptureRefusesOversizedCopyPlan(t *testing.T) {
	st := state.NewMemStore()
	a, project, op := snapshotRecoveryOperation(t, st)
	native := &cloneSnapshotNative{size: api.MaxObjectSinglePutBytes + 1}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }))
	t.Cleanup(server.Close)
	source, target := uuid.NewString(), uuid.NewString()
	_, err := captureProjectEnvironmentObjectStorageSnapshot(t.Context(), st, newCloneSnapshotS3(t, server.URL), a.ID, project.ID, op.ID, source, target, "source", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
	var size *objectstorage.SnapshotCopySizeError
	if !errors.As(err, &size) || size.SizeBytes != native.size || size.LimitBytes != api.MaxObjectSinglePutBytes || cloneCoordinatorErrorCode(err) != state.CloneFailureObjectCopySize {
		t.Fatal("oversized plan was not refused clearly during capture", err)
	}
	if _, err := st.ProjectEnvironmentCloneObjectManifest(t.Context(), a.ID, project.ID, op.ID, source); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unsupported copy manifest was committed", err)
	}
	code := directCloneFailure(op, size, time.Now())
	op.ErrorCode = code
	if code != state.CloneFailureObjectCopySize || !state.ProjectEnvironmentCloneFailedDirectly(op) || native.copies != 0 {
		t.Fatal("unsupported clone would retry or strand its target", code, native.copies)
	}
}

func TestProjectEnvironmentSnapshotRequalifiesOldPlanBeforeAnyCopy(t *testing.T) {
	st := state.NewMemStore()
	a, project, op := snapshotRecoveryOperation(t, st)
	source, target := uuid.NewString(), uuid.NewString()
	point := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	items := []state.ProjectEnvironmentCloneObjectVersion{
		{Key: "a-small", VersionID: "v1", Size: 4, LastModified: point.Add(-time.Hour)},
		{Key: "z-large", VersionID: "v1", Size: api.MaxObjectSinglePutBytes + 1, LastModified: point.Add(-time.Hour)},
	}
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash(items)
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.ProjectEnvironmentCloneObjectManifest{OperationID: op.ID, SourceBucketID: source, TargetBucketID: target, CapturedAt: point, Hash: hash, Objects: []state.ProjectEnvironmentCloneObjectCheckpoint{{Source: items[0]}, {Source: items[1]}}}
	if _, err = st.PutProjectEnvironmentCloneObjectManifest(t.Context(), a.ID, project.ID, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdvanceProjectEnvironmentCloneOperation(t.Context(), a.ID, project.ID, op.ID, state.CloneOperationCapturing, state.CloneOperationCopying, op.Revision, nil, ""); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(native.Close)
	copied, err := copyProjectEnvironmentObjectStorageSnapshot(t.Context(), st, newCloneSnapshotS3(t, native.URL), a.ID, project.ID, op.ID, source, target, "source", "target")
	var size *objectstorage.SnapshotCopySizeError
	if !errors.As(err, &size) || size.Key != "z-large" || copied != 0 || requests.Load() != 0 {
		t.Fatal("old unsupported plan partially dispatched", copied, err, requests.Load())
	}
	// Completed copies require no native mutation. A rollout must not fail a
	// checkpoint whose earlier worker already verified all of its bytes.
	for _, item := range items {
		if err := st.MarkProjectEnvironmentCloneObjectCopied(t.Context(), a.ID, project.ID, op.ID, source, item.Key, item.VersionID, `"verified"`, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}
	copied, err = copyProjectEnvironmentObjectStorageSnapshot(t.Context(), st, newCloneSnapshotS3(t, native.URL), a.ID, project.ID, op.ID, source, target, "source", "target")
	if err != nil || copied != 2 || requests.Load() != 0 {
		t.Fatal("completed legacy copy was refused", copied, err, requests.Load())
	}
}

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

var errOperationArtifactMismatch = errors.New("operation artifact changed")

// Bound temporary verification files across all concurrent requests on this
// API node. Per-account slots prevent one customer occupying the whole spool.
type operationArtifactBudget struct {
	mu        sync.Mutex
	bytes     int64
	accounts  map[string]int
	transfers int
}

func (b *operationArtifactBudget) reserve(account string, size int64) bool {
	return b.reserveLimit(account, size) == nil
}

func (b *operationArtifactBudget) reserveLimit(account string, size int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if size < 0 {
		return state.ErrInvalidArgument
	}
	if size >= api.OperationArtifactSpoolMaxBytes || b.bytes+size+1 > api.OperationArtifactSpoolMaxBytes {
		return state.NewOperationLimitError("artifact_spool_bytes", api.OperationArtifactSpoolMaxBytes, b.bytes+size+1)
	}
	if b.accounts[account] >= api.OperationArtifactTransfersPerAccount {
		return state.NewOperationLimitError("artifact_transfers_per_account", api.OperationArtifactTransfersPerAccount, int64(b.accounts[account])+1)
	}
	if b.transfers >= api.OperationArtifactTransfersPerNode {
		return state.NewOperationLimitError("artifact_transfers_per_node", api.OperationArtifactTransfersPerNode, int64(b.transfers)+1)
	}
	if b.accounts == nil {
		b.accounts = map[string]int{}
	}
	b.bytes += size + 1
	b.accounts[account]++
	b.transfers++
	return nil
}
func (b *operationArtifactBudget) release(account string, size int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bytes -= size + 1
	b.accounts[account]--
	b.transfers--
	if b.accounts[account] == 0 {
		delete(b.accounts, account)
	}
}

type verifiedOperationArtifact struct {
	*os.File
	once    sync.Once
	release func()
}

func (f *verifiedOperationArtifact) Close() error {
	var err error
	f.once.Do(func() { err = f.File.Close(); _ = os.Remove(f.Name()); f.release() })
	return err
}

func (s *server) operationArtifactObject(ctx context.Context, op state.Operation, uri string) (state.ObjectBucket, objectstorage.ObjectReader, string, error) {
	app, bucketID, key, err := operations.ParseArtifactURI(uri)
	if err != nil {
		return state.ObjectBucket{}, nil, "", state.ErrInvalidArgument
	}
	if app != op.AppID {
		return state.ObjectBucket{}, nil, "", state.ErrNotFound
	}
	if !s.objectStorageEnabled() {
		return state.ObjectBucket{}, nil, "", objectstorage.ErrUnavailable
	}
	store, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		return state.ObjectBucket{}, nil, "", objectstorage.ErrUnavailable
	}
	bucket, err := store.GetObjectBucket(ctx, op.AccountID, op.AppID, bucketID)
	if err != nil {
		return bucket, nil, "", err
	}
	if bucket.State != "ready" || bucket.PublicRead || bucket.Scope != op.Scope {
		return bucket, nil, "", state.ErrConflict
	}
	backend, err := s.objectStorage.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		return bucket, nil, "", err
	}
	reader, ok := backend.Provider.(objectstorage.ObjectReader)
	if !ok {
		return bucket, nil, "", objectstorage.ErrUnsupported
	}
	return bucket, reader, key, nil
}

func (s *server) verifyOperationArtifact(ctx context.Context, op state.Operation, req api.OperationArtifactRequest) (*verifiedOperationArtifact, error) {
	return s.spoolOperationArtifact(op, req, func() (io.ReadCloser, error) {
		bucket, reader, key, err := s.operationArtifactObject(ctx, op, req.URI)
		if err != nil {
			return nil, err
		}
		if err := s.admitObjectURL(ctx, bucket, objectstorage.SignRequest{Method: http.MethodGet, Key: key}); err != nil {
			return nil, err
		}
		return reader.ReadObject(ctx, bucket.PhysicalName, key)
	})
}

func (s *server) spoolOperationArtifact(op state.Operation, req api.OperationArtifactRequest, open func() (io.ReadCloser, error)) (*verifiedOperationArtifact, error) {
	if err := operations.ValidateArtifact(req, op.PlanLimits); err != nil {
		return nil, state.ErrInvalidArgument
	}
	if err := s.operationArtifactBudget.reserveLimit(op.AccountID, req.SizeBytes); err != nil {
		return nil, err
	}
	reserved := true
	defer func() {
		if reserved {
			s.operationArtifactBudget.release(op.AccountID, req.SizeBytes)
		}
	}()
	stream, err := open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	f, err := os.CreateTemp("", "gregale-operation-artifact-*")
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(stream, req.SizeBytes+1))
	if err != nil {
		return nil, err
	}
	if size != req.SizeBytes || fmt.Sprintf("sha256:%x", hash.Sum(nil)) != req.SHA256 {
		return nil, errOperationArtifactMismatch
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if err := stream.Close(); err != nil {
		return nil, err
	}
	valid = true
	reserved = false
	return &verifiedOperationArtifact{File: f, release: func() { s.operationArtifactBudget.release(op.AccountID, req.SizeBytes) }}, nil
}

func writeOperationArtifactError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "operation_artifact_unavailable", "Artifact unavailable", "the retained result bytes are unavailable"))
		return
	}
	if errors.Is(err, errOperationArtifactMismatch) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_artifact_changed", "Artifact unavailable", "the object bytes differ from the retained result reference"))
		return
	}
	if errors.Is(err, state.ErrOperationQuota) || errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrOperationStaleAttempt) || errors.Is(err, state.ErrOperationInputConflict) {
		writeOperationError(w, err)
		return
	}
	bucketProblem(w, err)
}

func (s *server) runtimeOperation(w http.ResponseWriter, r *http.Request) (state.Operation, state.OperationExecutionAuthority, bool) {
	authority, err := s.runtimeOperationAuthority(r)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "provide a current operation workload assertion"))
		return state.Operation{}, authority, false
	}
	_, ok := s.operationStore(w)
	if !ok {
		return state.Operation{}, authority, false
	}
	store, _ := s.store.(state.OperationStore)
	op, err := store.OperationByID(r.Context(), authority.AccountID, "", r.PathValue("id"))
	if err == nil {
		var inv state.Invocation
		inv, err = s.store.InvocationByID(r.Context(), authority.InvocationID)
		if err == nil {
			err = state.ValidateOperationExecutionAuthority(op, inv, authority, time.Now())
		}
	}
	if err != nil {
		writeOperationError(w, err)
		return op, authority, false
	}
	return op, authority, true
}

func (s *server) attachOperationArtifact(w http.ResponseWriter, r *http.Request) {
	op, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationArtifactStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation artifact storage unavailable"))
		return
	}
	var req api.OperationArtifactRequest
	if !decodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.OperationArtifactTransferTimeout)
	defer cancel()
	blobID, err := s.retainOperationArtifact(ctx, op, authority, req)
	if err != nil {
		writeOperationArtifactError(w, err)
		return
	}
	got, err := store.AttachVerifiedOperationArtifact(ctx, op.ID, authority, req, blobID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got.OperationResponse)
}

func (s *server) downloadPlatformTenantOperationArtifact(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.platformTenantSelfOperation(w, r, acct)
	if ok {
		s.downloadOperationArtifact(w, r, op)
	}
}
func (s *server) downloadOwnedOperationArtifact(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if ok {
		s.downloadOperationArtifact(w, r, op)
	}
}
func (s *server) downloadOperationArtifact(w http.ResponseWriter, r *http.Request, op state.Operation) {
	if op.State != api.OperationSucceeded {
		writeOperationError(w, state.ErrConflict)
		return
	}
	var artifact *api.OperationResultArtifact
	for i := range op.Artifacts {
		if op.Artifacts[i].ID == r.PathValue("artifact") {
			artifact = &op.Artifacts[i]
			break
		}
	}
	if artifact == nil {
		writeOperationError(w, state.ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.OperationArtifactTransferTimeout)
	defer cancel()
	req := api.OperationArtifactRequest{ReportID: "download", Name: artifact.Name, URI: artifact.URI, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256}
	f, err := s.readRetainedOperationArtifact(ctx, op, *artifact, req)
	if err != nil {
		writeOperationArtifactError(w, err)
		return
	}
	defer func() { _ = f.Close() }()
	store, ok := s.store.(state.OperationStore)
	if !ok {
		writeOperationError(w, state.ErrNotFound)
		return
	}
	current, err := store.OperationByID(ctx, op.AccountID, op.PlatformTenantID, op.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if current.Generation != op.Generation || current.State != api.OperationSucceeded {
		writeOperationError(w, state.ErrConflict)
		return
	}
	// Serve verified retained bytes. A storage failure remains separate from
	// the business outcome and never invites automatic execution replay.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Name}))
	w.Header().Set("X-Gregale-Artifact-Sha256", artifact.SHA256)
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(api.OperationArtifactTransferTimeout))
	_, _ = io.Copy(w, f)
}

// A prior receipt proves that this declaration was verified before commit.
// The store still atomically checks the claim and declaration fingerprint.
// Every download separately verifies the retained copy's availability and bytes.
func operationHasArtifactReceipt(op state.Operation, authority state.OperationExecutionAuthority, reportID string) bool {
	id := operations.ArtifactIdentity(op.ID, authority.InvocationID, authority.Attempt, reportID)
	for _, artifact := range op.Artifacts {
		if artifact.ID == id {
			return true
		}
	}
	return false
}

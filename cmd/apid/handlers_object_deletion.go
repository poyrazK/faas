package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) deletionService(b state.ObjectBucket, p objectstorage.Provider) objectstorage.DeletionService {
	st, _ := s.store.(state.ObjectDeletionStore)
	metrics, _ := s.store.(state.ObjectStorageProviderUsageStore)
	return objectstorage.DeletionService{Store: st, Provider: p, BeforeRequest: objectstorage.VersioningRequestRecorder(metrics, b.ID)}
}
func controlDeletionID(r *http.Request) (string, error) {
	values := r.Header.Values("X-Gregale-Delete-Id")
	if len(values) == 0 {
		return uuid.NewString(), nil
	}
	if len(values) != 1 {
		return "", objectstorage.ErrInvalid
	}
	id, e := uuid.Parse(values[0])
	if e != nil || id.String() != values[0] {
		return "", objectstorage.ErrInvalid
	}
	return id.String(), nil
}
func (s *server) createObjectDeletion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	var input api.ObjectDeletionRequest
	if !decodeObjectDeletion(w, r, &input) {
		return
	}
	if input.VersionID != "" && !state.ValidObjectVersionID(input.VersionID) {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, e := s.deletionService(b, p).Start(r.Context(), b, input.Key, input.VersionID, input.ID, s.objectStorage.Accounting)
	if e != nil && j.State != "dispatched" {
		bucketProblem(w, e)
		return
	}
	status := http.StatusAccepted
	if j.State == "completed" || j.State == "failed" {
		status = http.StatusOK
	}
	writeJSON(w, status, j.ObjectDeletion)
}
func (s *server) getObjectDeletion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	st, ok := s.store.(state.ObjectDeletionStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return
	}
	j, e := st.GetObjectDeletion(r.Context(), b.AccountID, b.ID, r.PathValue("deletion"))
	if e != nil {
		bucketProblem(w, e)
		return
	}
	writeJSON(w, http.StatusOK, j.ObjectDeletion)
}
func (s *server) deleteMutableBucketObject(ctx context.Context, b state.ObjectBucket, p objectstorage.Provider, key, selector, id string) (state.ObjectDeletion, error) {
	// The deletion journal owns admission and recovery drainage. A second
	// generic request receipt cannot be settled by original deletion recovery.
	j, err := s.deletionService(b, p).Start(ctx, b, key, selector, id, s.objectStorage.Accounting)
	if err == nil && j.State != "completed" {
		err = objectstorage.ErrUnavailable
	}
	return j, err
}

func decodeObjectDeletion(w http.ResponseWriter, r *http.Request, out *api.ObjectDeletionRequest) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectDeletionBodyBytes))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(out) != nil || d.Decode(&extra) != io.EOF {
		bucketProblem(w, objectstorage.ErrInvalid)
		return false
	}
	return true
}

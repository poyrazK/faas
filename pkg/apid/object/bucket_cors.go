package object

// ADR-20261025202805: customer CORS rules for a bucket. apid stores them;
// s3-gatewayd applies them to browser requests that carry a presigned URL or
// an upload grant for the bucket.

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) bucketCORSStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectBucketCORSStore, bool) {
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok {
		return b, nil, false
	}
	if !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, nil, false
	}
	st, ok := s.Host.Store().(state.ObjectBucketCORSStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return b, nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return b, st, true
}

func (s *Service) GetObjectBucketCORS(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.bucketCORSStore(w, r, acct)
	if !ok {
		return
	}
	c, err := st.GetObjectBucketCORS(r.Context(), b.AccountID, b.AppID, b.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, c)
}

func (s *Service) PutObjectBucketCORS(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.bucketCORSStore(w, r, acct)
	if !ok {
		return
	}
	var in api.ObjectBucketCORSRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectBucketCORSBodyBytes))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&in) != nil || d.Decode(&extra) != io.EOF || !api.ValidObjectBucketCORSRules(in.Rules) {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	c, err := st.PutObjectBucketCORS(r.Context(), b.AccountID, b.AppID, b.ID, in.Rules)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.cors_updated", &acct.ID, map[string]any{"bucket_id": b.ID, "rules": len(c.Rules), "revision": c.Revision})
	httpkit.WriteJSON(w, http.StatusOK, c)
}

func (s *Service) DeleteObjectBucketCORS(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.bucketCORSStore(w, r, acct)
	if !ok {
		return
	}
	if err := st.DeleteObjectBucketCORS(r.Context(), b.AccountID, b.AppID, b.ID); err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.cors_deleted", &acct.ID, map[string]any{"bucket_id": b.ID})
	w.WriteHeader(http.StatusNoContent)
}

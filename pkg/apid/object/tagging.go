package object

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ObjectBucketTags(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodDelete && !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	permission := state.ObjectBucketPermissionWrite
	if r.Method == http.MethodGet {
		permission = state.ObjectBucketPermissionRead
	}
	if !ok || !s.AuthorizeBucketData(w, r, b, permission) {
		return
	}
	q, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil || len(q["key"]) != 1 || len(q) != 1 && len(q) != 2 || len(q) == 2 && (len(q["version_id"]) != 1 || !state.ValidObjectVersionID(q.Get("version_id"))) {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if err := objectstorage.ValidateObjectTaggingRequest(r); err != nil {
		BucketProblem(w, err)
		return
	}
	tags, ok := decodeControlObjectTags(w, r)
	if !ok {
		return
	}
	out, err := objectstorageactivity.Tag(r.Context(), s.Host.Store(), b, s.objectTaggingService(b, p, q.Get("key")), r.Method, q.Get("key"), q.Get("version_id"), tags)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) objectTaggingService(b state.ObjectBucket, p objectstorage.Provider, key string) objectstorage.TaggingService {
	refs, _ := s.Host.Store().(state.ObjectVersionReferenceStore)
	return objectstorage.TaggingService{References: refs, Provider: p,
		BeforeOperation: func(ctx context.Context) error { return s.admitObjectMultipartPartURL(ctx, b, key) },
		BeforeRequest:   s.CustomerObjectRequestRecorder(b),
	}
}

func decodeControlObjectTags(w http.ResponseWriter, r *http.Request) (map[string]string, bool) {
	if r.Method != http.MethodPut {
		return nil, true
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectTaggingBodyBytes))
	d.DisallowUnknownFields()
	var input api.ObjectTaggingRequest
	var extra any
	if d.Decode(&input) != nil || input.Tags == nil || d.Decode(&extra) != io.EOF {
		BucketProblem(w, objectstorage.ErrInvalid)
		return nil, false
	}
	return input.Tags, true
}

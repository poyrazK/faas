package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorageactivity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) objectBucketTags(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodDelete && !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	permission := state.ObjectBucketPermissionWrite
	if r.Method == http.MethodGet {
		permission = state.ObjectBucketPermissionRead
	}
	if !ok || !s.authorizeBucketData(w, r, b, permission) {
		return
	}
	q, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil || len(q["key"]) != 1 || len(q) != 1 && len(q) != 2 || len(q) == 2 && (len(q["version_id"]) != 1 || !state.ValidObjectVersionID(q.Get("version_id"))) {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if err := objectstorage.ValidateObjectTaggingRequest(r); err != nil {
		bucketProblem(w, err)
		return
	}
	tags, ok := decodeControlObjectTags(w, r)
	if !ok {
		return
	}
	call := func(callCtx context.Context) (api.ObjectTaggingResult, error) {
		return s.objectTaggingService(b, p, q.Get("key")).Do(callCtx, b, r.Method, q.Get("key"), q.Get("version_id"), tags)
	}
	var out api.ObjectTaggingResult
	var err error
	if r.Method == http.MethodGet {
		out, err = call(r.Context())
	} else {
		out, err = objectstorageactivity.Execute(r.Context(), s.store, b, call)
	}
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) objectTaggingService(b state.ObjectBucket, p objectstorage.Provider, key string) objectstorage.TaggingService {
	refs, _ := s.store.(state.ObjectVersionReferenceStore)
	return objectstorage.TaggingService{References: refs, Provider: p, BeforeRequest: func(ctx context.Context) error {
		if err := s.admitObjectMultipartPartURL(ctx, b, key); err != nil {
			return err
		}
		return s.customerObjectRequestRecorder(b)(ctx)
	}}
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
		bucketProblem(w, objectstorage.ErrInvalid)
		return nil, false
	}
	return input.Tags, true
}

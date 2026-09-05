package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) WithObjectStorage(registry *objectstorage.Registry) *server {
	s.objectStorage = registry
	return s
}

func (s *server) objectStorageEnabled() bool {
	return s.objectStorage != nil && s.runtimeBool(runtimeConfigS3, false)
}

// bucketView deliberately excludes operator placement, credentials and leases.
type bucketView = api.ObjectBucket

func viewBucket(b state.ObjectBucket) bucketView {
	return bucketView{ID: b.ID, Name: b.Name, Scope: b.Scope, Region: b.Region, State: b.State, CreatedAt: b.CreatedAt}
}

func bucketProblem(w http.ResponseWriter, err error) {
	status, code, detail := 503, "object_storage_unavailable", "Object storage is unavailable; retry later."
	switch {
	case errors.Is(err, state.ErrObjectUsageStale):
		status, code, detail = 503, "object_storage_usage_stale", "Storage accounting is not configured or usage data is stale; new URLs are blocked."
	case errors.Is(err, state.ErrObjectBudget):
		status, code, detail = 402, "object_storage_budget_reached", "The object storage safety budget has been reached; new URLs are blocked."
	case errors.Is(err, state.ErrObjectCapacity):
		status, code, detail = 409, "object_storage_capacity_reserved", "The object storage capacity limit would be exceeded by this upload reservation."
	case errors.Is(err, state.ErrNotFound), errors.Is(err, objectstorage.ErrNotFound):
		status, code, detail = 404, "object_storage_not_found", "Bucket or object not found."
	case errors.Is(err, objectstorage.ErrInvalid):
		status, code, detail = 400, "object_storage_invalid", "Invalid object storage request."
	case errors.Is(err, objectstorage.ErrNotEmpty):
		status, code, detail = 409, "bucket_not_empty", "Empty the bucket, including any versions, before deleting it."
	case errors.Is(err, state.ErrConflict), errors.Is(err, objectstorage.ErrConflict):
		status, code, detail = 409, "object_storage_conflict", "Bucket is busy, conflicts with this request, or the bucket limit was reached."
	}
	problem := api.NewProblem(status, code, http.StatusText(status), detail)
	var limit *state.ObjectStorageLimitError
	if errors.As(err, &limit) {
		problem.Limit, problem.Observed = &limit.Limit, &limit.Observed
		problem.Detail = detail + " Limit: " + limit.Kind + "."
		problem.DocsURL = "https://github.com/poyrazK/faas/blob/main/docs/object-storage.md#accounting-and-safety-budgets"
	}
	api.WriteProblem(w, problem)
}

func decodeBucketRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(out) != nil || d.Decode(&extra) != io.EOF {
		bucketProblem(w, objectstorage.ErrInvalid)
		return false
	}
	return true
}

func (s *server) bucketStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ObjectBucketStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	st, ok := s.store.(state.ObjectBucketStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
	}
	return app, st, ok
}

func (s *server) listBuckets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, st, ok := s.bucketStore(w, r, acct)
	if !ok {
		return
	}
	var buckets []state.ObjectBucket
	var err error
	p, principalOK := principalFrom(r)
	access, hasAccessStore := s.store.(state.ObjectBucketAccessStore)
	if principalOK && p.Key != nil && !apiKeyCarriesScope(*p.Key, api.ScopeAdmin) && !apiKeyCarriesScope(*p.Key, api.ScopeStorageManage) {
		if !hasAccessStore {
			bucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		buckets, err = access.ListObjectBucketsForKey(r.Context(), acct.ID, app.ID, p.Key.ID)
	} else {
		buckets, err = st.ListObjectBuckets(r.Context(), acct.ID, app.ID)
	}
	if err != nil {
		bucketProblem(w, err)
		return
	}
	items := make([]bucketView, 0, len(buckets))
	for _, b := range buckets {
		items = append(items, viewBucket(b))
	}
	regions, defaultRegion, maxBytes, maxBuckets := []string{}, "", int64(0), 0
	if s.objectStorage != nil {
		regions, defaultRegion = s.objectStorage.Regions(), s.objectStorage.DefaultRegion
		maxBytes, maxBuckets = s.objectStorage.MaxUploadBytes, s.objectStorage.MaxBucketsPerApp
	}
	writeJSON(w, 200, api.ObjectBucketList{Items: items, Enabled: s.objectStorageEnabled(), Regions: regions, DefaultRegion: defaultRegion, MaxUploadBytes: maxBytes, MaxBucketsPerApp: maxBuckets})
}

func apiKeyCarriesScope(key state.APIKey, want string) bool {
	for _, scope := range key.Scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func (s *server) loadBucketRecord(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectBucketStore, bool) {
	app, st, ok := s.bucketStore(w, r, acct)
	if !ok {
		return state.ObjectBucket{}, nil, false
	}
	id := r.PathValue("bucket")
	if _, err := uuid.Parse(id); err != nil {
		bucketProblem(w, state.ErrNotFound)
		return state.ObjectBucket{}, nil, false
	}
	bucket, err := st.GetObjectBucket(r.Context(), acct.ID, app.ID, id)
	if err != nil {
		bucketProblem(w, err)
		return bucket, st, false
	}
	return bucket, st, true
}

type createBucketRequest struct {
	Name   string `json:"name"`
	Scope  string `json:"scope"`
	Region string `json:"region"`
}

func (s *server) createBucket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, st, ok := s.bucketStore(w, r, acct)
	if !ok {
		return
	}
	var req createBucketRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	if req.Scope == "" {
		req.Scope = api.DefaultEnvScope
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(req.Name) || api.ValidateScope(req.Scope) != nil {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	b, err := s.reserveBucket(r.Context(), st, acct.ID, app.ID, req)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if b.State == "ready" {
		writeJSON(w, 200, viewBucket(b))
		return
	}
	if b.State != "provisioning" {
		bucketProblem(w, state.ErrConflict)
		return
	}
	b, err = s.provisionBucket(r.Context(), st, b)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, 201, viewBucket(b))
}

func (s *server) reserveBucket(ctx context.Context, st state.ObjectBucketStore, account, app string, req createBucketRequest) (state.ObjectBucket, error) {
	// Resolve retries before consulting today's default, including defaults
	// removed from the creation catalog while old placements remain online.
	items, err := st.ListObjectBuckets(ctx, account, app)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	for _, old := range items {
		if old.Name == req.Name && old.Scope == req.Scope {
			if req.Region != "" && old.Region != req.Region {
				return old, state.ErrConflict
			}
			return old, nil
		}
	}
	if req.Region == "" {
		req.Region = s.objectStorage.DefaultRegion
	}
	backend, err := s.objectStorage.Default(req.Region)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	id := uuid.NewString()
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: account, AppID: app, Name: req.Name, Scope: req.Scope, Region: req.Region, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", "")}, s.objectStorage.MaxBucketsPerApp)
	if err == nil && b.Region != req.Region {
		return b, state.ErrConflict
	}
	return b, err
}

func (s *server) provisionBucket(ctx context.Context, st state.ObjectBucketStore, b state.ObjectBucket) (state.ObjectBucket, error) {
	if !s.objectStorageEnabled() {
		return b, objectstorage.ErrUnavailable
	}
	token := uuid.NewString()
	b, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, token, "provisioning")
	if err != nil {
		return b, err
	}
	err = s.executeBucketOperation(ctx, st, b)
	if err == nil {
		b.State = "ready"
	}
	return b, err
}

func finishBucket(ctx context.Context, st state.ObjectBucketStore, id, token, next string) error {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return st.FinishObjectBucket(finishCtx, id, token, next)
}

func (s *server) loadBucket(w http.ResponseWriter, r *http.Request, acct state.Account, ready bool) (state.ObjectBucket, state.ObjectBucketStore, objectstorage.Provider, bool) {
	b, st, ok := s.loadBucketRecord(w, r, acct)
	if !ok {
		return state.ObjectBucket{}, nil, nil, false
	}
	var err error
	if ready && b.State != "ready" {
		err = state.ErrConflict
	}
	if err != nil {
		bucketProblem(w, err)
		return b, st, nil, false
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		bucketProblem(w, err)
		return b, st, nil, false
	}
	return b, st, backend.Provider, true
}

func (s *server) authorizeBucketData(w http.ResponseWriter, r *http.Request, bucket state.ObjectBucket, permission string) bool {
	p, ok := principalFrom(r)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return false
	}
	if p.Key == nil || apiKeyCarriesScope(*p.Key, api.ScopeAdmin) {
		return true
	}
	access, ok := s.store.(state.ObjectBucketAccessStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return false
	}
	allowed, err := access.ObjectBucketKeyCan(r.Context(), bucket.AccountID, bucket.ID, p.Key.ID, permission)
	if err != nil {
		bucketProblem(w, err)
		return false
	}
	if !allowed {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "object_storage_access_denied", "Forbidden", "The API key has no matching grant for this bucket."))
		return false
	}
	return true
}

func (s *server) deleteBucket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, _, ok := s.loadBucket(w, r, acct, false)
	if !ok {
		return
	}
	token := uuid.NewString()
	b, err := st.ClaimObjectBucket(r.Context(), b.AccountID, b.AppID, b.ID, token, "deleting")
	if err != nil {
		bucketProblem(w, err)
		return
	}
	err = s.executeBucketOperation(r.Context(), st, b)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listBucketObjects(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, _, provider, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return
	}
	if !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionRead) {
		return
	}
	prefix, cursor := r.URL.Query().Get("prefix"), r.URL.Query().Get("cursor")
	limit := int64(100)
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
	}
	if err != nil || limit < 1 || limit > 1000 || len(prefix) > 1024 || !utf8.ValidString(prefix) || len(cursor) > 8192 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	page, err := provider.ListObjects(r.Context(), b.PhysicalName, prefix, cursor, int32(limit))
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, 200, page)
}

func (s *server) deleteBucketObject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, _, provider, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return
	}
	if !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	key := r.URL.Query().Get("key")
	if err := (objectstorage.SignRequest{Method: "GET", Key: key}).Validate(1); err != nil {
		bucketProblem(w, err)
		return
	}
	if err := provider.DeleteObject(r.Context(), b.PhysicalName, key); err != nil {
		bucketProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) signBucketObject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	var req objectstorage.SignRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	// POST is a read capability for GET URLs, but PUT requires write scope.
	handler := func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !s.objectStorageEnabled() {
			bucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		b, _, provider, ok := s.loadBucket(w, r, acct, true)
		if !ok {
			return
		}
		if err := req.Validate(s.objectStorage.MaxUploadBytes); err != nil {
			bucketProblem(w, err)
			return
		}
		permission := state.ObjectBucketPermissionRead
		if req.Method == "PUT" {
			permission = state.ObjectBucketPermissionWrite
		}
		if !s.authorizeBucketData(w, r, b, permission) {
			return
		}
		// Commit capacity and authorization accounting before exposing a URL.
		// Signer/network failures intentionally do not refund the reservation:
		// a lost response is not proof that no usable capability was issued.
		if err := s.admitObjectURL(r.Context(), b, req); err != nil {
			bucketProblem(w, err)
			return
		}
		out, err := provider.Presign(r.Context(), b.PhysicalName, req)
		if err != nil {
			bucketProblem(w, err)
			return
		}
		writeJSON(w, 200, out)
	}
	if req.Method == "PUT" {
		s.requireScope(api.ScopesStorageWriteSurface...)(handler)(w, r, acct)
		return
	}
	s.requireScope(api.ScopesStorageReadSurface...)(handler)(w, r, acct)
}

func viewBucketAccessGrant(grant state.ObjectBucketAccessGrant) api.ObjectBucketAccessGrant {
	return api.ObjectBucketAccessGrant{
		KeyID: grant.APIKeyID, KeyLabel: grant.KeyLabel, KeyStatus: grant.KeyStatus,
		Permission: grant.Permission, CreatedAt: grant.CreatedAt, UpdatedAt: grant.UpdatedAt,
	}
}

func (s *server) bucketAccessStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectBucketAccessStore, bool) {
	bucket, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok {
		return state.ObjectBucket{}, nil, false
	}
	access, ok := s.store.(state.ObjectBucketAccessStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return state.ObjectBucket{}, nil, false
	}
	return bucket, access, true
}

func (s *server) listBucketAccessGrants(w http.ResponseWriter, r *http.Request, acct state.Account) {
	bucket, access, ok := s.bucketAccessStore(w, r, acct)
	if !ok {
		return
	}
	grants, err := access.ListObjectBucketAccessGrants(r.Context(), acct.ID, bucket.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	items := make([]api.ObjectBucketAccessGrant, 0, len(grants))
	for _, grant := range grants {
		items = append(items, viewBucketAccessGrant(grant))
	}
	writeJSON(w, http.StatusOK, api.ObjectBucketAccessGrantList{Items: items})
}

func (s *server) setBucketAccessGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	bucket, access, ok := s.bucketAccessStore(w, r, acct)
	if !ok {
		return
	}
	keyID := r.PathValue("key")
	if _, err := uuid.Parse(keyID); err != nil {
		bucketProblem(w, state.ErrNotFound)
		return
	}
	var req api.SetObjectBucketAccessGrantRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	key, err := s.store.GetAPIKey(r.Context(), acct.ID, keyID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if key.Status != string(state.APIKeyStatusActive) && key.Status != string(state.APIKeyStatusGrace) {
		bucketProblem(w, state.ErrConflict)
		return
	}
	valid := !apiKeyCarriesScope(key, api.ScopeAdmin)
	switch req.Permission {
	case api.ObjectBucketPermissionRead:
		valid = valid && apiKeyCarriesScope(key, api.ScopeStorageRead)
	case api.ObjectBucketPermissionWrite:
		valid = valid && apiKeyCarriesScope(key, api.ScopeStorageWrite)
	case api.ObjectBucketPermissionReadWrite:
		valid = valid && apiKeyCarriesScope(key, api.ScopeStorageRead) && apiKeyCarriesScope(key, api.ScopeStorageWrite)
	default:
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if !valid {
		bucketProblem(w, state.ErrConflict)
		return
	}
	grant, err := access.SetObjectBucketAccessGrant(r.Context(), acct.ID, bucket.ID, keyID, req.Permission)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.access_grant_set", &acct.ID, map[string]any{
		"app_id": bucket.AppID, "bucket_id": bucket.ID, "key_id": keyID, "permission": req.Permission,
	})
	writeJSON(w, http.StatusOK, viewBucketAccessGrant(grant))
}

func (s *server) deleteBucketAccessGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	bucket, access, ok := s.bucketAccessStore(w, r, acct)
	if !ok {
		return
	}
	keyID := r.PathValue("key")
	if _, err := uuid.Parse(keyID); err != nil {
		bucketProblem(w, state.ErrNotFound)
		return
	}
	if err := access.DeleteObjectBucketAccessGrant(r.Context(), acct.ID, bucket.ID, keyID); err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.access_grant_deleted", &acct.ID, map[string]any{
		"app_id": bucket.AppID, "bucket_id": bucket.ID, "key_id": keyID,
	})
	w.WriteHeader(http.StatusNoContent)
}

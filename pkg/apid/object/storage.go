package object

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func viewObjectUploadRoute(route state.ObjectUploadRoute) api.ObjectUploadRoute {
	out := api.ObjectUploadRoute{ID: route.ID, Name: route.Name, BucketID: route.BucketID, KeyPrefix: route.KeyPrefix, MaxBytes: route.MaxBytes, AllowedContentTypes: append([]string(nil), route.AllowedContentTypes...), Enabled: route.Enabled, CreatedAt: route.CreatedAt, UpdatedAt: route.UpdatedAt}
	if !route.Encryption.Empty() {
		selection := route.Encryption.Clone().Selection
		out.Encryption = &selection
	}
	return out
}

func (s *Service) uploadRouteStore(w http.ResponseWriter) (state.ObjectUploadRouteStore, bool) {
	store, ok := s.Host.Store().(state.ObjectUploadRouteStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
	}
	return store, ok
}

func (s *Service) ListObjectUploadRoutes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.uploadRouteStore(w)
	if !ok {
		return
	}
	routes, err := store.ListObjectUploadRoutes(r.Context(), acct.ID, app.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	items := make([]api.ObjectUploadRoute, 0, len(routes))
	for _, route := range routes {
		items = append(items, viewObjectUploadRoute(route))
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ObjectUploadRouteList{Items: items})
}

func (s *Service) CreateObjectUploadRoute(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.uploadRouteStore(w)
	if !ok {
		return
	}
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	var req api.CreateObjectUploadRouteRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	buckets, ok := s.Host.Store().(state.ObjectBucketStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	if req.BucketID == "" {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	b, err := buckets.GetObjectBucket(r.Context(), acct.ID, app.ID, req.BucketID)
	if err != nil || b.State != "ready" {
		BucketProblem(w, state.ErrNotFound)
		return
	}
	if !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	route, status, err := s.configureObjectUploadRoute(r.Context(), b, store, req)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, status, viewObjectUploadRoute(route))
}

func (s *Service) DeleteObjectUploadRoute(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.uploadRouteStore(w)
	if !ok {
		return
	}
	if err := store.DeleteObjectUploadRoute(r.Context(), acct.ID, app.ID, r.PathValue("route")); err != nil {
		BucketProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validObjectUploadRouteName(name string) bool {
	return regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(name)
}

func validObjectUploadPrefix(prefix string) bool {
	if len(prefix) > 256 || strings.ContainsAny(prefix, "\\\r\n") {
		return false
	}
	if prefix == "" {
		return true
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return objectstorage.ValidKey(prefix)
}

func validUploadContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.Count(value, "/") != 1 || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	parts := strings.SplitN(value, "/", 2)
	return parts[0] != "" && parts[1] != "" && parts[1] != "*" || strings.HasSuffix(value, "/*")
}

// ObjectStorageEnabled reports whether a backend is configured and the S3
// runtime switch is on. A Service without a Host (a bare apid server in
// tests) has no backend.
func (s *Service) ObjectStorageEnabled() bool {
	return s.Host != nil && s.Host.ObjectStorage() != nil && s.Host.S3RuntimeEnabled()
}

func (s *Service) ObjectStorageProvisioningReady() bool {
	return s.ObjectStorageEnabled() && s.Host.ObjectStorage().CanProvisionAllRegions()
}

// BucketView deliberately excludes operator placement, credentials and leases.
type BucketView = api.ObjectBucket

func viewBucket(b state.ObjectBucket) BucketView {
	return BucketView{ID: b.ID, Name: b.Name, Scope: b.Scope, Region: b.Region, State: b.State, Public: b.PublicRead, ServeAt: b.ServeAt, CreatedAt: b.CreatedAt}
}

// ErrObjectStorageDisabled marks the operator gate: the installation has
// object storage switched off, so retrying cannot help. It still matches
// objectstorage.ErrUnavailable for callers that test the broad class.
var ErrObjectStorageDisabled = fmt.Errorf("%w: not enabled on this installation", objectstorage.ErrUnavailable)

func BucketProblem(w http.ResponseWriter, err error) {
	status, code, detail := 503, "object_storage_unavailable", "Object storage is unavailable; retry later."
	switch {
	case errors.Is(err, ErrObjectStorageDisabled):
		detail = "Object storage is not enabled on this Gregale installation; `gregale capabilities` shows feature availability."
	case errors.Is(err, state.ErrObjectBucketWriteFenced):
		status, code, detail = 503, "object_storage_checkpoint_active", "Bucket writes are temporarily paused for checkpoint capture."
	case errors.Is(err, state.ErrObjectUsageStale):
		status, code, detail = 503, "object_storage_usage_stale", "Storage accounting is not configured or usage data is stale; new provider requests and transfer authorizations are blocked."
	case errors.Is(err, state.ErrObjectBudget):
		status, code, detail = 402, "object_storage_budget_reached", "The object storage safety budget has been reached; new provider requests and transfer authorizations are blocked."
	case errors.Is(err, state.ErrObjectCapacity):
		status, code, detail = 409, "object_storage_capacity_reserved", "The object storage capacity limit would be exceeded by this upload reservation."
	case errors.Is(err, state.ErrNotFound), errors.Is(err, objectstorage.ErrNotFound):
		status, code, detail = 404, "object_storage_not_found", "Bucket or object not found."
	case errors.Is(err, objectstorage.ErrPreconditionFailed):
		status, code, detail = 412, "object_storage_precondition_failed", "The completion condition was not satisfied."
	case errors.Is(err, objectstorage.ErrConditionalConflict):
		status, code, detail = 409, "object_storage_conditional_conflict", "Start a new multipart upload after a conflicting write."
	case errors.Is(err, objectstorage.ErrConditionalNotFound):
		status, code, detail = 404, "object_storage_conditional_not_found", "The conditional destination object no longer exists."
	case errors.Is(err, objectstorage.ErrObjectNotTaggable):
		status, code, detail = 405, "object_storage_not_taggable", "The specified object version does not support tagging."
	case errors.Is(err, objectstorage.ErrInvalid):
		status, code, detail = 400, "object_storage_invalid", "Invalid object storage request."
	case errors.Is(err, objectstorage.ErrUnsupported):
		status, code, detail = 501, "object_storage_unsupported", "This object storage operation is not supported by the selected provider."
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
		BucketProblem(w, objectstorage.ErrInvalid)
		return false
	}
	return true
}

func (s *Service) bucketStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ObjectBucketStore, bool) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	st, ok := s.Host.Store().(state.ObjectBucketStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
	}
	return app, st, ok
}

func (s *Service) ListBuckets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, st, ok := s.bucketStore(w, r, acct)
	if !ok {
		return
	}
	var buckets []state.ObjectBucket
	var err error
	p, principalOK := httpkit.PrincipalFrom(r)
	access, hasAccessStore := s.Host.Store().(state.ObjectBucketAccessStore)
	if principalOK && p.Key != nil && !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin) && !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeStorageManage) {
		if !hasAccessStore {
			BucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		buckets, err = access.ListObjectBucketsForKey(r.Context(), acct.ID, app.ID, p.Key.ID)
	} else {
		buckets, err = st.ListObjectBuckets(r.Context(), acct.ID, app.ID)
	}
	if err != nil {
		BucketProblem(w, err)
		return
	}
	items := make([]BucketView, 0, len(buckets))
	for _, b := range buckets {
		items = append(items, viewBucket(b))
	}
	httpkit.WriteJSON(w, 200, s.objectBucketCatalog(items))
}

func (s *Service) objectBucketCatalog(items []BucketView) api.ObjectBucketList {
	result := api.ObjectBucketList{Items: items, Enabled: s.ObjectStorageProvisioningReady(), Regions: []string{}}
	if s.Host.ObjectStorage() != nil {
		result.Regions, result.DefaultRegion = s.Host.ObjectStorage().Regions(), s.Host.ObjectStorage().DefaultRegion
		result.MaxUploadBytes, result.MaxBucketsPerApp = s.Host.ObjectStorage().MaxUploadBytes, s.Host.ObjectStorage().MaxBucketsPerApp
		result.MaxSinglePutBytes, result.MaxPartBytes = s.Host.ObjectStorage().MaxSinglePutBytes, s.Host.ObjectStorage().MaxPartBytes
		result.TransferTimeoutSeconds = int64(s.Host.ObjectStorage().TransferTimeout() / time.Second)
		result.UploadProfile = s.Host.ObjectStorage().Transfer.Profile
	}
	return result
}

func (s *Service) loadBucketRecord(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectBucketStore, bool) {
	app, st, ok := s.bucketStore(w, r, acct)
	if !ok {
		return state.ObjectBucket{}, nil, false
	}
	id := r.PathValue("bucket")
	if _, err := uuid.Parse(id); err != nil {
		BucketProblem(w, state.ErrNotFound)
		return state.ObjectBucket{}, nil, false
	}
	bucket, err := st.GetObjectBucket(r.Context(), acct.ID, app.ID, id)
	if err != nil {
		BucketProblem(w, err)
		return bucket, st, false
	}
	return bucket, st, true
}

type CreateBucketRequest struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Region  string `json:"region"`
	Public  bool   `json:"public"`
	ServeAt string `json:"serve_at"`
}

func (s *Service) CreateBucket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, st, ok := s.bucketStore(w, r, acct)
	if !ok {
		return
	}
	var req CreateBucketRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	if req.Scope == "" {
		req.Scope = api.DefaultEnvScope
	}
	req.ServeAt = strings.TrimSpace(req.ServeAt)
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(req.Name) || api.ValidateScope(req.Scope) != nil || !objectstorage.ValidPublicReadPath(req.Public, req.ServeAt) {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	if req.Public && !s.Host.ObjectStorage().Accounting.Valid() {
		// Public mounts are an anonymous data plane. Do not create one when
		// the operator has not supplied the same fresh usage policy that gates
		// signed object URLs; otherwise the mount would be unmetered.
		BucketProblem(w, state.ErrObjectUsageStale)
		return
	}
	b, err := s.reserveBucket(r.Context(), st, acct.ID, app.ID, req)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	if b.State == "ready" {
		httpkit.WriteJSON(w, 200, viewBucket(b))
		return
	}
	if b.State != "provisioning" {
		BucketProblem(w, state.ErrConflict)
		return
	}
	b, err = s.ProvisionBucket(r.Context(), st, b)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 201, viewBucket(b))
}

func (s *Service) reserveBucket(ctx context.Context, st state.ObjectBucketStore, account, app string, req CreateBucketRequest) (state.ObjectBucket, error) {
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
			if old.PublicRead != req.Public || old.ServeAt != req.ServeAt {
				return old, state.ErrConflict
			}
			return old, nil
		}
	}
	if req.Region == "" {
		req.Region = s.Host.ObjectStorage().DefaultRegion
	}
	if !s.Host.ObjectStorage().CanProvisionAllRegions() {
		return state.ObjectBucket{}, state.ErrObjectUsageStale
	}
	backend, err := s.Host.ObjectStorage().Default(req.Region)
	if err != nil {
		return state.ObjectBucket{}, err
	}
	id := uuid.NewString()
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: account, AppID: app, Name: req.Name, Scope: req.Scope, Region: req.Region, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", ""), PublicRead: req.Public, ServeAt: req.ServeAt}, s.Host.ObjectStorage().MaxBucketsPerApp)
	if err == nil && b.Region != req.Region {
		return b, state.ErrConflict
	}
	return b, err
}

func (s *Service) ProvisionBucket(ctx context.Context, st state.ObjectBucketStore, b state.ObjectBucket) (state.ObjectBucket, error) {
	if !s.ObjectStorageEnabled() {
		return b, objectstorage.ErrUnavailable
	}
	token := uuid.NewString()
	b, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, token, "provisioning")
	if err != nil {
		return b, err
	}
	err = s.ExecuteBucketOperation(ctx, st, b)
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

func (s *Service) loadBucket(w http.ResponseWriter, r *http.Request, acct state.Account, ready bool) (state.ObjectBucket, state.ObjectBucketStore, objectstorage.Provider, bool) {
	b, st, ok := s.loadBucketRecord(w, r, acct)
	if !ok {
		return state.ObjectBucket{}, nil, nil, false
	}
	var err error
	if ready && b.State != "ready" {
		err = state.ErrConflict
	}
	if err != nil {
		BucketProblem(w, err)
		return b, st, nil, false
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		BucketProblem(w, err)
		return b, st, nil, false
	}
	return b, st, backend.Provider, true
}

func (s *Service) AuthorizeBucketData(w http.ResponseWriter, r *http.Request, bucket state.ObjectBucket, permission string) bool {
	p, ok := httpkit.PrincipalFrom(r)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return false
	}
	if p.Key == nil || httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin) {
		return true
	}
	access, ok := s.Host.Store().(state.ObjectBucketAccessStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return false
	}
	allowed, err := access.ObjectBucketKeyCan(r.Context(), bucket.AccountID, bucket.ID, p.Key.ID, permission)
	if err != nil {
		BucketProblem(w, err)
		return false
	}
	if !allowed {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "object_storage_access_denied", "Forbidden", "The API key has no matching grant for this bucket."))
		return false
	}
	return true
}

func (s *Service) DeleteBucket(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, _, ok := s.loadBucket(w, r, acct, false)
	if !ok {
		return
	}
	if nonEmpty, err := s.customerBucketHasData(r.Context(), b); err != nil || nonEmpty {
		if err == nil {
			err = objectstorage.ErrNotEmpty
		}
		BucketProblem(w, err)
		return
	}
	var bindingsToRevoke []state.ObjectS3Credential
	if bindings, ok := s.Host.Store().(state.ObjectS3CredentialBindingStore); ok {
		credentials, err := bindings.ListObjectS3Credentials(r.Context(), acct.ID, b.ID)
		if err != nil {
			BucketProblem(w, err)
			return
		}
		for _, credential := range credentials {
			if credential.ManagedAppID == b.AppID {
				bindingsToRevoke = append(bindingsToRevoke, credential)
			}
		}
	}
	if len(bindingsToRevoke) > 0 {
		bindings, ok := s.Host.Store().(state.ObjectS3CredentialBindingStore)
		if !ok {
			BucketProblem(w, objectstorage.ErrUnavailable)
			return
		}
		for _, credential := range bindingsToRevoke {
			changed, err := bindings.RevokeObjectS3ComputeBinding(r.Context(), acct.ID, b.ID, credential.ID)
			if err != nil {
				BucketProblem(w, err)
				return
			}
			if changed {
				s.Host.NotifyRuntimeConfigChange(r.Context(), db.NotifySecretRotated, acct, state.App{ID: credential.ManagedAppID}, "binding_revoked", credential.ManagedScope, "")
			}
		}
	}
	token := uuid.NewString()
	b, err := st.ClaimObjectBucket(r.Context(), b.AccountID, b.AppID, b.ID, token, "deleting")
	if err != nil {
		BucketProblem(w, err)
		return
	}
	err = s.ExecuteBucketOperation(r.Context(), st, b)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// customerBucketHasData reports whether a bucket the customer must empty
// still holds objects or retained versions. Deletion revokes the app's compute
// bindings before the provider is asked; when the provider then refused a
// non-empty bucket, the bucket returned to ready with its bindings gone and the
// app lost access to data that still existed (hunt #9). Owned buckets are
// emptied by the deletion itself and are not checked.
func (s *Service) customerBucketHasData(ctx context.Context, b state.ObjectBucket) (bool, error) {
	if b.State != "ready" {
		return false, nil
	}
	if owned, err := s.ownedBucketCleanupRequired(ctx, b); err != nil || owned {
		return false, err
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return false, objectstorage.ErrConfiguration
	}
	if err := s.inventoryRequestRecorder(b)(ctx); err != nil {
		return false, err
	}
	page, err := backend.Provider.ListObjects(ctx, b.PhysicalName, "", "", 1)
	if errors.Is(err, objectstorage.ErrNotFound) {
		return false, nil
	}
	if err != nil || len(page.Items) > 0 {
		return len(page.Items) > 0, err
	}
	versions, ok := backend.Provider.(objectstorage.ObjectVersionInventoryProvider)
	if !ok {
		return false, nil
	}
	if err := s.inventoryRequestRecorder(b)(ctx); err != nil {
		return false, err
	}
	retained, err := versions.ListObjectVersions(ctx, b.PhysicalName, "", 1)
	if errors.Is(err, objectstorage.ErrNotFound) || errors.Is(err, objectstorage.ErrUnsupported) {
		return false, nil
	}
	return len(retained.Items) > 0, err
}

func (s *Service) ListBucketObjects(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, _, provider, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return
	}
	if !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionRead) {
		return
	}
	prefix, cursor := r.URL.Query().Get("prefix"), r.URL.Query().Get("cursor")
	limit := int64(100)
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
	}
	if err != nil || limit < 1 || limit > 1000 || len(prefix) > 1024 || !utf8.ValidString(prefix) || len(cursor) > 8192 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if err := s.CustomerObjectRequestRecorder(b)(r.Context()); err != nil {
		BucketProblem(w, err)
		return
	}
	page, err := provider.ListObjects(r.Context(), b.PhysicalName, prefix, cursor, int32(limit))
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, page)
}

func (s *Service) DeleteBucketObject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, _, provider, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return
	}
	if !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	key := r.URL.Query().Get("key")
	if err := (objectstorage.SignRequest{Method: "GET", Key: key}).Validate(1); err != nil {
		BucketProblem(w, err)
		return
	}
	if err := objectstorage.ValidateObjectDeleteRequest(r); err != nil {
		BucketProblem(w, err)
		return
	}
	id, err := controlDeletionID(r)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	w.Header().Set("X-Gregale-Delete-Id", id)
	j, err := s.DeleteMutableBucketObject(r.Context(), b, provider, key, "", id)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	if j.VersionID != "" {
		w.Header().Set("X-Amz-Version-Id", j.VersionID)
	}
	if j.DeleteMarker {
		w.Header().Set("X-Amz-Delete-Marker", "true")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) SignBucketObject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	var req objectstorage.SignRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	handler := func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		s.issueSignedBucketObject(w, r, acct, req)
	}
	if req.Method == http.MethodPut {
		s.Host.RequireScope(api.ScopesStorageWriteSurface...)(handler)(w, r, acct)
		return
	}
	s.Host.RequireScope(api.ScopesStorageReadSurface...)(handler)(w, r, acct)
}

func viewBucketAccessGrant(grant state.ObjectBucketAccessGrant) api.ObjectBucketAccessGrant {
	return api.ObjectBucketAccessGrant{
		KeyID: grant.APIKeyID, KeyLabel: grant.KeyLabel, KeyStatus: grant.KeyStatus,
		Permission: grant.Permission, CreatedAt: grant.CreatedAt, UpdatedAt: grant.UpdatedAt,
	}
}

func (s *Service) bucketAccessStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectBucketAccessStore, bool) {
	bucket, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok {
		return state.ObjectBucket{}, nil, false
	}
	access, ok := s.Host.Store().(state.ObjectBucketAccessStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return state.ObjectBucket{}, nil, false
	}
	return bucket, access, true
}

func (s *Service) ListBucketAccessGrants(w http.ResponseWriter, r *http.Request, acct state.Account) {
	bucket, access, ok := s.bucketAccessStore(w, r, acct)
	if !ok {
		return
	}
	grants, err := access.ListObjectBucketAccessGrants(r.Context(), acct.ID, bucket.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	items := make([]api.ObjectBucketAccessGrant, 0, len(grants))
	for _, grant := range grants {
		items = append(items, viewBucketAccessGrant(grant))
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ObjectBucketAccessGrantList{Items: items})
}

func (s *Service) SetBucketAccessGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	bucket, access, ok := s.bucketAccessStore(w, r, acct)
	if !ok {
		return
	}
	keyID := r.PathValue("key")
	if _, err := uuid.Parse(keyID); err != nil {
		BucketProblem(w, state.ErrNotFound)
		return
	}
	var req api.SetObjectBucketAccessGrantRequest
	if !decodeBucketRequest(w, r, &req) {
		return
	}
	key, err := s.objectGrantKey(r.Context(), acct.ID, keyID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	if key.Status != string(state.APIKeyStatusActive) && key.Status != string(state.APIKeyStatusGrace) {
		BucketProblem(w, state.ErrConflict)
		return
	}
	valid := !httpkit.APIKeyCarriesScope(key, api.ScopeAdmin)
	switch req.Permission {
	case api.ObjectBucketPermissionRead:
		valid = valid && httpkit.APIKeyCarriesScope(key, api.ScopeStorageRead)
	case api.ObjectBucketPermissionWrite:
		valid = valid && httpkit.APIKeyCarriesScope(key, api.ScopeStorageWrite)
	case api.ObjectBucketPermissionReadWrite:
		valid = valid && httpkit.APIKeyCarriesScope(key, api.ScopeStorageRead) && httpkit.APIKeyCarriesScope(key, api.ScopeStorageWrite)
	default:
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if !valid {
		BucketProblem(w, state.ErrConflict)
		return
	}
	grant, err := access.SetObjectBucketAccessGrant(r.Context(), acct.ID, bucket.ID, keyID, req.Permission)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.access_grant_set", &acct.ID, map[string]any{
		"app_id": bucket.AppID, "bucket_id": bucket.ID, "key_id": keyID, "permission": req.Permission,
	})
	httpkit.WriteJSON(w, http.StatusOK, viewBucketAccessGrant(grant))
}

func (s *Service) DeleteBucketAccessGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	bucket, access, ok := s.bucketAccessStore(w, r, acct)
	if !ok {
		return
	}
	keyID := r.PathValue("key")
	if _, err := uuid.Parse(keyID); err != nil {
		BucketProblem(w, state.ErrNotFound)
		return
	}
	if err := access.DeleteObjectBucketAccessGrant(r.Context(), acct.ID, bucket.ID, keyID); err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.access_grant_deleted", &acct.ID, map[string]any{
		"app_id": bucket.AppID, "bucket_id": bucket.ID, "key_id": keyID,
	})
	w.WriteHeader(http.StatusNoContent)
}

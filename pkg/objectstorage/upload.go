package objectstorage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type UploadAuthenticator interface {
	AuthenticateKey(context.Context, []byte) (state.Account, state.APIKey, error)
}

type UploadConfig struct {
	Store          PublicReadStore
	Routes         state.ObjectUploadRouteStore
	Buckets        state.ObjectBucketStore
	Authenticator  UploadAuthenticator
	Registry       *Registry
	Accounting     state.ObjectStorageAccountingStore
	RequestMetrics state.ObjectStorageProviderUsageStore
	Enabled        func() bool
	AppsDomain     string
	Next           http.Handler
	Now            func() time.Time
	Log            *slog.Logger
}

type uploadHandler struct {
	store          PublicReadStore
	routes         state.ObjectUploadRouteStore
	buckets        state.ObjectBucketStore
	authenticator  UploadAuthenticator
	registry       *Registry
	accounting     state.ObjectStorageAccountingStore
	requestMetrics state.ObjectStorageProviderUsageStore
	enabled        func() bool
	appsDomain     string
	next           http.Handler
	now            func() time.Time
	log            *slog.Logger
}

// NewUploadHandler adds policy-controlled POST /uploads/{route} handling to
// the public edge. A miss is passed through untouched, preserving ordinary
// application routing for applications that do not declare a route.
func NewUploadHandler(c UploadConfig) (http.Handler, error) {
	if c.Store == nil || c.Routes == nil || c.Buckets == nil || c.Authenticator == nil || c.Registry == nil || c.Next == nil {
		return nil, errors.New("object storage upload: store, routes, buckets, authenticator, registry and next are required")
	}
	if c.Registry.Accounting.GatewaySafety() {
		if _, ok := c.RequestMetrics.(state.ObjectStorageGatewayRequestStore); !ok || c.Accounting == nil {
			return nil, errors.New("object storage upload: gateway safety accounting requires atomic request admission")
		}
	}
	if c.Enabled == nil {
		c.Enabled = func() bool { return true }
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	domain := strings.Trim(strings.ToLower(c.AppsDomain), ".")
	if domain == "" {
		domain = "gregale.dev"
	}
	return &uploadHandler{store: c.Store, routes: c.Routes, buckets: c.Buckets, authenticator: c.Authenticator, registry: c.Registry, accounting: c.Accounting, requestMetrics: c.RequestMetrics, enabled: c.Enabled, appsDomain: domain, next: c.Next, now: c.Now, log: c.Log}, nil
}

func (h *uploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.serveUploadReceipt(w, r) {
		return
	}
	name, ok := uploadRouteName(r.URL.Path)
	if !ok {
		h.next.ServeHTTP(w, r)
		return
	}
	app, route, ok := h.lookupUploadRoute(w, r, name)
	if !ok {
		return
	}
	acct, key, ok := h.authenticate(w, r, app)
	if !ok {
		return
	}
	completion, ok := h.prepareUpload(w, r, app, route, acct, key)
	if !ok {
		return
	}
	bucket, writer, ok := h.uploadDestination(w, r, app, route)
	if !ok {
		return
	}
	if tracked, ok := writer.(TrackedObjectWriter); ok {
		st, ok := h.routes.(state.ObjectTrackedUploadStore)
		if !ok {
			uploadProblem(w, http.StatusServiceUnavailable, "upload tracking is unavailable")
			return
		}
		h.performTrackedUpload(w, r, st, tracked, bucket, completion)
		return
	}
	if !completion.Protection.Empty() || !completion.Encryption.Empty() {
		uploadProblem(w, http.StatusNotImplemented, "the selected storage provider does not support tracked encrypted uploads")
		return
	}
	h.performLegacyUpload(w, r, writer, bucket, completion)
}

func (h *uploadHandler) lookupUploadRoute(w http.ResponseWriter, r *http.Request, name string) (state.App, state.ObjectUploadRoute, bool) {
	app, err := h.lookupApp(r)
	if err != nil {
		h.next.ServeHTTP(w, r)
		return app, state.ObjectUploadRoute{}, false
	}
	route, err := h.routes.GetObjectUploadRoute(r.Context(), app.AccountID, app.ID, name)
	if errors.Is(err, state.ErrNotFound) {
		h.next.ServeHTTP(w, r)
		return app, route, false
	}
	if err != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "upload route unavailable")
		return app, route, false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		uploadProblem(w, http.StatusMethodNotAllowed, "upload route only accepts POST")
		return app, route, false
	}
	if !route.Enabled {
		uploadProblem(w, http.StatusNotFound, "upload route not found")
		return app, route, false
	}
	if !h.enabled() {
		uploadProblem(w, http.StatusServiceUnavailable, "object storage is temporarily disabled")
		return app, route, false
	}
	return app, route, true
}

func (h *uploadHandler) prepareUpload(w http.ResponseWriter, r *http.Request, app state.App, route state.ObjectUploadRoute, acct state.Account, key state.APIKey) (state.ObjectUploadCompletion, bool) {
	subject := keySubject(acct, key)
	c := state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: app.AccountID, AppID: app.ID, BucketID: route.BucketID, SubjectID: subject, Key: generatedUploadKey(route.KeyPrefix, subject), Bytes: max(r.ContentLength, 0), ContentType: normalizedContentType(r.Header.Get("Content-Type")), RequestID: r.Header.Get("X-Request-ID"), Status: "pending", CreatedAt: h.now()}
	if !h.validateUpload(w, r, route, c) {
		return c, false
	}
	idem, err := parseUploadIdempotencyKey(r.Header.Get("Idempotency-Key"))
	if err != nil {
		uploadProblem(w, http.StatusBadRequest, "Idempotency-Key is invalid")
		return c, false
	}
	c.IdempotencyKey = idem
	if idem != "" {
		c.RequestFingerprint = uploadRequestFingerprint(key.Hash, route.ID, subject, r.ContentLength, c.ContentType, r.Header)
		c.Key = idempotentUploadKey(key.Hash, route.KeyPrefix, route.ID, subject, idem)
		existing, err := h.routes.GetObjectUploadIntent(r.Context(), route.ID, subject, idem)
		if err == nil {
			h.replayIdempotent(w, existing, c.RequestFingerprint)
			return c, false
		}
		if !errors.Is(err, state.ErrNotFound) {
			uploadProblem(w, http.StatusServiceUnavailable, "upload idempotency state is unavailable")
			return c, false
		}
	}
	c.Encryption = route.Encryption.Clone()
	c.RuntimeSinglePutLimit = h.registry.MaxSinglePutBytes
	return c, true
}
func (h *uploadHandler) validateUpload(w http.ResponseWriter, r *http.Request, route state.ObjectUploadRoute, c state.ObjectUploadCompletion) bool {
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-object-lock-") {
			uploadProblem(w, http.StatusBadRequest, "upload protection is selected by the bucket policy")
			return false
		}
		if strings.HasPrefix(strings.ToLower(name), "x-amz-server-side-encryption") {
			uploadProblem(w, http.StatusBadRequest, "upload encryption is selected by the route policy")
			return false
		}
	}
	limit := min(route.MaxBytes, h.registry.MaxUploadBytes)
	if !route.Encryption.Empty() {
		limit = min(limit, h.registry.MaxSinglePutBytes)
	}
	status, code, detail := 0, "", ""
	switch {
	case !allowedUploadContentType(route.AllowedContentTypes, c.ContentType) || ValidateContentType(c.ContentType) != nil:
		status, code, detail = http.StatusUnsupportedMediaType, "content_type_not_allowed", "content type is not allowed by this upload route"
	case r.ContentLength < 0:
		status, code, detail = http.StatusLengthRequired, "content_length_required", "Content-Length is required for bounded uploads"
	case r.ContentLength > limit:
		status, code, detail = http.StatusRequestEntityTooLarge, "size_limit_exceeded", "upload exceeds the route byte limit"
	}
	if status == 0 {
		return true
	}
	c.Status = "rejected"
	c.ErrorCode = code
	h.record(r.Context(), c)
	uploadProblem(w, status, detail)
	return false
}
func (h *uploadHandler) uploadDestination(w http.ResponseWriter, r *http.Request, app state.App, route state.ObjectUploadRoute) (state.ObjectBucket, ObjectWriter, bool) {
	if h.accounting == nil || !h.registry.Accounting.Valid() {
		uploadProblem(w, http.StatusServiceUnavailable, "object storage usage is temporarily unavailable")
		return state.ObjectBucket{}, nil, false
	}
	bucket, err := h.buckets.GetObjectBucket(r.Context(), app.AccountID, app.ID, route.BucketID)
	if err != nil || bucket.State != "ready" {
		uploadProblem(w, http.StatusNotFound, "upload destination is unavailable")
		return bucket, nil, false
	}
	backend, err := h.registry.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "object storage is temporarily unavailable")
		return bucket, nil, false
	}
	if locks, ok := h.buckets.(state.ObjectBucketObjectLockStore); ok {
		j, e := locks.GetObjectBucketObjectLock(r.Context(), bucket.AccountID, bucket.AppID, bucket.ID)
		if e != nil {
			uploadProblem(w, http.StatusServiceUnavailable, "upload protection is temporarily unavailable")
			return bucket, nil, false
		}
		if (j.EnabledRequired || j.NativeEnabledObserved) && !backend.ObjectLock.Enabled {
			uploadProblem(w, http.StatusNotImplemented, "protected uploads are disabled on this backend")
			return bucket, nil, false
		}
		if j.ObservedConfiguration != nil && j.ObservedConfiguration.DefaultRetention != nil && j.ObservedConfiguration.DefaultRetention.DefaultEventHold != nil && !backend.ObjectLock.EventHolds {
			uploadProblem(w, http.StatusNotImplemented, "event hold uploads are disabled on this backend")
			return bucket, nil, false
		}
	}
	writer, ok := backend.Provider.(ObjectWriter)
	if !ok {
		uploadProblem(w, http.StatusNotImplemented, "the selected storage provider does not support streaming uploads")
		return bucket, nil, false
	}
	if !route.Encryption.Empty() {
		owner, err := uuid.Parse(app.AccountID)
		if err != nil || backend.Encryption.VerifySnapshot(owner.String(), route.Encryption) != nil {
			uploadProblem(w, http.StatusServiceUnavailable, "upload encryption is temporarily unavailable")
			return bucket, nil, false
		}
		if _, ok := writer.(ObjectEncryptionProvider); !ok {
			uploadProblem(w, http.StatusNotImplemented, "the selected storage provider does not support encrypted uploads")
			return bucket, nil, false
		}
	}
	return bucket, writer, true
}

func (h *uploadHandler) performLegacyUpload(w http.ResponseWriter, r *http.Request, writer ObjectWriter, bucket state.ObjectBucket, c state.ObjectUploadCompletion) {
	guard, err := h.admitTrackedObjectWrite(r.Context(), bucket)
	if err != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "upload destination writes are temporarily unavailable")
		return
	}
	defer func(ctx context.Context) {
		if err := guard.finishUnsent(ctx); err != nil {
			h.log.Warn("unsent upload writer receipt completion failed")
		}
	}(r.Context())
	if err := h.accounting.AdmitObjectURL(r.Context(), c.AccountID, bucket.ID, c.Key, c.Bytes, true, h.registry.Accounting); err != nil {
		uploadAccountingProblem(w, err)
		return
	}
	if !h.recordUploadAttempt(w, r, bucket.ID) {
		return
	}
	if c.IdempotencyKey != "" {
		intent, err := h.routes.CreateObjectUploadIntent(r.Context(), c)
		if err != nil {
			if errors.Is(err, state.ErrConflict) {
				if existing, e := h.routes.GetObjectUploadIntent(r.Context(), c.RouteID, c.SubjectID, c.IdempotencyKey); e == nil {
					h.replayIdempotent(w, existing, c.RequestFingerprint)
					return
				}
			}
			uploadProblem(w, http.StatusServiceUnavailable, "upload idempotency state is unavailable")
			return
		}
		c = intent
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.registry.TransferTimeout())
	defer cancel()
	result, err := guard.write(ctx, writer, c.Key, io.LimitReader(r.Body, c.Bytes), c.Bytes, ObjectMetadata{ContentType: c.ContentType})
	c.ETag = result.ETag
	c.Status = "completed"
	if err != nil {
		c.Status = "failed"
		c.ErrorCode = "provider_write_failed"
	}
	if !h.persistLegacyUpload(w, r, c) {
		return
	}
	if err != nil {
		uploadProblem(w, http.StatusBadGateway, "object storage upload failed")
		return
	}
	writeUploadJSON(w, http.StatusCreated, uploadResponse(c))
}
func (h *uploadHandler) persistLegacyUpload(w http.ResponseWriter, r *http.Request, c state.ObjectUploadCompletion) bool {
	if c.IdempotencyKey == "" {
		h.record(r.Context(), c)
		return true
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), api.ObjectUploadSettlementTimeout)
	defer cancel()
	if _, err := h.routes.UpdateObjectUploadCompletion(ctx, c); err != nil {
		h.log.Warn("object upload receipt update failed", "receipt_id", c.ID)
		uploadProblem(w, http.StatusServiceUnavailable, "object storage completion is temporarily unavailable")
		return false
	}
	return true
}
func (h *uploadHandler) recordUploadAttempt(w http.ResponseWriter, r *http.Request, bucket string) bool {
	if h.requestMetrics != nil {
		if err := RecordGatewayProviderRequest(r.Context(), h.requestMetrics, bucket, h.now(), h.registry.Accounting); err != nil {
			if h.registry.Accounting.GatewaySafety() {
				uploadAccountingProblem(w, err)
			} else {
				uploadProblem(w, http.StatusServiceUnavailable, "object storage usage is temporarily unavailable")
			}
			return false
		}
	}
	return true
}

func (h *uploadHandler) replayIdempotent(w http.ResponseWriter, completion state.ObjectUploadCompletion, requestFingerprint string) bool {
	if completion.RequestFingerprint != requestFingerprint {
		uploadProblem(w, http.StatusConflict, "Idempotency-Key was already used with different upload parameters")
		return true
	}
	w.Header().Set("X-Gregale-Upload-ID", completion.ID)
	switch completion.Status {
	case "completed":
		writeUploadJSON(w, http.StatusCreated, uploadResponse(completion))
	case "failed":
		uploadProblem(w, http.StatusBadGateway, "object storage upload failed")
	case "pending":
		w.Header().Set("Retry-After", strconv.Itoa(int(api.ObjectUploadRecoveryRetry.Seconds())))
		uploadProblem(w, http.StatusConflict, "this upload is pending provider confirmation; retry with the same Idempotency-Key")
	default:
		uploadProblem(w, http.StatusConflict, "Idempotency-Key cannot be replayed")
	}
	return true
}

func (h *uploadHandler) authenticate(w http.ResponseWriter, r *http.Request, app state.App) (state.Account, state.APIKey, bool) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" || !api.ValidAPIKeyFormat(token) {
		uploadProblem(w, http.StatusUnauthorized, "a Gregale API key is required")
		return state.Account{}, state.APIKey{}, false
	}
	acct, key, err := h.authenticator.AuthenticateKey(r.Context(), api.HashAPIKey(token))
	if err != nil || acct.ID != app.AccountID {
		uploadProblem(w, http.StatusUnauthorized, "the presented API key is not valid for this app")
		return state.Account{}, state.APIKey{}, false
	}
	if !acct.Active() || key.AppID != "" && key.AppID != app.ID || !slices.Contains(key.Scopes, api.ScopeAdmin) && !slices.Contains(key.Scopes, api.ScopeStorageWrite) {
		uploadProblem(w, http.StatusForbidden, "an active account and storage write permission for this app are required")
		return state.Account{}, state.APIKey{}, false
	}
	return acct, key, true
}

func (h *uploadHandler) lookupApp(r *http.Request) (state.App, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.Host), "."))
	if hostName, port, err := net.SplitHostPort(host); err == nil && port != "" {
		host = hostName
	} else {
		host = strings.Trim(host, "[]")
	}
	if strings.HasSuffix(host, "."+h.appsDomain) {
		slug := strings.TrimSuffix(host, "."+h.appsDomain)
		if slug == "" || strings.Contains(slug, ".") {
			return state.App{}, state.ErrNotFound
		}
		return h.store.AppBySlug(r.Context(), slug)
	}
	domain, err := h.store.DomainByName(r.Context(), host)
	if err != nil {
		return state.App{}, err
	}
	if !domain.Verified() {
		return state.App{}, state.ErrNotFound
	}
	return h.store.AppByID(r.Context(), domain.AppID)
}

func (h *uploadHandler) record(ctx context.Context, completion state.ObjectUploadCompletion) {
	if completion.Key == "" {
		completion.Key = "rejected"
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	if _, err := h.routes.RecordObjectUploadCompletion(recordCtx, completion); err != nil {
		h.log.Warn("object upload completion record failed", "receipt_id", completion.ID)
	}
}

const maxUploadIdempotencyKeyBytes = 128

func parseUploadIdempotencyKey(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxUploadIdempotencyKeyBytes {
		return "", errors.New("invalid idempotency key")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("invalid idempotency key")
		}
	}
	return value, nil
}

func uploadRequestFingerprint(key []byte, routeID, subject string, size int64, contentType string, headers http.Header) string {
	material := strings.Join([]string{routeID, subject, strconv.FormatInt(size, 10), contentType, strings.TrimSpace(headers.Get("Content-MD5")), strings.TrimSpace(headers.Get("Digest"))}, "\x00")
	return uploadMaterialMAC(key, material)
}

func idempotentUploadKey(key []byte, prefix, routeID, subject, idempotencyKey string) string {
	material := routeID + "\x00" + subject + "\x00" + idempotencyKey
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return subject + "/idem-" + uploadMaterialMAC(key, material)
	}
	return prefix + "/" + subject + "/idem-" + uploadMaterialMAC(key, material)
}

func uploadMaterialMAC(key []byte, material string) string {
	// HMAC-SHA256 is intentionally used instead of a bare SHA-256 digest:
	// route, subject, and idempotency inputs are request-derived sensitive
	// data, and the API-key fingerprint provides a stable per-principal key.
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(material))
	return hex.EncodeToString(mac.Sum(nil))
}

func uploadResponse(completion state.ObjectUploadCompletion) map[string]any {
	out := map[string]any{"id": completion.ID, "bucket_id": completion.BucketID, "key": completion.Key, "bytes": completion.Bytes, "content_type": completion.ContentType, "etag": completion.ETag, "status": completion.Status}
	if !completion.Encryption.Empty() {
		out["encryption"] = completion.Encryption.Clone().Selection
	}
	return out
}

func uploadRouteName(path string) (string, bool) {
	const prefix = "/uploads/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(path, prefix)
	if name == "" || strings.Contains(name, "/") || len(name) > 63 {
		return "", false
	}
	for i, r := range name {
		if i == 0 {
			if r < 'a' || r > 'z' {
				return "", false
			}
			continue
		}
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return "", false
		}
	}
	return name, true
}

func generatedUploadKey(prefix, subject string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return subject + "/" + uuid.NewString()
	}
	return prefix + "/" + subject + "/" + uuid.NewString()
}

func keySubject(acct state.Account, key state.APIKey) string {
	if key.ID != "" {
		return key.ID
	}
	return acct.ID
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

func normalizedContentType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "application/octet-stream"
	}
	if media, _, err := mime.ParseMediaType(value); err == nil {
		return strings.ToLower(media)
	}
	return strings.ToLower(value)
}

func allowedUploadContentType(allowed []string, contentType string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == contentType || strings.HasSuffix(value, "/*") && strings.HasPrefix(contentType, strings.TrimSuffix(value, "*")) {
			return true
		}
	}
	return false
}

func uploadProblem(w http.ResponseWriter, status int, detail string) {
	api.WriteProblem(w, api.NewProblem(status, "object_upload_error", http.StatusText(status), detail))
}

func uploadAccountingProblem(w http.ResponseWriter, err error) {
	var sizeErr *state.ObjectStorageLimitError
	if errors.As(err, &sizeErr) && sizeErr.Kind == "single_put_bytes" {
		uploadProblem(w, http.StatusRequestEntityTooLarge, "encrypted upload exceeds the current single PUT byte limit")
		return
	}
	status, detail := http.StatusServiceUnavailable, "object storage usage policy is unavailable"
	switch {
	case errors.Is(err, state.ErrObjectBudget):
		status, detail = http.StatusPaymentRequired, "object storage usage budget rejected this upload"
	case errors.Is(err, state.ErrObjectCapacity):
		status, detail = http.StatusConflict, "object storage capacity rejected this upload"
	}
	uploadProblem(w, status, detail)
}

func writeUploadJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonEncode(w, value)
}

func jsonEncode(w http.ResponseWriter, value any) error {
	return json.NewEncoder(w).Encode(value)
}

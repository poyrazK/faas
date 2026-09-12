package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
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
	name, ok := uploadRouteName(r.URL.Path)
	if !ok {
		h.next.ServeHTTP(w, r)
		return
	}
	app, err := h.lookupApp(r)
	if err != nil {
		h.log.Warn("object upload app lookup failed", "host", r.Host, "err", err)
		h.next.ServeHTTP(w, r)
		return
	}
	route, err := h.routes.GetObjectUploadRoute(r.Context(), app.AccountID, app.ID, name)
	if errors.Is(err, state.ErrNotFound) {
		h.next.ServeHTTP(w, r)
		return
	}
	if err != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "upload route unavailable")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		uploadProblem(w, http.StatusMethodNotAllowed, "upload route only accepts POST")
		return
	}
	if !route.Enabled {
		uploadProblem(w, http.StatusNotFound, "upload route not found")
		return
	}
	if !h.enabled() {
		uploadProblem(w, http.StatusServiceUnavailable, "object storage is temporarily disabled")
		return
	}
	acct, key, ok := h.authenticate(w, r, app)
	if !ok {
		return
	}
	subject := keySubject(acct, key)
	objectKey := generatedUploadKey(route.KeyPrefix, subject)
	contentType := normalizedContentType(r.Header.Get("Content-Type"))
	if !allowedUploadContentType(route.AllowedContentTypes, contentType) {
		h.record(r.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: app.AccountID, AppID: app.ID, BucketID: route.BucketID, SubjectID: subject, Key: objectKey, ContentType: contentType, Status: "rejected", ErrorCode: "content_type_not_allowed", RequestID: r.Header.Get("X-Request-ID"), CreatedAt: h.now()})
		uploadProblem(w, http.StatusUnsupportedMediaType, "content type is not allowed by this upload route")
		return
	}
	if r.ContentLength < 0 {
		h.record(r.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: app.AccountID, AppID: app.ID, BucketID: route.BucketID, SubjectID: subject, Key: objectKey, ContentType: contentType, Status: "rejected", ErrorCode: "content_length_required", RequestID: r.Header.Get("X-Request-ID"), CreatedAt: h.now()})
		uploadProblem(w, http.StatusLengthRequired, "Content-Length is required for bounded uploads")
		return
	}
	if r.ContentLength > route.MaxBytes || r.ContentLength > h.registry.MaxUploadBytes {
		h.record(r.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: app.AccountID, AppID: app.ID, BucketID: route.BucketID, SubjectID: subject, Key: objectKey, Bytes: r.ContentLength, ContentType: contentType, Status: "rejected", ErrorCode: "size_limit_exceeded", RequestID: r.Header.Get("X-Request-ID"), CreatedAt: h.now()})
		uploadProblem(w, http.StatusRequestEntityTooLarge, "upload exceeds the route byte limit")
		return
	}
	if h.accounting == nil || !h.registry.Accounting.Valid() {
		uploadProblem(w, http.StatusServiceUnavailable, "object storage usage is temporarily unavailable")
		return
	}
	bucket, err := h.buckets.GetObjectBucket(r.Context(), app.AccountID, app.ID, route.BucketID)
	if err != nil || bucket.State != "ready" {
		uploadProblem(w, http.StatusNotFound, "upload destination is unavailable")
		return
	}
	if err := h.accounting.AdmitObjectURL(r.Context(), app.AccountID, bucket.ID, objectKey, r.ContentLength, true, h.registry.Accounting); err != nil {
		uploadAccountingProblem(w, err)
		return
	}
	backend, err := h.registry.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "object storage is temporarily unavailable")
		return
	}
	writer, ok := backend.Provider.(ObjectWriter)
	if !ok {
		uploadProblem(w, http.StatusNotImplemented, "the selected storage provider does not support streaming uploads")
		return
	}
	if h.requestMetrics != nil {
		if err := h.requestMetrics.RecordObjectStorageProviderRequest(r.Context(), bucket.ID, h.now()); err != nil {
			uploadProblem(w, http.StatusServiceUnavailable, "object storage usage is temporarily unavailable")
			return
		}
	}
	result, err := writer.WriteObject(r.Context(), bucket.PhysicalName, objectKey, io.LimitReader(r.Body, route.MaxBytes+1), r.ContentLength, ObjectMetadata{ContentType: contentType})
	completion := state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: app.AccountID, AppID: app.ID, BucketID: bucket.ID, SubjectID: subject, Key: objectKey, Bytes: r.ContentLength, ContentType: contentType, ETag: result.ETag, RequestID: r.Header.Get("X-Request-ID"), CreatedAt: h.now()}
	if err != nil {
		completion.Status = "failed"
		completion.ErrorCode = "provider_write_failed"
		h.record(r.Context(), completion)
		uploadProblem(w, http.StatusBadGateway, "object storage upload failed")
		return
	}
	completion.Status = "completed"
	h.record(r.Context(), completion)
	h.log.Info("object upload completed", "route", route.Name, "bucket_id", bucket.ID, "bytes", r.ContentLength)
	writeUploadJSON(w, http.StatusCreated, map[string]any{"id": completion.ID, "bucket_id": bucket.ID, "key": objectKey, "bytes": completion.Bytes, "content_type": contentType, "etag": result.ETag, "status": completion.Status})
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
	if _, err := h.routes.RecordObjectUploadCompletion(context.WithoutCancel(ctx), completion); err != nil {
		h.log.Warn("object upload completion record failed", "err", err)
	}
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

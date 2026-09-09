package objectstorage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/state"
)

const publicImmutableCacheControl = "public, max-age=31536000, immutable"

var publicReadPathRE = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9._~/-]*$`)

// PublicReadStore is the read-only state needed by the public asset edge.
// It deliberately excludes credentials and provisioning operations.
type PublicReadStore interface {
	AppBySlug(context.Context, string) (state.App, error)
	AppByID(context.Context, string) (state.App, error)
	DomainByName(context.Context, string) (state.CustomDomain, error)
	ListObjectBuckets(context.Context, string, string) ([]state.ObjectBucket, error)
}

type PublicReadConfig struct {
	Store          PublicReadStore
	Registry       *Registry
	RequestMetrics state.ObjectStorageProviderUsageStore
	Accounting     state.ObjectStorageAccountingStore
	Enabled        func() bool
	AppsDomain     string
	Next           http.Handler
	HTTPClient     *http.Client
	Now            func() time.Time
	Log            *slog.Logger
}

type publicReadHandler struct {
	store          PublicReadStore
	registry       *Registry
	requestMetrics state.ObjectStorageProviderUsageStore
	accounting     state.ObjectStorageAccountingStore
	enabled        func() bool
	appsDomain     string
	next           http.Handler
	client         *http.Client
	now            func() time.Time
	log            *slog.Logger
}

// NewPublicReadHandler mounts ready public buckets on app hostnames. A route
// miss is passed to Next untouched, which is what preserves normal app and API
// routing for paths that do not belong to a public bucket.
func NewPublicReadHandler(c PublicReadConfig) (http.Handler, error) {
	if c.Store == nil || c.Registry == nil || c.Next == nil {
		return nil, errors.New("object storage public read: store, registry and next are required")
	}
	if c.Enabled == nil {
		c.Enabled = func() bool { return true }
	}
	if c.HTTPClient == nil {
		transport := http.DefaultTransport
		if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
			clone := defaultTransport.Clone()
			clone.ResponseHeaderTimeout = 30 * time.Second
			transport = clone
		}
		c.HTTPClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	appsDomain := strings.Trim(strings.ToLower(c.AppsDomain), ".")
	if appsDomain == "" {
		appsDomain = "gregale.dev"
	}
	return &publicReadHandler{
		store: c.Store, registry: c.Registry, requestMetrics: c.RequestMetrics,
		accounting: c.Accounting, enabled: c.Enabled,
		appsDomain: appsDomain, next: c.Next,
		client: c.HTTPClient, now: c.Now, log: c.Log,
	}, nil
}

// ValidPublicReadPath validates the customer-facing mount path. A private
// bucket must not carry a stale path, and a public bucket must have one.
func ValidPublicReadPath(public bool, serveAt string) bool {
	if !public {
		return serveAt == ""
	}
	if serveAt == "" || !utf8.ValidString(serveAt) || !publicReadPathRE.MatchString(serveAt) || strings.HasSuffix(serveAt, "/") || strings.Contains(serveAt, "//") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(serveAt, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, r := range segment {
			if r < 0x20 || r == 0x7f || r == '\\' {
				return false
			}
		}
	}
	return true
}

func (h *publicReadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.next.ServeHTTP(w, r)
		return
	}
	bucket, key, matched, err := h.lookup(r)
	if err != nil {
		if matched {
			http.Error(w, "invalid object path", http.StatusBadRequest)
			return
		}
		h.log.Warn("public object lookup failed", "host", r.Host, "path", r.URL.Path)
		h.next.ServeHTTP(w, r)
		return
	}
	if !matched {
		h.next.ServeHTTP(w, r)
		return
	}
	if !h.enabled() {
		http.Error(w, "object storage is temporarily disabled", http.StatusServiceUnavailable)
		return
	}
	if key == "" || !ValidKey(key) || !validPublicKey(key) {
		http.NotFound(w, r)
		return
	}
	if h.accounting == nil || !h.registry.Accounting.Valid() {
		http.Error(w, "object storage usage is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := h.accounting.AdmitObjectURL(r.Context(), bucket.AccountID, bucket.ID, key, 0, false, h.registry.Accounting); err != nil {
		http.Error(w, "object storage usage is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	backend, err := h.registry.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	signed, err := backend.Provider.Presign(r.Context(), bucket.PhysicalName, SignRequest{Method: r.Method, Key: key, ExpiresIn: 60})
	if err != nil {
		http.Error(w, "object not found", http.StatusNotFound)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), r.Method, signed.URL, nil)
	if err != nil {
		http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	for name, values := range signed.Headers {
		upstream.Header.Set(name, values)
	}
	for _, name := range []string{"Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		if value := r.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	if h.requestMetrics != nil {
		if err := h.requestMetrics.RecordObjectStorageProviderRequest(r.Context(), bucket.ID, h.now()); err != nil {
			http.Error(w, "object storage usage is temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	response, err := h.client.Do(upstream)
	if err != nil {
		http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = response.Body.Close() }() // best effort; the provider response is already accounted
	if response.StatusCode == http.StatusNotModified {
		copyPublicObjectHeaders(w.Header(), response.Header)
		w.Header().Set("Cache-Control", publicImmutableCacheControl)
		w.Header().Del("Content-Disposition")
		w.WriteHeader(response.StatusCode)
		return
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if response.StatusCode == http.StatusNotFound {
			http.NotFound(w, r)
		} else {
			http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	copyPublicObjectHeaders(w.Header(), response.Header)
	w.Header().Set("Cache-Control", publicImmutableCacheControl)
	w.Header().Del("Content-Disposition")
	w.WriteHeader(response.StatusCode)
	if r.Method != http.MethodGet {
		return
	}
	n, _ := io.Copy(w, response.Body)
	if n > 0 {
		if metrics, ok := h.requestMetrics.(state.ObjectStorageProviderEgressStore); ok {
			// A browser disconnect cancels the request context after bytes have
			// already left the provider. Preserve the bounded ledger write so
			// those bytes remain visible to the accounting path.
			egressCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
			err := metrics.RecordObjectStorageProviderEgress(egressCtx, bucket.ID, n, h.now())
			cancel()
			if err != nil {
				h.log.Warn("public object egress metric write failed", "bucket_id", bucket.ID)
			}
		}
	}
}

func (h *publicReadHandler) lookup(r *http.Request) (state.ObjectBucket, string, bool, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.Host), "."))
	if hostName, port, err := net.SplitHostPort(host); err == nil && port != "" {
		host = hostName
	} else {
		host = strings.Trim(host, "[]")
	}
	var app state.App
	var err error
	if h.appsDomain != "" && strings.HasSuffix(host, "."+h.appsDomain) {
		slug := strings.TrimSuffix(host, "."+h.appsDomain)
		if slug == "" || strings.Contains(slug, ".") {
			return state.ObjectBucket{}, "", false, nil
		}
		app, err = h.store.AppBySlug(r.Context(), slug)
	} else {
		domain, domainErr := h.store.DomainByName(r.Context(), host)
		if domainErr != nil {
			if errors.Is(domainErr, state.ErrNotFound) {
				return state.ObjectBucket{}, "", false, nil
			}
			return state.ObjectBucket{}, "", false, domainErr
		}
		if !domain.Verified() {
			return state.ObjectBucket{}, "", false, nil
		}
		app, err = h.store.AppByID(r.Context(), domain.AppID)
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.ObjectBucket{}, "", false, nil
		}
		return state.ObjectBucket{}, "", false, err
	}
	buckets, err := h.store.ListObjectBuckets(r.Context(), app.AccountID, app.ID)
	if err != nil {
		return state.ObjectBucket{}, "", false, err
	}
	path, err := url.PathUnescape(r.URL.EscapedPath())
	if err != nil {
		for _, bucket := range buckets {
			if bucket.State == "ready" && bucket.PublicRead && ValidPublicReadPath(true, bucket.ServeAt) {
				return bucket, "", true, err
			}
		}
		return state.ObjectBucket{}, "", false, err
	}
	for _, bucket := range buckets {
		if bucket.State != "ready" || !bucket.PublicRead || !ValidPublicReadPath(true, bucket.ServeAt) {
			continue
		}
		if path == bucket.ServeAt {
			return bucket, "", true, nil
		}
		if strings.HasPrefix(path, bucket.ServeAt+"/") {
			return bucket, strings.TrimPrefix(path, bucket.ServeAt+"/"), true, nil
		}
	}
	return state.ObjectBucket{}, "", false, nil
}

func validPublicKey(key string) bool {
	for _, segment := range strings.Split(key, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return false
		}
	}
	return true
}

func copyPublicObjectHeaders(dst, src http.Header) {
	for _, name := range []string{"Accept-Ranges", "Content-Length", "Content-Range", "Content-Type", "ETag", "Expires", "Last-Modified"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}

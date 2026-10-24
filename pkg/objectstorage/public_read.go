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
	mounts         *publicMountCache
	limiters       *publicReadLimiters
}

// NewPublicReadHandler mounts ready public buckets on app hostnames. A route
// miss is passed to Next untouched, which is what preserves normal app and API
// routing for paths that do not belong to a public bucket.
func NewPublicReadHandler(c PublicReadConfig) (http.Handler, error) {
	if c.Store == nil || c.Registry == nil || c.Next == nil {
		return nil, errors.New("object storage public read: store, registry and next are required")
	}
	if c.Registry.Accounting.GatewaySafety() {
		if _, ok := c.RequestMetrics.(state.ObjectStorageGatewayEgressStore); !ok || c.Accounting == nil {
			return nil, errors.New("object storage public read: gateway safety accounting requires durable meters and admission")
		}
		if _, ok := c.RequestMetrics.(state.ObjectStorageGatewayRequestStore); !ok {
			return nil, errors.New("object storage public read: gateway safety accounting requires atomic request admission")
		}
	}
	if c.Enabled == nil {
		c.Enabled = func() bool { return true }
	}
	if c.HTTPClient == nil {
		transport := http.DefaultTransport
		if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
			clone := defaultTransport.Clone()
			clone.DisableCompression = true
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
		mounts: newPublicMountCache(), limiters: newPublicReadLimiters(),
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
	if !h.limiters.allow(bucket.ID) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "too many requests for this public bucket", http.StatusTooManyRequests)
		return
	}
	// Strangers spend at most a share of the account's monthly budget.
	policy := h.registry.Accounting.AnonymousReadPolicy()
	if err := h.accounting.AdmitObjectURL(r.Context(), bucket.AccountID, bucket.ID, key, 0, false, policy); err != nil {
		writePublicReadRefusal(w, err)
		return
	}
	backend, err := h.registry.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	signed, err := PresignObjectRead(r.Context(), backend.Provider, bucket.PhysicalName, r.Method, key, 60)
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
		if err := RecordGatewayProviderRequest(r.Context(), h.requestMetrics, bucket.ID, h.now(), policy); err != nil {
			writePublicReadRefusal(w, err)
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
		w.Header().Set("Cache-Control", publicCacheControl)
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
	if _, gcs := backend.Provider.(*GCS); gcs {
		if _, err := ProviderReadEncryption(backend.Provider, backend.Encryption, bucket.AccountID, response.Header); err != nil || response.Uncompressed {
			http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	if r.Method == http.MethodGet && h.registry.Accounting.GatewaySafety() {
		if err := ReserveGatewayRead(r.Context(), h.requestMetrics, bucket.ID, response, policy, h.now()); err != nil {
			w.Header().Del("Content-Length")
			writePublicReadRefusal(w, err)
			return
		}
	}
	copyPublicObjectHeaders(w.Header(), response.Header)
	w.Header().Set("Cache-Control", publicCacheControl)
	w.Header().Del("Content-Disposition")
	w.WriteHeader(response.StatusCode)
	if r.Method != http.MethodGet {
		return
	}
	body := io.Reader(response.Body)
	if h.registry.Accounting.GatewaySafety() {
		body = io.LimitReader(body, response.ContentLength)
	}
	n, _ := io.Copy(w, body)
	if !h.registry.Accounting.GatewaySafety() {
		if err := RecordDeliveredEgress(r.Context(), h.requestMetrics, bucket.ID, n, h.now()); err != nil {
			h.log.Warn("public object egress metric write failed", "bucket_id", bucket.ID)
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
	buckets, err := h.hostMounts(r.Context(), host)
	if err != nil {
		return state.ObjectBucket{}, "", false, err
	}
	path, err := url.PathUnescape(r.URL.EscapedPath())
	if err != nil {
		if len(buckets) > 0 {
			return buckets[0], "", true, err
		}
		return state.ObjectBucket{}, "", false, err
	}
	for _, bucket := range buckets {
		if path == bucket.ServeAt {
			return bucket, "", true, nil
		}
		if strings.HasPrefix(path, bucket.ServeAt+"/") {
			return bucket, strings.TrimPrefix(path, bucket.ServeAt+"/"), true, nil
		}
	}
	return state.ObjectBucket{}, "", false, nil
}

// hostMounts resolves host to its servable public buckets, reusing a recent
// answer (see publicMountTTL). A host that names no app has no mounts.
func (h *publicReadHandler) hostMounts(ctx context.Context, host string) ([]state.ObjectBucket, error) {
	now := time.Now()
	if buckets, ok := h.mounts.get(host, now); ok {
		return buckets, nil
	}
	app, found, err := h.hostApp(ctx, host)
	if err != nil {
		return nil, err
	}
	var buckets []state.ObjectBucket
	if found {
		all, err := h.store.ListObjectBuckets(ctx, app.AccountID, app.ID)
		if err != nil {
			return nil, err
		}
		buckets = servableMounts(all)
	}
	h.mounts.put(host, buckets, now)
	return buckets, nil
}

func (h *publicReadHandler) hostApp(ctx context.Context, host string) (state.App, bool, error) {
	var app state.App
	var err error
	if h.appsDomain != "" && strings.HasSuffix(host, "."+h.appsDomain) {
		slug := strings.TrimSuffix(host, "."+h.appsDomain)
		if slug == "" || strings.Contains(slug, ".") {
			return state.App{}, false, nil
		}
		app, err = h.store.AppBySlug(ctx, slug)
	} else {
		domain, domainErr := h.store.DomainByName(ctx, host)
		if domainErr != nil {
			if errors.Is(domainErr, state.ErrNotFound) {
				return state.App{}, false, nil
			}
			return state.App{}, false, domainErr
		}
		if !domain.Verified() {
			return state.App{}, false, nil
		}
		app, err = h.store.AppByID(ctx, domain.AppID)
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.App{}, false, nil
		}
		return state.App{}, false, err
	}
	return app, true, nil
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

package objectstorage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"

	"github.com/onebox-faas/faas/pkg/api"
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
	// ReadAdmitter, when set, admits gateway-safety reads from cached
	// decisions and batches their counts (ADR-20261030075301 step 2). Build
	// it with the registry's AnonymousReadPolicy.
	ReadAdmitter *ReadAdmitter
	// Transfers paces object bodies to the edge's part of its node's port
	// share on a compute node; nil leaves them unpaced (ADR-20261031122757
	// step 7).
	Transfers *TransferLimiter
	// HostLookups bounds store reads for hostnames never seen naming an
	// app; nil leaves them unbounded (ADR-20261103163954 §4).
	HostLookups           *HostLookupBudget
	Enabled               func() bool
	TenantSurfacesEnabled func() bool
	AppsDomain            string
	Next                  http.Handler
	HTTPClient            *http.Client
	Now                   func() time.Time
	Log                   *slog.Logger
}

type publicReadHandler struct {
	store                 PublicReadStore
	registry              *Registry
	requestMetrics        state.ObjectStorageProviderUsageStore
	accounting            state.ObjectStorageAccountingStore
	readAdmitter          *ReadAdmitter
	transfers             *TransferLimiter
	enabled               func() bool
	tenantSurfacesEnabled func() bool
	appsDomain            string
	next                  http.Handler
	client                *http.Client
	now                   func() time.Time
	log                   *slog.Logger
	mounts                *publicMountCache
	lookups               singleflight.Group
	hostLookups           *HostLookupBudget
	limiters              *publicReadLimiters
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
		c.HTTPClient = &http.Client{Transport: transport, Timeout: c.Registry.TransferTimeout(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
		accounting: c.Accounting, readAdmitter: c.ReadAdmitter, transfers: c.Transfers, enabled: c.Enabled, tenantSurfacesEnabled: c.TenantSurfacesEnabled,
		appsDomain: appsDomain, next: c.Next,
		client: c.HTTPClient, now: c.Now, log: c.Log,
		mounts: newPublicMountCache(), hostLookups: c.HostLookups, limiters: newPublicReadLimiters(),
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
	if errors.Is(err, errHostLookupShed) {
		writeHostLookupShed(w)
		return
	}
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
	read, admitted := h.admit(w, r, bucket, key, policy)
	if !admitted {
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
	for _, name := range []string{"Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		if value := r.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	response, read, ok := h.readPublicObject(w, r, upstream, bucket, key, policy, read)
	if !ok {
		return
	}
	defer func() { _ = response.Body.Close() }() // best effort; the provider response is already accounted
	if writePublicReadCondition(w, response) {
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
	if _, err := ProviderReadEncryption(backend.Provider, backend.Encryption, bucket.AccountID, response.Header); err != nil {
		http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, gcs := backend.Provider.(*GCS); gcs {
		if response.Uncompressed {
			http.Error(w, "object storage is temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	if r.Method == http.MethodGet && h.registry.Accounting.GatewaySafety() {
		if err := h.reserveEgress(r.Context(), bucket, response, policy, read); err != nil {
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
	body = h.transfers.Reader(r.Context(), body, TransferDownload)
	n, _ := io.Copy(w, body)
	if !h.registry.Accounting.GatewaySafety() {
		if err := RecordDeliveredEgress(r.Context(), h.requestMetrics, bucket.ID, n, h.now()); err != nil {
			h.log.Warn("public object egress metric write failed", "bucket_id", bucket.ID)
		}
	}
}

// Conditions describe the selected public representation, not a provider
// outage. Keep their HTTP status and safe validators, without exposing the
// provider's XML error body, body framing or private response metadata.
func writePublicReadCondition(w http.ResponseWriter, response *http.Response) bool {
	switch response.StatusCode {
	case http.StatusNotModified:
		copyPublicObjectHeaders(w.Header(), response.Header)
		w.Header().Set("Cache-Control", publicCacheControl)
		w.Header().Del("Content-Disposition")
	case http.StatusPreconditionFailed, http.StatusRequestedRangeNotSatisfiable:
		for _, name := range []string{"ETag", "Last-Modified", "Accept-Ranges"} {
			if value := response.Header.Get(name); value != "" {
				w.Header().Set(name, value)
			}
		}
		if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			if value := response.Header.Get("Content-Range"); value != "" {
				w.Header().Set("Content-Range", value)
			}
		}
		w.Header().Set("Cache-Control", "no-store")
	default:
		return false
	}
	w.WriteHeader(response.StatusCode)
	return true
}

// admit admits an anonymous read. Under gateway safety with a read admitter
// it is admitted from the cached decision and its counts are batched
// (ADR-20261030075301 step 2); the returned read then carries its egress
// reservation. Otherwise it takes the locking admission and read is nil.
func (h *publicReadHandler) admit(w http.ResponseWriter, r *http.Request, bucket state.ObjectBucket, key string, policy api.ObjectStoragePolicy) (*AdmittedRead, bool) {
	if h.readAdmitter != nil && h.registry.Accounting.GatewaySafety() {
		read, err := h.readAdmitter.AdmitRead(r.Context(), bucket)
		if err != nil {
			writePublicReadRefusal(w, err)
			return nil, false
		}
		return &read, true
	}
	if err := h.accounting.AdmitObjectURL(r.Context(), bucket.AccountID, bucket.ID, key, 0, false, policy); err != nil {
		writePublicReadRefusal(w, err)
		return nil, false
	}
	return nil, true
}

// reserveEgress reserves a gateway-safety GET's body against the egress
// ceiling: in the admitter for a cached admission, otherwise in the
// database. An unknown length fails closed.
func (h *publicReadHandler) reserveEgress(ctx context.Context, bucket state.ObjectBucket, response *http.Response, policy api.ObjectStoragePolicy, read *AdmittedRead) error {
	if read == nil {
		return ReserveGatewayRead(ctx, h.requestMetrics, bucket.ID, response, policy, h.now())
	}
	if response.ContentLength < 0 || response.Uncompressed {
		return state.ErrObjectUsageStale
	}
	return h.readAdmitter.ReserveEgress(*read, response.ContentLength)
}

func (h *publicReadHandler) lookup(r *http.Request) (state.ObjectBucket, string, bool, error) {
	host := storageRequestHost(r.Host)
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
//
// Concurrent misses for one host share one lookup. Each used to read the
// control plane and spend the new-host budget on its own, so every expiry of a
// busy host's mounts, and every host after an edge restart, sent one store
// read per request in flight. The shared lookup runs apart from the request
// that started it, bounded by api.PublicReadHostLookupTimeout, so one caller's
// cancellation does not fail the others; each caller still leaves at its own
// deadline.
func (h *publicReadHandler) hostMounts(ctx context.Context, host string) ([]state.ObjectBucket, error) {
	checkTenant := !strings.HasSuffix(host, "."+h.appsDomain) && h.tenantSurfacesOn()
	if buckets, ok := h.mounts.get(host, time.Now()); ok && (!checkTenant || len(buckets) == 0) {
		return buckets, nil
	}
	key := host
	if checkTenant {
		key = "tenant\x00" + host
	}
	shared := h.lookups.DoChan(key, func() (any, error) {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.PublicReadHostLookupTimeout)
		defer cancel()
		return h.resolveHostMounts(lookupCtx, host, checkTenant)
	})
	select {
	case result := <-shared:
		if result.Err != nil {
			return nil, result.Err
		}
		buckets, _ := result.Val.([]state.ObjectBucket)
		return buckets, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// resolveHostMounts reads host's mounts from the store and caches them. A
// lookup that finished while this one waited to start is reused.
func (h *publicReadHandler) resolveHostMounts(ctx context.Context, host string, checkTenant bool) ([]state.ObjectBucket, error) {
	now := time.Now()
	cacheKey := host
	var binding *state.PlatformTenantHostBinding
	if checkTenant {
		if buckets, ok := h.mounts.get(host, now); ok && len(buckets) == 0 {
			return buckets, nil
		}
		if !h.hostLookups.admit(ctx, host) {
			return nil, errHostLookupShed
		}
		b, err := storageTenantBinding(ctx, h.store, host)
		if errors.Is(err, state.ErrPlatformTenantSuspended) {
			return nil, nil
		}
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			binding = &b
			cacheKey += "\x00" + b.SurfaceID + "\x00" + b.AppID + "\x00" + b.AccountID
		}
	}
	if buckets, ok := h.mounts.get(cacheKey, now); ok {
		return buckets, nil
	}
	app, found, err := h.mountApp(ctx, host, binding, checkTenant)
	if err != nil {
		return nil, err
	}
	var buckets []state.ObjectBucket
	if found {
		h.hostLookups.found(host)
		all, err := h.store.ListObjectBuckets(ctx, app.AccountID, app.ID)
		if err != nil {
			return nil, err
		}
		buckets = servableMounts(all)
	}
	h.mounts.put(cacheKey, buckets, now)
	return buckets, nil
}

func (h *publicReadHandler) tenantSurfacesOn() bool {
	if h.tenantSurfacesEnabled != nil {
		return h.tenantSurfacesEnabled()
	}
	return api.TenantSurfacesEnabled()
}

func (h *publicReadHandler) mountApp(ctx context.Context, host string, binding *state.PlatformTenantHostBinding, tenantChecked bool) (state.App, bool, error) {
	if !tenantChecked {
		return h.hostApp(ctx, host)
	}
	var app state.App
	var err error
	if binding != nil {
		app, err = storageTenantApp(ctx, h.store, *binding)
	} else {
		app, err = customDomainApp(ctx, h.store, host)
	}
	if errors.Is(err, state.ErrNotFound) {
		return state.App{}, false, nil
	}
	return app, err == nil, err
}

// errHostLookupShed is a host lookup refused by the new-host budget. It is
// answered 503, never passed on as a miss: the host may be a real app's.
var errHostLookupShed = errors.New("object storage public read: host lookup shed: new-host lookup budget spent")

// writeHostLookupShed answers a request whose host lookup was shed.
func writeHostLookupShed(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeTrafficPolicyUnavailable,
		"Traffic policy unavailable", "Gregale could not look up this host right now. Retry shortly."))
}

func (h *publicReadHandler) hostApp(ctx context.Context, host string) (state.App, bool, error) {
	var app state.App
	var err error
	if h.appsDomain != "" && strings.HasSuffix(host, "."+h.appsDomain) {
		slug := strings.TrimSuffix(host, "."+h.appsDomain)
		if slug == "" || strings.Contains(slug, ".") {
			return state.App{}, false, nil
		}
		if !h.hostLookups.admit(ctx, host) {
			return state.App{}, false, errHostLookupShed
		}
		app, err = h.store.AppBySlug(ctx, slug)
	} else {
		if !h.hostLookups.admit(ctx, host) {
			return state.App{}, false, errHostLookupShed
		}
		app, err = storageDomainApp(ctx, h.store, host, h.tenantSurfacesOn())
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
	for _, name := range []string{"Accept-Ranges", "Content-Encoding", "Content-Language", "Content-Length", "Content-Range", "Content-Type", "ETag", "Expires", "Last-Modified"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
	if sandboxedPublicType(dst.Get("Content-Type")) {
		dst.Set("Content-Security-Policy", publicObjectSandboxCSP)
	}
}

// publicObjectSandboxCSP runs an SVG or XML object opened directly with no
// script and no access to the app's origin. An <img> or CSS use is
// unaffected (ADR-20261104221012).
const publicObjectSandboxCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:"

// sandboxedPublicType reports an active type that is not a page: SVG and XML
// are images and data, so a public object of that type gets
// publicObjectSandboxCSP. HTML and XHTML stay as uploaded, since a bucket
// owner may host pages, and upload routes cannot put them in a public bucket.
func sandboxedPublicType(contentType string) bool {
	if !ActiveContentType(contentType) {
		return false
	}
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true
	}
	switch media {
	case "text/html", "application/xhtml+xml", "text/javascript", "application/javascript",
		"application/x-javascript", "text/ecmascript", "application/ecmascript", "text/jscript", "application/x-ecmascript":
		return false
	}
	return true
}

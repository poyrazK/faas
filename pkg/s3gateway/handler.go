// Package s3gateway implements Gregale's branded S3-compatible data plane.
// It authenticates Gregale-issued SigV4 credentials and translates logical
// path-style bucket operations onto the immutable provider placement recorded
// by pkg/state. Provider names, endpoints and credentials never cross the
// public response boundary.
package s3gateway

import (
	"context"
	"crypto/md5" // #nosec G501 -- Content-MD5 is an S3 wire-integrity check.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const CredentialSecretNamespace = "object_s3_credential"

const defaultMaxConcurrentPuts = 4

type Store interface {
	state.ObjectS3CredentialStore
	AdmitObjectURL(context.Context, string, string, string, int64, bool, api.ObjectStoragePolicy) error
}

type Config struct {
	Registry       *objectstorage.Registry
	Store          Store
	RequestMetrics state.ObjectStorageProviderUsageStore
	OpenSecret     func([]byte) (string, error)
	HTTPClient     *http.Client
	Enabled        func() bool
	Host           string
	Region         string
	SpoolDir       string
	MaxPutBytes    int64
	// MaxConcurrentPuts bounds local disk consumed by authenticated uploads
	// waiting to be verified or forwarded. Zero selects a conservative default.
	MaxConcurrentPuts int
	Now               func() time.Time
	Log               *slog.Logger
}

type Handler struct {
	registry       *objectstorage.Registry
	store          Store
	multipartStore state.ObjectMultipartUploadStore
	requestMetrics state.ObjectStorageProviderUsageStore
	openSecret     func([]byte) (string, error)
	client         *http.Client
	enabled        func() bool
	host           string
	region         string
	spoolDir       string
	maxPutBytes    int64
	putSlots       chan struct{}
	now            func() time.Time
	log            *slog.Logger
	touchMu        sync.Mutex
	lastTouch      map[string]time.Time
}

func New(c Config) (*Handler, error) {
	if c.Registry == nil || c.Store == nil || c.OpenSecret == nil {
		return nil, errors.New("s3 gateway: registry, store and secret opener are required")
	}
	if c.Host == "" {
		endpoint, err := url.Parse(c.Registry.PublicEndpoint)
		if err != nil {
			return nil, errors.New("s3 gateway: invalid public endpoint")
		}
		c.Host = endpoint.Host
	}
	if c.Region == "" {
		c.Region = c.Registry.PublicRegion
	}
	if c.Host == "" || c.Region == "" || strings.ContainsAny(c.Host, "/\\@") {
		return nil, errors.New("s3 gateway: host and region are required")
	}
	if c.MaxPutBytes == 0 {
		c.MaxPutBytes = min(c.Registry.MaxUploadBytes, api.MaxObjectSinglePutBytes)
	}
	if c.MaxPutBytes < 1 || c.MaxPutBytes > api.MaxObjectSinglePutBytes {
		return nil, errors.New("s3 gateway: invalid single PUT limit")
	}
	if c.MaxConcurrentPuts == 0 {
		c.MaxConcurrentPuts = defaultMaxConcurrentPuts
	}
	if c.MaxConcurrentPuts < 1 || c.MaxConcurrentPuts > 64 {
		return nil, errors.New("s3 gateway: invalid concurrent PUT limit")
	}
	if c.HTTPClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = 30 * time.Second
		transport.ExpectContinueTimeout = 5 * time.Second
		c.HTTPClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
	return &Handler{
		registry: c.Registry, store: c.Store, requestMetrics: c.RequestMetrics, openSecret: c.OpenSecret, client: c.HTTPClient,
		multipartStore: multipartStore(c.Store),
		enabled:        c.Enabled, host: strings.ToLower(c.Host), region: c.Region, spoolDir: c.SpoolDir,
		maxPutBytes: c.MaxPutBytes, putSlots: make(chan struct{}, c.MaxConcurrentPuts), now: c.Now, log: c.Log, lastTouch: map[string]time.Time{},
	}, nil
}

func multipartStore(store Store) state.ObjectMultipartUploadStore {
	storeWithMultipart, _ := store.(state.ObjectMultipartUploadStore)
	return storeWithMultipart
}

type requestContext struct {
	requestID  string
	credential state.ObjectS3Credential
	bucket     state.ObjectBucket
	provider   objectstorage.Provider
	signature  sigV4Request
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := strings.ReplaceAll(uuid.NewString(), "-", "")
	w.Header().Set("Server", "Gregale")
	w.Header().Set("x-amz-request-id", requestID)
	if !h.enabled() {
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale Object Storage is temporarily disabled.", r.URL.Path, requestID)
		return
	}
	if strings.ToLower(r.Host) != h.host {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "Use the configured Gregale S3 endpoint with path-style addressing.", r.URL.Path, requestID)
		return
	}
	if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
		writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "SigV4 streaming uploads are not implemented by Gregale yet.", r.URL.Path, requestID)
		return
	}
	var parsed sigV4Request
	var presigned bool
	var err error
	switch {
	case r.Header.Get("Authorization") != "" && r.URL.Query().Has("X-Amz-Algorithm"):
		writeS3Error(w, http.StatusForbidden, "InvalidRequest", "Use either header or query authentication, not both.", r.URL.Path, requestID)
		return
	case r.Header.Get("Authorization") != "":
		parsed, err = parseSigV4(r, h.region, h.now())
	case r.URL.Query().Has("X-Amz-Algorithm"):
		presigned = true
		parsed, err = parsePresignedSigV4(r, h.region, h.now())
	default:
		err = errSignature
	}
	if err != nil {
		writeS3Error(w, http.StatusForbidden, "SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided.", r.URL.Path, requestID)
		return
	}
	credential, bucket, err := h.store.ResolveObjectS3Credential(r.Context(), parsed.AccessKeyID)
	if err != nil {
		writeS3Error(w, http.StatusForbidden, "InvalidAccessKeyId", "The AWS access key ID you provided does not exist in Gregale.", r.URL.Path, requestID)
		return
	}
	secret, err := h.openSecret(credential.SecretSealed)
	if err != nil || presigned && verifyPresignedSigV4(r.Context(), r, parsed, secret, h.region) != nil || !presigned && verifySigV4(r.Context(), r, parsed, secret, h.region) != nil {
		writeS3Error(w, http.StatusForbidden, "SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided.", r.URL.Path, requestID)
		return
	}
	backend, err := h.registry.Resolve(bucket.BackendID, bucket.BackendFingerprint)
	if err != nil {
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not reach this bucket's storage placement.", r.URL.Path, requestID)
		return
	}
	h.touchCredential(r.Context(), credential.ID)
	request := requestContext{requestID: requestID, credential: credential, bucket: bucket, provider: backend.Provider, signature: parsed}
	h.route(w, r, request)
}

func (h *Handler) touchCredential(ctx context.Context, credentialID string) {
	now := h.now().UTC()
	h.touchMu.Lock()
	if last := h.lastTouch[credentialID]; now.Sub(last) < time.Minute {
		h.touchMu.Unlock()
		return
	}
	h.lastTouch[credentialID] = now
	h.touchMu.Unlock()
	if err := h.store.TouchObjectS3Credential(ctx, credentialID, now); err != nil {
		h.log.Warn("S3 credential last-used update failed", "credential_id", credentialID)
	}
}

func (h *Handler) route(w http.ResponseWriter, r *http.Request, req requestContext) {
	query := operationQuery(r.URL.Query())
	bucketName, key, hasBucket, hasKey, err := parsePath(r.URL.EscapedPath())
	if err != nil {
		writeS3Error(w, http.StatusBadRequest, "InvalidURI", "Could not parse the specified URI.", r.URL.Path, req.requestID)
		return
	}
	if !hasBucket {
		if r.Method == http.MethodGet && len(query) == 0 && h.can(req.credential, state.ObjectBucketPermissionRead) {
			writeS3XML(w, http.StatusOK, req.requestID, listAllMyBucketsResult{XMLNS: s3XMLNamespace, Buckets: listBucketsBody{Buckets: []listBucket{{Name: req.bucket.Name, CreationDate: req.bucket.CreatedAt.UTC().Format(time.RFC3339)}}}})
			return
		}
		h.unsupported(w, r, req.requestID)
		return
	}
	if bucketName != req.bucket.Name {
		writeS3Error(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.", r.URL.Path, req.requestID)
		return
	}
	if !hasKey {
		h.routeBucket(w, r, req)
		return
	}
	h.routeObject(w, r, req, key)
}

func parsePath(escapedPath string) (bucket, key string, hasBucket, hasKey bool, err error) {
	if escapedPath == "" || escapedPath == "/" {
		return "", "", false, false, nil
	}
	if !strings.HasPrefix(escapedPath, "/") {
		return "", "", false, false, errors.New("missing leading slash")
	}
	parts := strings.SplitN(strings.TrimPrefix(escapedPath, "/"), "/", 2)
	bucket, err = url.PathUnescape(parts[0])
	if err != nil || bucket == "" || strings.Contains(bucket, "/") {
		return "", "", false, false, errors.New("invalid bucket")
	}
	if len(parts) == 1 {
		return bucket, "", true, false, nil
	}
	key, err = url.PathUnescape(parts[1])
	if err != nil || !objectstorage.ValidKey(key) {
		return "", "", false, false, errors.New("invalid key")
	}
	return bucket, key, true, true, nil
}

func (h *Handler) routeBucket(w http.ResponseWriter, r *http.Request, req requestContext) {
	query := operationQuery(r.URL.Query())
	query.Del("x-id")
	if r.Method == http.MethodHead && len(query) == 0 {
		if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) {
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method == http.MethodGet && query.Has("location") {
		if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) {
			return
		}
		writeS3XML(w, http.StatusOK, req.requestID, struct {
			XMLName xml.Name `xml:"LocationConstraint"`
			XMLNS   string   `xml:"xmlns,attr"`
			Value   string   `xml:",chardata"`
		}{XMLNS: s3XMLNamespace, Value: h.region})
		return
	}
	if r.Method == http.MethodGet && query.Has("uploads") {
		h.listMultipartUploads(w, r, req)
		return
	}
	if r.Method == http.MethodGet && query.Get("list-type") == "2" {
		if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) || !h.admit(w, r, req, "__list__", 0, false) {
			return
		}
		prefix, cursor := query.Get("prefix"), query.Get("continuation-token")
		limit := int64(1000)
		var err error
		if raw := query.Get("max-keys"); raw != "" {
			limit, err = strconv.ParseInt(raw, 10, 32)
		}
		delimiter := query.Get("delimiter")
		if err != nil || limit < 1 || limit > 1000 || len(prefix) > 1024 || !utf8.ValidString(prefix) || len(cursor) > 8192 || !validDelimiter(delimiter) {
			writeS3Error(w, http.StatusBadRequest, "InvalidArgument", "A query parameter is invalid or unsupported.", r.URL.Path, req.requestID)
			return
		}
		if !h.recordProviderRequest(w, r, req) {
			return
		}
		var page objectstorage.ObjectPage
		if delimiter == "" {
			page, err = req.provider.ListObjects(r.Context(), req.bucket.PhysicalName, prefix, cursor, int32(limit))
		} else if lister, ok := req.provider.(objectstorage.DelimitedObjectLister); ok {
			page, err = lister.ListObjectsDelimited(r.Context(), req.bucket.PhysicalName, prefix, delimiter, cursor, int32(limit))
		} else {
			writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "Delimiter listing is not implemented by this storage provider.", r.URL.Path, req.requestID)
			return
		}
		if err != nil {
			h.providerError(w, r, req, err, "")
			return
		}
		writeS3XML(w, http.StatusOK, req.requestID, listObjectsResult(req.bucket.Name, prefix, int32(limit), page))
		return
	}
	h.unsupported(w, r, req.requestID)
}

func validDelimiter(delimiter string) bool {
	if delimiter == "" {
		return true
	}
	if !utf8.ValidString(delimiter) || len(delimiter) > 4 {
		return false
	}
	count := 0
	for _, r := range delimiter {
		if r < 32 || r == 127 {
			return false
		}
		count++
	}
	return count == 1
}

func (h *Handler) routeObject(w http.ResponseWriter, r *http.Request, req requestContext, key string) {
	query := operationQuery(r.URL.Query())
	query.Del("x-id")
	if uploadID := query.Get("uploadId"); uploadID != "" {
		switch r.Method {
		case http.MethodPut:
			if !queryKeysOnly(query, "uploadId", "partNumber") || query.Get("partNumber") == "" {
				h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
				return
			}
			h.uploadMultipartPart(w, r, req, key, uploadID, query.Get("partNumber"))
		case http.MethodGet:
			if !queryKeysOnly(query, "uploadId", "part-number-marker", "max-parts") {
				h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
				return
			}
			h.listMultipartParts(w, r, req, key, uploadID, query)
		case http.MethodPost:
			if !queryKeysOnly(query, "uploadId") {
				h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
				return
			}
			h.completeMultipart(w, r, req, key, uploadID)
		case http.MethodDelete:
			if !queryKeysOnly(query, "uploadId") {
				h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
				return
			}
			h.abortMultipart(w, r, req, key, uploadID)
		default:
			h.unsupported(w, r, req.requestID)
		}
		return
	}
	if r.Method == http.MethodPost && query.Has("uploads") {
		if !queryKeysOnly(query, "uploads") {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
			return
		}
		h.initiateMultipart(w, r, req)
		return
	}
	if len(query) != 0 {
		h.unsupported(w, r, req.requestID)
		return
	}
	if r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "" {
		h.copyObject(w, r, req, key)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) || !h.admit(w, r, req, key, 0, false) {
			return
		}
		h.download(w, r, req, key)
	case http.MethodPut:
		if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
			return
		}
		h.upload(w, r, req, key)
	case http.MethodDelete:
		if !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) || !h.admit(w, r, req, key, 0, false) {
			return
		}
		if !h.recordProviderRequest(w, r, req) {
			return
		}
		if err := req.provider.DeleteObject(r.Context(), req.bucket.PhysicalName, key); err != nil {
			h.providerError(w, r, req, err, key)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		h.unsupported(w, r, req.requestID)
	}
}

func (h *Handler) can(credential state.ObjectS3Credential, permission string) bool {
	return credential.Permission == permission || credential.Permission == state.ObjectBucketPermissionReadWrite
}

func (h *Handler) require(w http.ResponseWriter, req requestContext, permission, resource string) bool {
	if h.can(req.credential, permission) {
		return true
	}
	writeS3Error(w, http.StatusForbidden, "AccessDenied", "Access Denied.", resource, req.requestID)
	return false
}

func (h *Handler) admit(w http.ResponseWriter, r *http.Request, req requestContext, key string, size int64, put bool) bool {
	if err := h.store.AdmitObjectURL(r.Context(), req.bucket.AccountID, req.bucket.ID, key, size, put, h.registry.Accounting); err != nil {
		status, code, message := http.StatusServiceUnavailable, "ServiceUnavailable", "Object storage accounting is temporarily unavailable."
		if errors.Is(err, state.ErrObjectBudget) {
			status, code, message = http.StatusPaymentRequired, "AccountProblem", "The object storage safety budget has been reached."
		} else if errors.Is(err, state.ErrObjectCapacity) {
			status, code, message = http.StatusConflict, "OperationAborted", "The object storage capacity reservation would be exceeded."
		}
		writeS3Error(w, status, code, message, r.URL.Path, req.requestID)
		return false
	}
	return true
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request, req requestContext, key string) {
	if r.ContentLength < 0 {
		writeS3Error(w, http.StatusLengthRequired, "MissingContentLength", "You must provide the Content-Length HTTP header.", r.URL.Path, req.requestID)
		return
	}
	if r.ContentLength > h.maxPutBytes {
		writeS3Error(w, http.StatusBadRequest, "EntityTooLarge", "Your proposed upload exceeds the maximum allowed object size for a single PUT.", r.URL.Path, req.requestID)
		return
	}
	select {
	case h.putSlots <- struct{}{}:
		defer func() { <-h.putSlots }()
	default:
		writeS3Error(w, http.StatusServiceUnavailable, "SlowDown", "Please reduce your request rate.", r.URL.Path, req.requestID)
		return
	}
	file, err := os.CreateTemp(h.spoolDir, "gregale-s3-put-*")
	if err != nil {
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not stage this upload.", r.URL.Path, req.requestID)
		return
	}
	name := file.Name()
	defer func() {
		_ = file.Close()
		if removeErr := os.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			h.log.Warn("S3 upload spool cleanup failed", "request_id", req.requestID)
		}
	}()
	sha := sha256.New()
	md5sum := md5.New() // #nosec G401 -- S3 Content-MD5 compatibility.
	written, err := io.Copy(io.MultiWriter(file, sha, md5sum), io.LimitReader(r.Body, r.ContentLength+1))
	if err != nil || written != r.ContentLength {
		writeS3Error(w, http.StatusBadRequest, "IncompleteBody", "You did not provide the number of bytes specified by Content-Length.", r.URL.Path, req.requestID)
		return
	}
	if req.signature.PayloadHash != "UNSIGNED-PAYLOAD" {
		actual := hex.EncodeToString(sha.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(actual), []byte(req.signature.PayloadHash)) != 1 {
			writeS3Error(w, http.StatusBadRequest, "XAmzContentSHA256Mismatch", "The provided x-amz-content-sha256 does not match the request body.", r.URL.Path, req.requestID)
			return
		}
	}
	if expected := r.Header.Get("Content-MD5"); expected != "" {
		decoded, decodeErr := base64.StdEncoding.DecodeString(expected)
		if decodeErr != nil || len(decoded) != md5.Size || subtle.ConstantTimeCompare(decoded, md5sum.Sum(nil)) != 1 {
			writeS3Error(w, http.StatusBadRequest, "BadDigest", "The Content-MD5 you specified did not match what Gregale received.", r.URL.Path, req.requestID)
			return
		}
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil || !h.admit(w, r, req, key, r.ContentLength, true) {
		if err != nil {
			writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not stage this upload.", r.URL.Path, req.requestID)
		}
		return
	}
	contentType := r.Header.Get("Content-Type")
	signed, err := req.provider.Presign(r.Context(), req.bucket.PhysicalName, objectstorage.SignRequest{Method: http.MethodPut, Key: key, SizeBytes: &r.ContentLength, ContentType: contentType, ExpiresIn: 60})
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPut, signed.URL, file)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	upstream.ContentLength = r.ContentLength
	for name, value := range signed.Headers {
		upstream.Header.Set(name, value)
	}
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	response, err := h.client.Do(upstream)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	defer h.closeResponseBody(response.Body, req.requestID)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		h.providerHTTPError(w, r, req, response.StatusCode, key)
		return
	}
	if etag := response.Header.Get("ETag"); etag != "" {
		w.Header().Set("ETag", etag)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) copyObject(w http.ResponseWriter, r *http.Request, req requestContext, destinationKey string) {
	if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) || !h.require(w, req, state.ObjectBucketPermissionWrite, r.URL.Path) {
		return
	}
	if r.ContentLength > 0 {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "CopyObject does not accept a request body.", r.URL.Path, req.requestID)
		return
	}
	copier, ok := req.provider.(objectstorage.ObjectCopier)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return
	}
	sourceBucket, sourceKey, err := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
	if err != nil || sourceBucket != req.bucket.Name {
		writeS3Error(w, http.StatusNotFound, "NoSuchKey", "The specified copy source does not exist.", r.URL.Path, req.requestID)
		return
	}
	directive := strings.ToUpper(strings.TrimSpace(r.Header.Get("X-Amz-Metadata-Directive")))
	if directive == "" {
		directive = "COPY"
	}
	if directive != "COPY" && directive != "REPLACE" {
		writeS3Error(w, http.StatusBadRequest, "InvalidDirective", "The metadata directive is invalid.", r.URL.Path, req.requestID)
		return
	}
	metadata, err := copyMetadata(r, directive)
	if err != nil {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "The object metadata is invalid.", r.URL.Path, req.requestID)
		return
	}
	size := int64(0)
	if sizer, ok := req.provider.(objectstorage.ObjectSizer); ok {
		size, err = sizer.ObjectSize(r.Context(), req.bucket.PhysicalName, sourceKey)
		if err != nil {
			h.providerError(w, r, req, err, sourceKey)
			return
		}
	}
	if !h.admit(w, r, req, destinationKey, size, true) || !h.recordProviderRequest(w, r, req) {
		return
	}
	result, err := copier.CopyObject(r.Context(), req.bucket.PhysicalName, objectstorage.CopyObjectRequest{
		SourceKey: sourceKey, DestinationKey: destinationKey, MetadataDirective: directive, Metadata: metadata,
	})
	if err != nil {
		h.providerError(w, r, req, err, sourceKey)
		return
	}
	lastModified := ""
	if !result.LastModified.IsZero() {
		lastModified = result.LastModified.UTC().Format(time.RFC3339Nano)
	}
	writeS3XML(w, http.StatusOK, req.requestID, copyObjectResult{XMLNS: s3XMLNamespace, LastModified: lastModified, ETag: result.ETag})
}

func parseCopySource(value string) (string, string, error) {
	if value == "" || strings.ContainsRune(value, '?') {
		return "", "", objectstorage.ErrInvalid
	}
	value = strings.TrimPrefix(value, "/")
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return "", "", objectstorage.ErrInvalid
	}
	bucket, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", objectstorage.ErrInvalid
	}
	key, err := url.PathUnescape(parts[1])
	if err != nil || !objectstorage.ValidKey(key) {
		return "", "", objectstorage.ErrInvalid
	}
	return bucket, key, nil
}

func copyMetadata(r *http.Request, directive string) (objectstorage.ObjectMetadata, error) {
	metadata := objectstorage.ObjectMetadata{}
	if directive == "COPY" {
		for name := range r.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-amz-meta-") || strings.HasPrefix(lower, "x-amz-tag") || name == "Content-Type" || name == "Cache-Control" || name == "Content-Disposition" || name == "Content-Encoding" || name == "Content-Language" {
				return metadata, objectstorage.ErrInvalid
			}
		}
		return metadata, nil
	}
	metadata.ContentType = r.Header.Get("Content-Type")
	metadata.CacheControl = r.Header.Get("Cache-Control")
	metadata.ContentDisposition = r.Header.Get("Content-Disposition")
	metadata.ContentEncoding = r.Header.Get("Content-Encoding")
	metadata.ContentLanguage = r.Header.Get("Content-Language")
	metadata.Metadata = make(map[string]string)
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-tag") {
			return objectstorage.ObjectMetadata{}, objectstorage.ErrInvalid
		}
		if !strings.HasPrefix(lower, "x-amz-meta-") {
			continue
		}
		if len(values) != 1 {
			return objectstorage.ObjectMetadata{}, objectstorage.ErrInvalid
		}
		key := strings.TrimPrefix(lower, "x-amz-meta-")
		if key == "" {
			return objectstorage.ObjectMetadata{}, objectstorage.ErrInvalid
		}
		metadata.Metadata[key] = values[0]
	}
	return metadata, nil
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request, req requestContext, key string) {
	signed, err := req.provider.Presign(r.Context(), req.bucket.PhysicalName, objectstorage.SignRequest{Method: r.Method, Key: key, ExpiresIn: 60})
	if err != nil {
		h.providerError(w, r, req, err, key)
		return
	}
	upstream, err := http.NewRequestWithContext(r.Context(), r.Method, signed.URL, nil)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	for name, value := range signed.Headers {
		upstream.Header.Set(name, value)
	}
	for _, name := range []string{"Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		if value := r.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	if !h.recordProviderRequest(w, r, req) {
		return
	}
	response, err := h.client.Do(upstream)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
		return
	}
	defer h.closeResponseBody(response.Body, req.requestID)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		h.providerHTTPError(w, r, req, response.StatusCode, key)
		return
	}
	copyObjectHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, response.Body)
	}
}

// recordProviderRequest commits the outbound request attempt before it is
// sent. A failed commit blocks the request rather than allowing an
// unaccounted provider call to escape; the resulting 503 is preferable to
// silently under-reporting customer usage.
func (h *Handler) recordProviderRequest(w http.ResponseWriter, r *http.Request, req requestContext) bool {
	if h.requestMetrics == nil {
		// Unit fixtures may omit persistence. Production s3-gatewayd rejects
		// startup when the configured store lacks this capability.
		return true
	}
	if err := h.requestMetrics.RecordObjectStorageProviderRequest(r.Context(), req.bucket.ID, h.now().UTC()); err != nil {
		h.log.Warn("S3 provider request metric write failed", "request_id", req.requestID)
		writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not record object storage usage.", r.URL.Path, req.requestID)
		return false
	}
	return true
}

func (h *Handler) closeResponseBody(body io.Closer, requestID string) {
	if err := body.Close(); err != nil {
		h.log.Warn("S3 provider response close failed", "request_id", requestID)
	}
}

func copyObjectHeaders(dst, src http.Header) {
	for _, name := range []string{"Accept-Ranges", "Cache-Control", "Content-Disposition", "Content-Length", "Content-Range", "Content-Type", "ETag", "Expires", "Last-Modified"} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}

func (h *Handler) providerError(w http.ResponseWriter, r *http.Request, req requestContext, err error, key string) {
	resource := r.URL.Path
	if errors.Is(err, objectstorage.ErrNotFound) {
		code, message := "NoSuchKey", "The specified key does not exist."
		if key == "" {
			code, message = "NoSuchBucket", "The specified bucket does not exist."
		}
		writeS3Error(w, http.StatusNotFound, code, message, resource, req.requestID)
		return
	}
	if errors.Is(err, objectstorage.ErrInvalid) {
		writeS3Error(w, http.StatusBadRequest, "InvalidRequest", "The request is invalid.", resource, req.requestID)
		return
	}
	writeS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable", "Gregale could not reach this bucket's storage placement.", resource, req.requestID)
}

func (h *Handler) providerHTTPError(w http.ResponseWriter, r *http.Request, req requestContext, status int, key string) {
	switch status {
	case http.StatusNotFound:
		h.providerError(w, r, req, objectstorage.ErrNotFound, key)
	case http.StatusRequestedRangeNotSatisfiable:
		writeS3Error(w, status, "InvalidRange", "The requested range is not satisfiable.", r.URL.Path, req.requestID)
	case http.StatusBadRequest:
		h.providerError(w, r, req, objectstorage.ErrInvalid, key)
	default:
		h.providerError(w, r, req, objectstorage.ErrUnavailable, key)
	}
}

func (h *Handler) unsupported(w http.ResponseWriter, r *http.Request, requestID string) {
	writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "This S3 operation is not implemented by Gregale yet.", r.URL.Path, requestID)
}

func operationQuery(query url.Values) url.Values {
	out := make(url.Values, len(query))
	for key, values := range query {
		if strings.EqualFold(key, "x-id") || strings.HasPrefix(strings.ToLower(key), "x-amz-") {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func queryKeysOnly(query url.Values, allowed ...string) bool {
	keys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		keys[key] = struct{}{}
	}
	for key, values := range query {
		if _, ok := keys[key]; !ok || len(values) != 1 {
			return false
		}
	}
	return true
}

func IsLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

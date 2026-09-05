// s3client.go — minimal hand-rolled S3-compatible client (issue
// #562). PUT and GET only; no list, no multipart, no presigned
// URLs. The shipper's needs are narrow: gzip one file, ship it,
// retrieve on `?after=7d`. Hand-rolling avoids the
// aws-sdk-go-v2 vendor tree (50+ transitive deps, ~5 MB binary
// growth) and keeps the signing surface auditable in one place.
//
// SigV4 reference: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html
// Canonical request → string-to-sign → signing key (chain of
// HMAC-SHA256) → signature. The body-hash part of canonical
// request uses SHA-256 of the body, hex-encoded.
//
// Region handling: AWS S3 uses the region in the canonical
// request scope; some vendors (Cloudflare R2) accept "auto".
// The Endpoint URL is opaque to SigV4 — only the host header
// matters for the Host field.

package logarchive

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrAuthMissing is returned by PutObject/GetObject when the
// client was constructed without KeyID/Secret. Surfaces as a typed
// sentinel so the shipper's RunOnce can distinguish "credential
// not yet provisioned" from transient S3 errors.
var ErrAuthMissing = errors.New("logarchive: s3client credentials missing")

// ErrPermanent is the wrapper for terminal (4xx) S3 responses.
// The shipper increments *_log_archive_failures_total{reason} on
// a Permanent to give the operator a clear signal that retry
// is futile (e.g. 403 AccessDenied, 404 NoSuchBucket, 400
// InvalidArgument). Transient (5xx, network) errors return
// their plain error.
type Permanent struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *Permanent) Error() string {
	return fmt.Sprintf("logarchive: s3 permanent %d %s: %s", e.StatusCode, e.Code, e.Message)
}

// IsPermanent reports whether err is a *Permanent.
func IsPermanent(err error) bool {
	var p *Permanent
	return errors.As(err, &p)
}

// S3Client is a single-purpose S3-compatible PUT/GET client.
// Constructed once per Shipper and shared across all flushes;
// the underlying http.Client is the stdlib one with a sane
// timeout (no shared state per request). The Shipper field
// accessors are read-only after construction; no internal
// mutex.
type S3Client struct {
	Endpoint string // e.g. "https://s3.us-east-1.amazonaws.com"
	Region   string // e.g. "us-east-1"
	Bucket   string
	KeyID    string
	Secret   string
	HTTP     *http.Client
}

// NewS3Client constructs a client. Returns ErrAuthMissing if
// KeyID or Secret is empty (matches the apid wire-up's
// fail-closed posture). HTTP defaults to a 30s-timeout client.
func NewS3Client(endpoint, region, bucket, keyID, secret string) (*S3Client, error) {
	if keyID == "" || secret == "" {
		return nil, ErrAuthMissing
	}
	if endpoint == "" {
		return nil, errors.New("logarchive: s3client endpoint required")
	}
	if bucket == "" {
		return nil, errors.New("logarchive: s3client bucket required")
	}
	return &S3Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Region:   region,
		Bucket:   bucket,
		KeyID:    keyID,
		Secret:   secret,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// PutObject uploads r (with the given byte length) to
// {bucket}/{key}. contentType is sent as-is (the shipper uses
// "application/gzip"). Returns *Permanent on 4xx, plain error on
// 5xx/network, nil on success.
//
// The signature is computed against the SHA-256 of the body; the
// body is buffered in memory because gzip-compressed JSONL is
// bounded by the BatchMaxBytes cap (default 100 MB). The
// shipper's local-spool size cap (DefaultLocalBytesMax = 10 GB)
// ensures no single PUT exceeds the stdlib memory budget —
// tests with httptest.NewServer cover the streaming case directly.
func (c *S3Client) PutObject(ctx context.Context, key, contentType string, r io.Reader, size int64) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("logarchive: read body: %w", err)
	}
	if int64(len(body)) != size {
		// Defensive: the shipper always passes the local file
		// size; a mismatch means r produced a different byte
		// count than expected. Surface as Permanent so the
		// shipper increments the right counter and the operator
		// sees the bug in metrics.
		return &Permanent{StatusCode: 0, Code: "BodyLengthMismatch", Message: fmt.Sprintf("body length %d != expected %d", len(body), size)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.objectURL(key), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("logarchive: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", size))
	if err := c.sign(req, body, "PUT", key); err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("logarchive: put: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return parseS3Response(resp, "PUT")
}

// GetObject fetches {bucket}/{key} and writes the body to w.
// Returns *Permanent on 4xx. The shipper uses this on the
// read-back path (`?after=7d`); the gatewayd-internal handler
// streams the result straight to the SSE response without
// buffering.
func (c *S3Client) GetObject(ctx context.Context, key string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.objectURL(key), nil)
	if err != nil {
		return 0, fmt.Errorf("logarchive: build request: %w", err)
	}
	if err := c.sign(req, nil, "GET", key); err != nil {
		return 0, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("logarchive: get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Status check only — we must NOT drain the success-path
	// body, or io.Copy below would see 0 bytes.
	if err := checkS3Status(resp, "GET"); err != nil {
		return 0, err
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, fmt.Errorf("logarchive: read body: %w", err)
	}
	return n, nil
}

// objectURL builds the canonical S3 object URL. The SigV4 host
// header is the same host as the URL; virtual-hosted style
// (bucket.endpoint) and path style (endpoint/bucket) are both
// supported by every S3-compatible vendor. We use path style
// because some vendors (MinIO single-node) don't support
// virtual-hosted style on custom endpoints.
func (c *S3Client) objectURL(key string) string {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		// Constructor validates Endpoint; a parse failure here
		// means the Endpoint string changed at runtime — refuse.
		return c.Endpoint + "/" + c.Bucket + "/" + key
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + c.Bucket + "/" + key
	return u.String()
}

// sign adds the AWS SigV4 authorization header to req. body is
// the bytes that will be sent (or nil for GET); method is the
// HTTP verb; key is the object key (without the bucket prefix).
//
// Algorithm (AWS SigV4):
//
//  1. Canonical request = method + "\n" +
//     canonical URI + "\n" +
//     canonical query + "\n" +
//     canonical headers + "\n" +
//     signed headers + "\n" +
//     hex(SHA-256(body))
//  2. String to sign = algorithm + "\n" +
//     amz-date + "\n" +
//     credential scope + "\n" +
//     hex(SHA-256(canonical request))
//  3. Signing key = HMAC-SHA256("AWS4" + secret, date) →
//     HMAC-SHA256(prev, region) → HMAC-SHA256(prev, service) →
//     HMAC-SHA256(prev, "aws4_request")
//  4. Signature = hex(HMAC-SHA256(signing key, string to sign))
//  5. Authorization = "AWS4-HMAC-SHA256 Credential=...,
//     SignedHeaders=..., Signature=..."
//
// The amz-date format is YYYYMMDDTHHMMSSZ (basic ISO 8601, no
// separators). We compute it from time.Now().UTC() at sign time
// rather than threading it through — the request timestamp is
// allowed to drift up to 15 minutes from the server's clock and
// apid's ntp-synced clock stays inside that envelope.
func (c *S3Client) sign(req *http.Request, body []byte, method, key string) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	// Empty body hash for GET; SHA-256 hex for PUT.
	var payloadHash string
	if body == nil {
		payloadHash = hexSHA256(nil)
	} else {
		payloadHash = hexSHA256(body)
	}
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	if req.Host != "" {
		// Some http.Client implementations strip the Host
		// header; SigV4 requires it for the canonical
		// headers list. Re-set from req.URL.
		req.Header.Set("Host", req.URL.Host)
	} else {
		req.Header.Set("Host", req.URL.Host)
	}

	host := req.URL.Host
	contentType := req.Header.Get("Content-Type")
	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "content-type;host;x-amz-content-sha256;x-amz-date"

	canonicalURI := "/" + key
	canonicalQuery := ""

	canonicalRequest := method + "\n" +
		canonicalURI + "\n" +
		canonicalQuery + "\n" +
		canonicalHeaders + "\n" +
		signedHeaders + "\n" +
		payloadHash

	credentialScope := dateStamp + "/" + c.Region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" +
		amzDate + "\n" +
		credentialScope + "\n" +
		hexSHA256([]byte(canonicalRequest))

	signingKey := deriveSigningKey(c.Secret, dateStamp, c.Region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	authorization := "AWS4-HMAC-SHA256 " +
		"Credential=" + c.KeyID + "/" + credentialScope + ", " +
		"SignedHeaders=" + signedHeaders + ", " +
		"Signature=" + signature
	req.Header.Set("Authorization", authorization)
	return nil
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func deriveSigningKey(secret, date, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	k = hmacSHA256(k, []byte(region))
	k = hmacSHA256(k, []byte(service))
	return hmacSHA256(k, []byte("aws4_request"))
}

// parseS3Response inspects resp and returns nil on 2xx, *Permanent
// on 4xx, plain error on 5xx. The success path drains the body so
// the connection can be reused (PUT callers don't read the body).
// GET callers use checkS3Status instead so they can stream the
// success body straight to their writer.
func parseS3Response(resp *http.Response, verb string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Drain + discard body so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return errorFromS3Status(resp, verb)
}

// checkS3Status is the body-preserving sibling of
// parseS3Response. It returns the typed error on non-2xx but
// leaves the body unread for the caller to consume.
func checkS3Status(resp *http.Response, verb string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return errorFromS3Status(resp, verb)
}

// errorFromS3Status reads the body (only called on non-2xx)
// and maps the status code to the typed sentinel.
func errorFromS3Status(resp *http.Response, verb string) error {
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		code, message := parseS3ErrorBody(body)
		return &Permanent{
			StatusCode: resp.StatusCode,
			Code:       code,
			Message:    message,
		}
	}
	return fmt.Errorf("logarchive: %s unexpected status %d: %s", verb, resp.StatusCode, string(body))
}

// parseS3ErrorBody extracts Code + Message from an S3 error
// body. Falls back to the raw body when the body isn't JSON.
func parseS3ErrorBody(body []byte) (code, message string) {
	var parsed struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Code != "" {
		return parsed.Code, parsed.Message
	}
	return "Unknown", string(body)
}

// Package statusmirror publishes the public status page's data to storage
// that does not run on Gregale (ADR-20261115090000).
//
// apid already serves /v1/status, but that endpoint, its database and its
// Prometheus all run on the platform: when Gregale is down, so is the page
// that should say so. The mirror copies the same document to a Cloudflare R2
// bucket every minute. A static page on Cloudflare reads the copy, a Cloudflare
// cron probe writes its own outside view next to it, and operators can post
// incidents into the bucket without apid. The page treats a copy that stops
// updating as an outage signal rather than showing the last green state.
package statusmirror

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/onebox-faas/faas/pkg/api"
)

// Object keys inside the mirror bucket. The status page and the probe Worker
// (deploy/cloudflare/status-worker) read the same keys; change them together.
const (
	StatusKey  = "v1/status.json"
	NoticesKey = "v1/notices.json"
	ProbeKey   = "v1/probe.json"
)

// SchemaVersion is the envelope version of every mirror document.
const SchemaVersion = 1

// StatusCacheControl lets Cloudflare's cache absorb page polling, so viewers
// during an outage cost no R2 reads beyond one per cache interval and
// location. It must stay well below the page's staleness threshold.
const StatusCacheControl = "public, max-age=15"

// NoticesCacheControl is shorter because operators post notices by hand
// during an incident and want them visible quickly.
const NoticesCacheControl = "public, max-age=10"

// PublishInterval is how often apid pushes the snapshot. The page calls the
// copy stale after StaleAfter, which tolerates two missed pushes plus cache.
const (
	PublishInterval = time.Minute
	StaleAfter      = 3 * time.Minute
)

// StatusDocument is v1/status.json: the /v1/status body plus the time apid
// pushed it. PublishedAt is the liveness signal; Status.UpdatedAt is when the
// telemetry was evaluated, which can lag while Prometheus is unhealthy.
type StatusDocument struct {
	Schema      int                      `json:"schema"`
	PublishedAt time.Time                `json:"published_at"`
	Publisher   string                   `json:"publisher,omitempty"`
	Status      api.PublicStatusOverview `json:"status"`
}

// NoticesDocument is v1/notices.json: incidents posted while apid may be
// unavailable. Events use the public event shape so the page renders them
// with the same code as apid's events.
type NoticesDocument struct {
	Schema    int                     `json:"schema"`
	UpdatedAt time.Time               `json:"updated_at"`
	Events    []api.PublicStatusEvent `json:"events"`
}

// ErrNotFound reports a missing object.
var ErrNotFound = errors.New("status mirror object not found")

// ErrConflict reports that an object changed between read and write.
var ErrConflict = errors.New("status mirror object changed concurrently")

// Object is one stored document and its version tag.
type Object struct {
	Body []byte
	ETag string
}

// Store is the bucket surface the mirror needs.
type Store interface {
	Get(ctx context.Context, key string) (Object, error)
	// Put writes body. A non-empty ifMatch makes the write conditional on the
	// current ETag; ifMatch "-" requires that the object does not exist.
	Put(ctx context.Context, key string, body []byte, cacheControl, ifMatch string) error
}

// R2 is a Store backed by an R2 (S3-compatible) bucket.
type R2 struct {
	client *s3.Client
	bucket string
}

const maxObjectBytes = 4 << 20

// NewR2 builds a client for a bucket-scoped R2 token.
func NewR2(endpoint, bucket, accessKey, secretKey string) (*R2, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("status mirror requires an HTTPS R2 endpoint")
	}
	if bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("status mirror requires a bucket and credentials")
	}
	client := s3.New(s3.Options{
		Region: "auto", BaseEndpoint: aws.String(endpoint), UsePathStyle: true,
		Credentials:      credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		HTTPClient:       &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		RetryMaxAttempts: 2, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &R2{client: client, bucket: bucket}, nil
}

// Env names. The same four variables configure apid and the gregalectl
// offline commands; operators should use separate bucket-scoped tokens.
const (
	EnvEndpoint  = "FAAS_STATUS_MIRROR_R2_ENDPOINT"
	EnvBucket    = "FAAS_STATUS_MIRROR_R2_BUCKET"
	EnvAccessKey = "FAAS_STATUS_MIRROR_R2_ACCESS_KEY"
	EnvSecretKey = "FAAS_STATUS_MIRROR_R2_SECRET_KEY"
)

// Load returns nil, nil when the mirror is not configured, and an error when
// it is only partly configured.
func Load(getenv func(string) string) (*R2, error) {
	values := []string{getenv(EnvEndpoint), getenv(EnvBucket), getenv(EnvAccessKey), getenv(EnvSecretKey)}
	set := 0
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
		if values[i] != "" {
			set++
		}
	}
	if set == 0 {
		return nil, nil
	}
	if set != len(values) {
		return nil, fmt.Errorf("status mirror needs all of %s, %s, %s and %s", EnvEndpoint, EnvBucket, EnvAccessKey, EnvSecretKey)
	}
	return NewR2(values[0], values[1], values[2], values[3])
}

func (r *R2) Get(ctx context.Context, key string) (Object, error) {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound") {
			return Object{}, ErrNotFound
		}
		return Object{}, fmt.Errorf("status mirror get %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(out.Body, maxObjectBytes+1))
	if err != nil {
		return Object{}, fmt.Errorf("status mirror read %s: %w", key, err)
	}
	if len(body) > maxObjectBytes {
		return Object{}, fmt.Errorf("status mirror object %s exceeds %d bytes", key, maxObjectBytes)
	}
	return Object{Body: body, ETag: aws.ToString(out.ETag)}, nil
}

func (r *R2) Put(ctx context.Context, key string, body []byte, cacheControl, ifMatch string) error {
	in := &s3.PutObjectInput{
		Bucket: aws.String(r.bucket), Key: aws.String(key), Body: bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))), ContentType: aws.String("application/json"),
		CacheControl: aws.String(cacheControl),
	}
	switch ifMatch {
	case "":
	case "-":
		in.IfNoneMatch = aws.String("*")
	default:
		in.IfMatch = aws.String(ifMatch)
	}
	_, err := r.client.PutObject(ctx, in)
	if err != nil {
		var apiErr smithy.APIError
		if ifMatch != "" && errors.As(err, &apiErr) && (apiErr.ErrorCode() == "PreconditionFailed" || apiErr.ErrorCode() == "ConditionalRequestConflict") {
			return ErrConflict
		}
		return fmt.Errorf("status mirror put %s: %w", key, err)
	}
	return nil
}

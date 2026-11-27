package storagegrant

// adr: 20261117190000

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Signer presigns SigV4 URLs for one bucket. S3 conditions writes on
// ETags (If-None-Match: * creates, If-Match: <etag> replaces), and a large
// object is written as a multipart upload whose create, parts, complete and
// abort are each a grant. Generation-conditioned operations are GCS only.
type S3Signer struct {
	presign *s3.PresignClient
	bucket  string
	// The raw-signing parts, for the multipart requests the SDK cannot
	// presign.
	creds     aws.CredentialsProvider
	region    string
	endpoint  *url.URL
	pathStyle bool
	signer    *v4.Signer
}

// NewS3Signer presigns for bucket through client's credentials.
func NewS3Signer(client *s3.Client, bucket string) (*S3Signer, error) {
	if client == nil || bucket == "" {
		return nil, errors.New("storagegrant: S3 signer needs a client and a bucket")
	}
	opts := client.Options()
	if opts.BaseEndpoint == nil || *opts.BaseEndpoint == "" || opts.Credentials == nil || opts.Region == "" {
		return nil, errors.New("storagegrant: S3 signer needs a client with an endpoint, a region and credentials")
	}
	endpoint, err := url.Parse(strings.TrimSuffix(*opts.BaseEndpoint, "/"))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return nil, fmt.Errorf("storagegrant: S3 endpoint %q is not a URL", *opts.BaseEndpoint)
	}
	return &S3Signer{presign: s3.NewPresignClient(client), bucket: bucket, creds: opts.Credentials,
		region: opts.Region, endpoint: endpoint, pathStyle: opts.UsePathStyle, signer: v4.NewSigner()}, nil
}

// Sign implements Signer.
func (s *S3Signer) Sign(ctx context.Context, r Request, expires time.Time) (Grant, error) {
	if err := r.Validate(); err != nil {
		return Grant{}, err
	}
	ttl := time.Until(expires)
	if ttl <= 0 {
		return Grant{}, errors.New("storagegrant: grant already expired")
	}
	withTTL := s3.WithPresignExpires(ttl)
	bucket, key := aws.String(s.bucket), aws.String(r.Key)
	var (
		method, u string
		signed    http.Header
		err       error
	)
	switch r.Op {
	case OpGet:
		var p *v4.PresignedHTTPRequest
		p, err = s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: bucket, Key: key}, withTTL)
		if p != nil {
			method, u, signed = p.Method, p.URL, p.SignedHeader
		}
	case OpHead:
		var p *v4.PresignedHTTPRequest
		p, err = s.presign.PresignHeadObject(ctx, &s3.HeadObjectInput{Bucket: bucket, Key: key}, withTTL)
		if p != nil {
			method, u, signed = p.Method, p.URL, p.SignedHeader
		}
	case OpCreate, OpReplace:
		if r.Op == OpReplace && r.ETag == "" {
			return Grant{}, fmt.Errorf("%w: a generation-conditioned replace on S3", ErrUnsupported)
		}
		in := &s3.PutObjectInput{Bucket: bucket, Key: key, Metadata: r.Metadata}
		if r.Op == OpCreate {
			in.IfNoneMatch = aws.String("*")
		} else {
			in.IfMatch = aws.String(r.ETag)
		}
		if r.ContentType != "" {
			in.ContentType = aws.String(r.ContentType)
		}
		var p *v4.PresignedHTTPRequest
		p, err = s.presign.PresignPutObject(ctx, in, withTTL)
		if p != nil {
			method, u, signed = p.Method, p.URL, p.SignedHeader
		}
	case OpUploadPart:
		var p *v4.PresignedHTTPRequest
		p, err = s.presign.PresignUploadPart(ctx, &s3.UploadPartInput{Bucket: bucket, Key: key,
			UploadId: aws.String(r.UploadID), PartNumber: aws.Int32(r.PartNumber)}, withTTL)
		if p != nil {
			method, u, signed = p.Method, p.URL, p.SignedHeader
		}
	case OpCreateMultipart, OpCompleteMultipart, OpAbortMultipart:
		return s.signRaw(ctx, r, ttl, expires)
	default:
		return Grant{}, fmt.Errorf("%w: %s on S3", ErrUnsupported, r.Op)
	}
	if err != nil {
		return Grant{}, fmt.Errorf("storagegrant: presign %s %q: %w", r.Op, r.Key, err)
	}
	return Grant{Method: method, URL: u, Headers: flatten(signed), Expires: expires}, nil
}

// signRaw presigns the multipart requests the SDK's presign client does
// not offer, with the same SigV4 query signing.
func (s *S3Signer) signRaw(ctx context.Context, r Request, ttl time.Duration, expires time.Time) (Grant, error) {
	u := s.objectURL(r.Key)
	q := url.Values{}
	headers := map[string]string{}
	switch r.Op {
	case OpCreateMultipart:
		q.Set("uploads", "")
		for k, v := range r.Metadata {
			headers["x-amz-meta-"+strings.ToLower(k)] = v
		}
		if r.ContentType != "" {
			headers["content-type"] = r.ContentType
		}
	case OpCompleteMultipart:
		q.Set("uploadId", r.UploadID)
		if r.ETag == "" {
			headers["if-none-match"] = "*"
		} else {
			headers["if-match"] = r.ETag
		}
	case OpAbortMultipart:
		q.Set("uploadId", r.UploadID)
	}
	secs := int64(ttl / time.Second)
	if secs < 1 {
		secs = 1
	}
	q.Set("X-Amz-Expires", strconv.FormatInt(secs, 10))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method(r.Op), u.String(), nil)
	if err != nil {
		return Grant{}, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	creds, err := s.creds.Retrieve(ctx)
	if err != nil {
		return Grant{}, fmt.Errorf("storagegrant: S3 credentials: %w", err)
	}
	signedURL, signed, err := s.signer.PresignHTTP(ctx, creds, req, "UNSIGNED-PAYLOAD", "s3", s.region, time.Now().UTC())
	if err != nil {
		return Grant{}, fmt.Errorf("storagegrant: sign %s %q: %w", r.Op, r.Key, err)
	}
	return Grant{Method: req.Method, URL: signedURL, Headers: flatten(signed), Expires: expires}, nil
}

// objectURL is the object's URL, path style or virtual-hosted as the
// client addresses the bucket.
func (s *S3Signer) objectURL(key string) *url.URL {
	u := *s.endpoint
	escaped := (&url.URL{Path: "/" + key}).EscapedPath()
	if s.pathStyle {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + s.bucket + "/" + key
		u.RawPath = strings.TrimSuffix(s.endpoint.EscapedPath(), "/") + "/" + url.PathEscape(s.bucket) + escaped
	} else {
		u.Host = s.bucket + "." + u.Host
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + key
		u.RawPath = strings.TrimSuffix(s.endpoint.EscapedPath(), "/") + escaped
	}
	return &u
}

// flatten keeps the headers the node must send; Host is implied by the URL.
func flatten(h map[string][]string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		if strings.EqualFold(k, "host") {
			continue
		}
		out[strings.ToLower(k)] = strings.Join(v, ",")
	}
	return out
}

package storagegrant

// adr: 20261117190000

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Signer presigns SigV4 URLs for one bucket. S3 conditions writes on
// ETags and versions, not generations, so it signs reads and create-only
// writes (If-None-Match: *); generation-conditioned operations are
// ErrUnsupported until an S3 artifact backend defines their equivalent.
type S3Signer struct {
	presign *s3.PresignClient
	bucket  string
}

// NewS3Signer presigns for bucket through client's credentials.
func NewS3Signer(client *s3.Client, bucket string) (*S3Signer, error) {
	if client == nil || bucket == "" {
		return nil, errors.New("storagegrant: S3 signer needs a client and a bucket")
	}
	return &S3Signer{presign: s3.NewPresignClient(client), bucket: bucket}, nil
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
	switch r.Op {
	case OpGet:
		req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(r.Key)}, withTTL)
		if err != nil {
			return Grant{}, fmt.Errorf("storagegrant: presign get %q: %w", r.Key, err)
		}
		return Grant{Method: req.Method, URL: req.URL, Headers: flatten(req.SignedHeader), Expires: expires}, nil
	case OpCreate:
		in := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(r.Key), IfNoneMatch: aws.String("*"), Metadata: r.Metadata}
		if r.ContentType != "" {
			in.ContentType = aws.String(r.ContentType)
		}
		req, err := s.presign.PresignPutObject(ctx, in, withTTL)
		if err != nil {
			return Grant{}, fmt.Errorf("storagegrant: presign create %q: %w", r.Key, err)
		}
		return Grant{Method: req.Method, URL: req.URL, Headers: flatten(req.SignedHeader), Expires: expires}, nil
	}
	return Grant{}, fmt.Errorf("%w: %s on S3", ErrUnsupported, r.Op)
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

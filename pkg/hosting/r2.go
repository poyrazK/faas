package hosting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/onebox-faas/faas/pkg/api"
)

type ArtifactStore interface {
	Upload(context.Context, string, string, api.HostingFile) (api.HostingUpload, error)
	Publish(context.Context, string, string, api.HostingManifest) error
}

func (b *R2) Remove(ctx context.Context, site, release string, manifest api.HostingManifest) error {
	if ValidateManifest(manifest) != nil {
		return ErrInvalidArtifact
	}
	for _, file := range manifest.Files {
		for _, key := range []string{stagingKey(site, release, file.Path), ReleasePrefix(site, release) + "files/" + file.Path} {
			if _, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)}); err != nil {
				return err
			}
		}
	}
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(ReleasePrefix(site, release) + "manifest.json")})
	return err
}

// R2 uses a private, operator-provisioned bucket. It never creates a public
// bucket or gives a client credentials for the published namespace.
type R2 struct {
	client        *s3.Client
	signer        *s3.PresignClient
	bucket        string
	id            string
	uploadBaseURL string
	slots         chan struct{}
}

func NewR2(endpoint, bucket, accessKey, secretKey string) (*R2, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("hosting requires an HTTPS R2 endpoint, private bucket, and credentials")
	}
	client := s3.New(s3.Options{
		Region: "auto", BaseEndpoint: aws.String(endpoint), UsePathStyle: true,
		Credentials:      credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		HTTPClient:       &http.Client{Timeout: api.HostingStorageTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		RetryMaxAttempts: 2, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	digest := sha256.Sum256([]byte(endpoint + "\n" + bucket))
	return &R2{client: client, signer: s3.NewPresignClient(client), bucket: bucket, id: hex.EncodeToString(digest[:]), uploadBaseURL: strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(bucket), slots: make(chan struct{}, api.HostingPublishConcurrency)}, nil
}

func (b *R2) ID() string { return b.id }

func (b *R2) UploadBaseURL() string { return b.uploadBaseURL }

func (b *R2) Upload(ctx context.Context, site, release string, f api.HostingFile) (api.HostingUpload, error) {
	if !ValidPath(f.Path) || f.Size < 0 || f.Size > api.HostingFileMaxBytes {
		return api.HostingUpload{}, ErrInvalidArtifact
	}
	r, err := b.signer.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(stagingKey(site, release, f.Path)), ContentLength: aws.Int64(f.Size), ContentType: aws.String(f.ContentType)}, s3.WithPresignExpires(api.HostingUploadTTL))
	if err != nil {
		return api.HostingUpload{}, fmt.Errorf("hosting upload grant: %w", err)
	}
	return api.HostingUpload{Path: f.Path, URL: r.URL, Headers: r.SignedHeader}, nil
}

func (b *R2) Publish(ctx context.Context, site, release string, manifest api.HostingManifest) error {
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, f := range manifest.Files {
		if err := b.publishFile(ctx, site, release, f); err != nil {
			return err
		}
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return b.putImmutable(ctx, ReleasePrefix(site, release)+"manifest.json", body, "application/json")
}

func (b *R2) publishFile(ctx context.Context, site, release string, f api.HostingFile) error {
	body, err := b.read(ctx, stagingKey(site, release, f.Path), f.Size)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	if int64(len(body)) != f.Size || hex.EncodeToString(digest[:]) != f.SHA256 {
		return ErrInvalidArtifact
	}
	return b.putImmutable(ctx, ReleasePrefix(site, release)+"files/"+f.Path, body, f.ContentType)
}

func (b *R2) read(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	r, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("hosting read artifact: %w", err)
	}
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		return nil, errors.Join(ErrInvalidArtifact, err)
	}
	return body, nil
}

// Retrying publication is safe: an existing key must contain the exact bytes.
func (b *R2) putImmutable(ctx context.Context, key string, body []byte, contentType string) error {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body))), ContentType: aws.String(contentType), IfNoneMatch: aws.String("*")})
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "PreconditionFailed" {
		return err
	}
	existing, readErr := b.read(ctx, key, int64(len(body)))
	if readErr != nil || !bytes.Equal(existing, body) {
		return errors.Join(ErrInvalidArtifact, readErr)
	}
	return nil
}

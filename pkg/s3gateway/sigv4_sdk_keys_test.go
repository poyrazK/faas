package s3gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestSigV4_SDKKeysNeedingEscaping drives the gateway with the real AWS S3
// SDK. S3 signs the request path escaped once; the gateway re-signed it with
// the signer's default double escaping, so every key containing a space, '+',
// or non-ASCII byte was rejected with SignatureDoesNotMatch — both for
// header-signed requests and for presigned URLs.
func TestSigV4_SDKKeysNeedingEscaping(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("ETag", `"sdk-etag"`)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("hi")), Request: r}, nil
	})
	handler.now = func() time.Time { return time.Now().UTC() }
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler.host = endpoint.Host
	client := awss3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(testAccess, testSecret, ""),
		HTTPClient:  server.Client(),
	}, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	presigner := awss3.NewPresignClient(client)

	for _, key := range []string{
		"plain.txt",
		"my file.txt",
		"a+b.txt",
		"reports/2026-09/ü.csv",
		"tilde~(paren);semi.txt",
	} {
		t.Run(key, func(t *testing.T) {
			ctx := context.Background()
			input := &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(key)}
			if _, err := client.GetObject(ctx, input); err != nil {
				t.Fatalf("header-signed GetObject: %v", err)
			}
			presigned, err := presigner.PresignGetObject(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := server.Client().Get(presigned.URL)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("presigned GET status = %d, want 200", resp.StatusCode)
			}
		})
	}
}

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
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGatewayAWSGoSDKCompatibility(t *testing.T) {
	var uploaded []byte
	handler, _, provider := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			var err error
			uploaded, err = io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
		}
		header := make(http.Header)
		header.Set("ETag", `"sdk-etag"`)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	handler.now = func() time.Time { return time.Now().UTC() }
	var customerHeaders []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customerHeaders = append(customerHeaders, r.Header.Clone())
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler.host = endpoint.Host
	client := awss3.NewFromConfig(aws.Config{
		Region:                     "us-east-1",
		Credentials:                credentials.NewStaticCredentialsProvider(testAccess, testSecret, ""),
		HTTPClient:                 server.Client(),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenSupported,
	}, func(options *awss3.Options) {
		options.BaseEndpoint = aws.String(server.URL)
		options.UsePathStyle = true
	})

	payload := "current AWS SDK checksum framing"
	size := int64(len(payload))
	_, err = client.PutObject(context.Background(), &awss3.PutObjectInput{
		Bucket: aws.String("assets"), Key: aws.String("sdk.txt"),
		Body: strings.NewReader(payload), ContentLength: aws.Int64(size),
	})
	if err != nil {
		t.Fatalf("PutObject through AWS SDK: %v", err)
	}
	if string(uploaded) != payload {
		t.Fatalf("uploaded body = %q", uploaded)
	}
	if len(customerHeaders) != 1 || !hasSDKIntegrityHeader(customerHeaders[0]) {
		t.Fatalf("PutObject did not carry an SDK integrity header: %v", customerHeaders)
	}

	_, err = client.DeleteObjects(context.Background(), &awss3.DeleteObjectsInput{
		Bucket: aws.String("assets"),
		Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: aws.String("sdk.txt")}, {Key: aws.String("old.txt")}}},
	})
	if err != nil {
		t.Fatalf("DeleteObjects through AWS SDK: %v", err)
	}
	if strings.Join(provider.deleted, ",") != "sdk.txt,old.txt" {
		t.Fatalf("deleted keys = %v", provider.deleted)
	}
	if len(customerHeaders) != 2 || !hasSDKIntegrityHeader(customerHeaders[1]) {
		t.Fatalf("DeleteObjects did not carry an SDK integrity header: %v", customerHeaders)
	}
}

func hasSDKIntegrityHeader(header http.Header) bool {
	if header.Get("Content-MD5") != "" {
		return true
	}
	for _, name := range awsChecksumHeaderNames {
		if header.Get(name) != "" {
			return true
		}
	}
	return false
}

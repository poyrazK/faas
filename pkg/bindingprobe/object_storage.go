// Package bindingprobe implements bounded platform-owned binding canaries.
package bindingprobe

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/api"
)

const objectStorageTimeout = 5 * time.Second

// ObjectStorage uses only the selected injected credentials. It makes one
// signed, path-style ListObjectsV2 request and never follows redirects, writes
// objects, retries or returns object names or raw provider diagnostics.
func ObjectStorage(ctx context.Context, prefix string, getenv func(string) string) api.ObjectStorageBindingProbeReport {
	report := api.ObjectStorageBindingProbeReport{
		Environment:   api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		Configuration: api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		Connection:    api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		Authorization: api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		BucketAccess:  api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
	}
	if !api.ValidObjectStorageBindingPrefix(prefix) {
		report.Environment = api.ObjectStorageBindingProbeCheck{Status: "failed", Detail: "binding prefix is invalid"}
		report.Error = "binding prefix is invalid"
		return report
	}
	report.Prefix = prefix
	values := make([]string, 0, 6)
	for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
		value := getenv(prefix + suffix)
		if value == "" {
			report.Environment = api.ObjectStorageBindingProbeCheck{Status: "failed", Detail: "required environment variables are missing from this deployment"}
			report.Error = "object-storage binding environment is incomplete"
			return report
		}
		values = append(values, value)
	}
	report.Environment = api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "all six binding environment variables are present"}
	endpoint, err := url.Parse(values[0])
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		values[5] != "path" || strings.ContainsAny(values[2], "/\\ \t\r\n?#") ||
		strings.ContainsAny(values[1], "/\\ \t\r\n?#") {
		report.Configuration = api.ObjectStorageBindingProbeCheck{Status: "failed", Detail: "endpoint, region, bucket or addressing style is invalid"}
		report.Error = "object-storage binding configuration is invalid"
		return report
	}
	report.Configuration = api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "path-style S3 configuration validated"}
	probeContext, cancel := context.WithTimeout(ctx, objectStorageTimeout)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	observation := &objectStorageTransport{base: transport}
	client := s3.New(s3.Options{
		Region: values[1], BaseEndpoint: aws.String(values[0]), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(values[3], values[4], ""),
		HTTPClient: &http.Client{Transport: observation, Timeout: objectStorageTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		RetryMaxAttempts:           1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	output, err := client.ListObjectsV2(probeContext, &s3.ListObjectsV2Input{Bucket: aws.String(values[2]), MaxKeys: aws.Int32(1)})
	if observation.status == 0 {
		report.Connection = api.ObjectStorageBindingProbeCheck{Status: "failed", Detail: "could not reach the S3 endpoint; verify DNS, TLS and network access"}
		report.Error = "object-storage connection failed"
		return report
	}
	report.Connection = api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "S3 endpoint returned a response"}
	if err == nil && observation.status == http.StatusOK && output != nil && aws.ToString(output.Name) == values[2] {
		report.Authorization = api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "injected credentials were accepted for bucket listing"}
		report.BucketAccess = api.ObjectStorageBindingProbeCheck{Status: "passed", Detail: "read-only bucket listing succeeded; write access was not checked"}
	} else if observation.status == http.StatusUnauthorized || observation.status == http.StatusForbidden {
		report.Authorization = api.ObjectStorageBindingProbeCheck{Status: "failed", Detail: "credentials or bucket read permission were rejected"}
		report.Error = "object-storage authorization failed"
	} else {
		report.BucketAccess = api.ObjectStorageBindingProbeCheck{Status: "failed", Detail: "bucket listing failed; verify the bucket and storage provider availability"}
		report.Error = "read-only object-storage bucket listing failed"
	}
	return report
}

type objectStorageTransport struct {
	base   http.RoundTripper
	status int
}

func (t *objectStorageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if response != nil {
		t.status = response.StatusCode
		// Bound success and error XML before the SDK parses it.
		response.Body = http.MaxBytesReader(nil, response.Body, 64*1024)
	}
	return response, err
}

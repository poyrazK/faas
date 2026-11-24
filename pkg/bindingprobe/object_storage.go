// Package bindingprobe implements bounded platform-owned binding canaries.
package bindingprobe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/api"
)

// objectStorageTimeout bounds the whole probe and objectStorageAttemptTimeout
// one request. A verification task VM is cold: its first request pays DNS,
// the first egress packet and a TLS handshake, and a single 5 s attempt
// reported live, working bindings as unreachable. The whole probe stays
// inside the 15 s app-task budget (api.BindingVerificationRunProbeTimeoutSeconds).
const (
	objectStorageTimeout        = 13 * time.Second
	objectStorageAttemptTimeout = 6 * time.Second
	objectStorageReadAttempts   = 2
)

// ObjectStorage uses only the selected injected credentials. It makes a
// signed, path-style ListObjectsV2 request, repeated once only when no HTTP
// response arrived, and never follows redirects, writes objects or returns
// object names or raw provider diagnostics.
func ObjectStorage(ctx context.Context, prefix string, getenv func(string) string) api.ObjectStorageBindingProbeReport {
	return objectStorage(ctx, prefix, getenv, "")
}

// ObjectStorageWrite is opt-in. Write-only credentials never list or download.
func ObjectStorageWrite(ctx context.Context, prefix, mode string, getenv func(string) string) api.ObjectStorageBindingProbeReport {
	if !api.ValidObjectStorageWriteProbeMode(mode) {
		return api.ObjectStorageBindingProbeReport{Error: "object-storage write probe mode is invalid"}
	}
	return objectStorage(ctx, prefix, getenv, mode)
}

func objectStorage(ctx context.Context, prefix string, getenv func(string) string, mode string) api.ObjectStorageBindingProbeReport {
	report := api.ObjectStorageBindingProbeReport{
		Environment:   api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		Configuration: api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		Connection:    api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		Authorization: api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
		BucketAccess:  api.ObjectStorageBindingProbeCheck{Status: "not_checked"},
	}
	if mode != "" {
		report.Mode = mode
		report.Upload = &api.ObjectStorageBindingProbeCheck{Status: "not_checked"}
		report.Download = &api.ObjectStorageBindingProbeCheck{Status: "not_checked"}
		report.Cleanup = &api.ObjectStorageBindingProbeCheck{Status: "not_checked"}
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
		HTTPClient: &http.Client{Transport: observation, Timeout: objectStorageAttemptTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		RetryMaxAttempts:           1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	if mode != "" {
		return objectStorageWrite(probeContext, client, observation, values[2], report)
	}
	var output *s3.ListObjectsV2Output
	// A read-only listing is safe to repeat; only a request that got no HTTP
	// response at all is tried again.
	for attempt := 0; attempt < objectStorageReadAttempts && (attempt == 0 || observation.status == 0 && probeContext.Err() == nil); attempt++ {
		output, err = client.ListObjectsV2(probeContext, &s3.ListObjectsV2Input{Bucket: aws.String(values[2]), MaxKeys: aws.Int32(1)})
	}
	if observation.status == 0 {
		report.Connection = api.ObjectStorageBindingProbeCheck{Status: "failed", Detail: objectStorageConnectionDetail(observation.err)}
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
	err    error
}

func (t *objectStorageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	t.err = err
	if response != nil {
		t.status = response.StatusCode
		// Bound success and error XML before the SDK parses it.
		response.Body = http.MaxBytesReader(nil, response.Body, 64*1024)
	}
	return response, err
}

// objectStorageConnectionDetail names the step that failed without echoing
// raw provider or resolver text.
func objectStorageConnectionDetail(err error) string {
	const advice = "; the binding was checked from a fresh verification VM"
	var dnsErr *net.DNSError
	var opErr *net.OpError
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	switch {
	case err == nil:
		return "could not reach the S3 endpoint; verify DNS, TLS and network access" + advice
	case errors.As(err, &dnsErr):
		return "DNS lookup for the S3 endpoint host failed" + advice
	case errors.As(err, &certErr) || errors.As(err, &recordErr):
		return "TLS handshake with the S3 endpoint failed; verify the endpoint scheme and certificate" + advice
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded):
		return fmt.Sprintf("the S3 endpoint did not answer within %s (%d attempts)", objectStorageAttemptTimeout, objectStorageReadAttempts) + advice
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return "could not open a connection to the S3 endpoint; verify the app's egress rules allow it" + advice
	}
	return "could not reach the S3 endpoint; verify DNS, TLS and network access" + advice
}

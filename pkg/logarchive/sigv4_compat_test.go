package logarchive

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// The archive talks SigV4 to S3-compatible stores (OVH, R2, MinIO). A store
// recomputes the signature from the headers the request actually carries, so
// a signed header the request lacks (content-type on a GET) fails the check
// on stores that require every signed header to be present. For requests
// without a body the AWS SDK's signer is the reference: both must produce
// the same Authorization header.
func TestS3_SigV4MatchesTheAWSSignerForBodylessRequests(t *testing.T) {
	c, err := NewS3Client("https://s3.gra.io.cloud.ovh.net", "gra", "archive", "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY")
	if err != nil {
		t.Fatal(err)
	}
	list := url.Values{}
	list.Set("prefix", "faas-logs/i-1/")
	list.Set("marker", "faas-logs/i-1/2026/10/01.jsonl.gz")
	for name, build := range map[string]func() *http.Request{
		"get": func() *http.Request {
			r, _ := http.NewRequest(http.MethodGet, c.objectURL("faas-logs/i-1/2026/10/01.jsonl.gz"), nil)
			return r
		},
		"delete": func() *http.Request {
			r, _ := http.NewRequest(http.MethodDelete, c.objectURL("faas-logs/i-1/2026/10/01.jsonl.gz"), nil)
			return r
		},
		"list": func() *http.Request {
			u, _ := url.Parse(c.Endpoint)
			u.Path = "/" + c.Bucket
			u.RawQuery = list.Encode()
			r, _ := http.NewRequest(http.MethodGet, u.String(), nil)
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			ours := build()
			if err := c.sign(ours, hexSHA256(nil)); err != nil {
				t.Fatal(err)
			}
			at, err := time.Parse("20060102T150405Z", ours.Header.Get("x-amz-date"))
			if err != nil {
				t.Fatal(err)
			}
			ref := build()
			ref.Header.Set("x-amz-content-sha256", hexSHA256(nil))
			creds := aws.Credentials{AccessKeyID: c.KeyID, SecretAccessKey: c.Secret}
			if err := v4.NewSigner().SignHTTP(context.Background(), creds, ref, hexSHA256(nil), "s3", c.Region, at); err != nil {
				t.Fatal(err)
			}
			if got, want := ours.Header.Get("Authorization"), ref.Header.Get("Authorization"); got != want {
				t.Fatalf("Authorization differs from the AWS signer:\n got %s\nwant %s", got, want)
			}
		})
	}
}

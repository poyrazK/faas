package objectstorage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// adr: 553
func TestS3NativeTransferUsesStreamBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "x" {
				t.Error("S3 native writer changed bytes", err)
			}
		}
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("ETag", `"local-transfer"`)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Length", "1")
			_, _ = io.WriteString(w, "x")
		}
	}))
	defer server.Close()
	config := testBackend()
	config.Endpoint = server.URL
	provider, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*S3)
	options := p.client.Options()
	httpClient := *options.HTTPClient.(*http.Client)
	// Keep the metadata bound short to qualify real SDK streams quickly.
	httpClient.Timeout = 30 * time.Millisecond
	options.HTTPClient = &httpClient
	p.client = s3.New(options)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	out, err := p.WriteObject(ctx, "physical", "key", strings.NewReader("x"), 1, ObjectMetadata{ContentType: "text/plain"})
	if err != nil || out.ETag != `"local-transfer"` {
		t.Fatal("S3 writer used metadata timeout", out, err)
	}
	body, err := p.ReadObject(ctx, "physical", "key")
	if err != nil {
		t.Fatal("S3 reader used metadata timeout", err)
	}
	data, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || string(data) != "x" {
		t.Fatal("S3 stream body", readErr, closeErr)
	}
	if _, err = p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("physical"), Key: aws.String("key")}); err == nil {
		t.Fatal("S3 metadata escaped its shorter bound")
	}
}

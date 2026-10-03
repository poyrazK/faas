package objectstorage

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

// adr: 411
func TestGCSNativeTransferUsesStreamBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-fixture" {
			t.Error("native SDK lost OAuth identity")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadType") == "multipart":
			_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			parts := multipart.NewReader(r.Body, params["boundary"])
			metadata, err := parts.NextPart()
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			_, _ = io.Copy(io.Discard, metadata)
			body, err := parts.NextPart()
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			payload, err := io.ReadAll(body)
			if err != nil || string(payload) != "x" {
				t.Error("native SDK changed payload", err)
			}
			time.Sleep(100 * time.Millisecond)
		case r.URL.Query().Get("alt") == "media":
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Length", "1")
			_, _ = io.WriteString(w, "x")
			return
		case strings.HasSuffix(r.URL.Path, "/b/physical"):
			time.Sleep(100 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"bucket":"physical","name":"key","size":"1","etag":"local-transfer","generation":"1"}`)
	}))
	defer server.Close()
	httpClient := newGCSHTTPClient(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "local-fixture"}))
	// Shrink the control bound so the test qualifies the separation without
	// waiting for the production 20-second control timeout.
	httpClient.Transport.(*gcsRequestTransport).client.Timeout = 30 * time.Millisecond
	client, err := storage.NewClient(t.Context(), option.WithHTTPClient(httpClient), option.WithEndpoint(server.URL+"/"), storage.WithJSONReads(), storage.WithDisabledClientMetrics())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	store := &googleGCSStore{client: client}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	out, err := store.WriteObject(ctx, "physical", "key", strings.NewReader("x"), 1, ObjectMetadata{ContentType: "text/plain"})
	if err != nil || out.ETag != "local-transfer" {
		t.Fatal("native writer used metadata timeout", out, err)
	}
	body, err := store.ReadObject(ctx, "physical", "key")
	if err != nil {
		t.Fatal("native reader used metadata timeout", err)
	}
	data, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || string(data) != "x" {
		t.Fatal("native stream body", readErr, closeErr)
	}
	if _, err = store.BucketState(ctx, "physical"); err == nil {
		t.Fatal("metadata call escaped its shorter bound")
	}
}

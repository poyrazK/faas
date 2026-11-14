package objectstorage

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

func TestGCSVerificationReadsPreserveStoredBytes(t *testing.T) {
	for _, jsonReads := range []bool{false, true} {
		for _, compressed := range []bool{false, true} {
			for _, pinned := range []bool{false, true} {
				t.Run(fmt.Sprintf("json=%v/gzip=%v/pinned=%v", jsonReads, compressed, pinned), func(t *testing.T) {
					want := []byte("artifact contents\x00\xff")
					if compressed {
						var encoded bytes.Buffer
						zw := gzip.NewWriter(&encoded)
						if _, err := zw.Write(want); err != nil {
							t.Fatal(err)
						}
						if err := zw.Close(); err != nil {
							t.Fatal(err)
						}
						want = encoded.Bytes()
					}
					native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Header.Get("Accept-Encoding") != "gzip" || pinned && r.URL.Query().Get("generation") != "1" {
							t.Error("read did not retain raw encoding or pinned generation", r.Header, r.URL)
						}
						if compressed {
							w.Header().Set("Content-Encoding", "gzip")
						}
						w.Header().Set("Content-Length", strconv.Itoa(len(want)))
						w.Header().Set("X-Goog-Generation", "1")
						_, _ = w.Write(want)
					}))
					t.Cleanup(native.Close)
					httpClient := newGCSHTTPClient(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fixture"}))
					opts := []option.ClientOption{option.WithHTTPClient(httpClient), option.WithEndpoint(native.URL + "/"), storage.WithDisabledClientMetrics()}
					if jsonReads {
						opts = append(opts, storage.WithJSONReads())
					}
					client, err := storage.NewClient(t.Context(), opts...)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = client.Close() })
					provider := &GCS{store: &googleGCSStore{client: client}}
					var stream io.ReadCloser
					if pinned {
						stream, err = provider.ReadObjectVersion(t.Context(), "physical", "key", "1")
					} else {
						stream, err = provider.ReadObject(t.Context(), "physical", "key")
					}
					if err != nil {
						t.Fatal(err)
					}
					got, readErr := io.ReadAll(stream)
					closeErr := stream.Close()
					if readErr != nil || closeErr != nil || !bytes.Equal(got, want) {
						t.Fatalf("verification bytes changed: stored=%d returned=%d read=%v close=%v", len(want), len(got), readErr, closeErr)
					}
				})
			}
		}
	}
}

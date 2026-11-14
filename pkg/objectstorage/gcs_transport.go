package objectstorage

import "net/http"

type gcsObjectStreamContextKey struct{}

// The native SDK shares one OAuth client between streams and metadata. Bound
// each exchange with a copied client so concurrent requests never mutate its
// timeout. The inner client also retains the bound while reading response bodies.
type gcsRequestTransport struct {
	client *http.Client
}

func (t *gcsRequestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	client := *t.client
	if streaming, _ := r.Context().Value(gcsObjectStreamContextKey{}).(bool); streaming {
		client.Timeout = objectStreamTimeout(r.Context())
		if r.Method == http.MethodGet {
			// Verification reads need stored bytes. An explicit gzip header
			// prevents GCS transcoding and Go's automatic decompression. Set
			// it here because the JSON SDK rejects ReadCompressed(true)'s
			// header before dispatching to the transport.
			r = r.Clone(r.Context())
			r.Header.Set("Accept-Encoding", "gzip")
		}
	}
	return client.Do(r)
}

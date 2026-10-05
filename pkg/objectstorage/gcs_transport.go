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
	}
	return client.Do(r)
}

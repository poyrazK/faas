package main

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	oauthHTTPTimeout      = 10 * time.Second
	oauthResponseMaxBytes = 1 << 20
)

// oauthHTTPClient returns a request-scoped copy of the process default client
// with a finite timeout. Copying the current transport keeps the test seam
// (and any operator-installed transport) while preventing an OAuth provider
// from holding an apid handler forever.
func oauthHTTPClient() *http.Client {
	client := &http.Client{Timeout: oauthHTTPTimeout}
	if http.DefaultClient != nil {
		client.Transport = http.DefaultClient.Transport
		client.CheckRedirect = http.DefaultClient.CheckRedirect
		client.Jar = http.DefaultClient.Jar
	}
	return client
}

// readOAuthBody reads provider responses with a hard upper bound. OAuth
// payloads are tiny; a larger response is treated as an upstream failure
// rather than retained in apid memory.
func readOAuthBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, oauthResponseMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > oauthResponseMaxBytes {
		return nil, fmt.Errorf("oauth response exceeds %d bytes", oauthResponseMaxBytes)
	}
	return body, nil
}

package main

import (
	"strings"
	"testing"
)

func TestReadOAuthBodyRejectsOversizedResponse(t *testing.T) {
	if _, err := readOAuthBody(strings.NewReader(strings.Repeat("x", oauthResponseMaxBytes+1))); err == nil {
		t.Fatal("readOAuthBody unexpectedly accepted an oversized response")
	}
}

func TestOAuthHTTPClientHasTimeout(t *testing.T) {
	client := oauthHTTPClient()
	if client.Timeout != oauthHTTPTimeout {
		t.Fatalf("OAuth client timeout = %s, want %s", client.Timeout, oauthHTTPTimeout)
	}
}

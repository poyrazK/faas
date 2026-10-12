package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestReadOAuthBodyRejectsOversizedResponse(t *testing.T) {
	if _, err := apidserver.ReadOAuthBody(strings.NewReader(strings.Repeat("x", apidserver.OauthResponseMaxBytes+1))); err == nil {
		t.Fatal("readOAuthBody unexpectedly accepted an oversized response")
	}
}

func TestOAuthHTTPClientHasTimeout(t *testing.T) {
	client := apidserver.OauthHTTPClient()
	if client.Timeout != apidserver.OauthHTTPTimeout {
		t.Fatalf("OAuth client timeout = %s, want %s", client.Timeout, apidserver.OauthHTTPTimeout)
	}
}

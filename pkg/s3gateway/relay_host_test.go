package s3gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// ADR-20261031193635 slice 6c: the bridge object relay forwards a contained
// branch guest's request with the Host the guest signed. The gateway accepts
// that host only from a loopback peer, and the signature still has to verify.
func TestGatewayRelayHostOnlyFromLoopback(t *testing.T) {
	const relayHost = "objects.gregale:10085"
	signed := func(host, remote string) *http.Request {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "http://"+host+"/assets/key?x-id=GetObject", nil)
		request.Host = host
		request.RemoteAddr = remote
		request.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
		request.Header.Set("X-Amz-Date", "20260907T120000Z")
		if err := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(context.Background(),
			aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, request, "UNSIGNED-PAYLOAD", "s3", "us-east-1",
			time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		return request
	}
	serve := func(relay []string, request *http.Request) *httptest.ResponseRecorder {
		t.Helper()
		handler, _, _ := newGatewayTestHandler(t, "read", func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Length": {"5"}}, Body: io.NopCloser(strings.NewReader("hello")), Request: r}, nil
		})
		handler.relayHosts = relayHostSet(relay)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	relay := []string{"objects.gregale", relayHost}

	if got := serve(relay, signed(relayHost, "127.0.0.1:41000")); got.Code != http.StatusOK || got.Body.String() != "hello" {
		t.Fatalf("relayed GET = %d %s", got.Code, got.Body.String())
	}
	for name, tc := range map[string]struct {
		relay   []string
		request *http.Request
	}{
		"relay host from a remote peer": {relay, signed(relayHost, "203.0.113.7:41000")},
		"relay host not configured":     {nil, signed(relayHost, "127.0.0.1:41000")},
		"unrelated host from loopback":  {relay, signed("evil.example:10085", "127.0.0.1:41000")},
	} {
		if got := serve(tc.relay, tc.request); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "InvalidRequest") {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	// The relay forwards, it never re-signs: a request whose signature
	// covers another host fails verification even from loopback.
	tampered := signed("s3.gregale.dev", "127.0.0.1:41000")
	tampered.Host = relayHost
	if got := serve(relay, tampered); got.Code != http.StatusForbidden {
		t.Fatalf("host swapped after signing = %d %s", got.Code, got.Body.String())
	}
}

package storagefixture

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewayUsesVerifiedTLSAndObservesNewConnections(t *testing.T) {
	g := NewGateway(t, "127.0.0.1:0")
	g.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "exact bytes") }))
	block, _ := pem.Decode(g.CA)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || certificate.VerifyHostname("127.0.0.1") != nil || certificate.VerifyHostname("198.18.0.254") == nil {
		t.Fatal("fixture certificate is not pinned to its owned listener IP")
	}
	read := func() {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, g.URL+"/assets?list-type=2", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=fixture-key/20261015/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=private")
		response, err := g.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "exact bytes" {
			t.Fatal("fixture transport changed response")
		}
	}
	read()
	read()
	first := observations(t, g, 2)
	if first[0].Connection == 0 || first[0].Connection != first[1].Connection || first[0].AccessKey != "fixture-key" || !first[0].Listing {
		t.Fatal("fixture failed to observe a retained signed transport")
	}
	g.Disconnect()
	read()
	after := observations(t, g, 3)
	if after[2].Connection == first[0].Connection {
		t.Fatal("disconnected TLS transport was reused")
	}
}

func TestListingFaultIsCancelledAndDoesNotAffectColdValidation(t *testing.T) {
	g := NewGateway(t, "127.0.0.1:0")
	var calls atomic.Int32
	g.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusNoContent) }))
	fault, err := g.DelayNextList(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.DelayNextList(time.Second); err == nil {
		t.Fatal("overlapping fault accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, g.URL+"/assets?list-type=2", nil)
	result := make(chan error, 1)
	go func() {
		response, err := g.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-fault.Entered:
	case <-time.After(3 * time.Second):
		t.Fatal("listing fault not consumed")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("listing did not observe cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled request remained live")
	}
	select {
	case <-fault.Cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled validation continued through the gateway")
	}
	select {
	case <-fault.Done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled fixture validation did not finish cleanup")
	}
	request, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, g.URL+"/assets?list-type=2", nil)
	response, err := g.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || calls.Load() != 1 {
		t.Fatal("consumed restore fault affected independent cold validation")
	}
	records := observations(t, g, 2)
	if records[0].Status != 0 || records[1].Status != http.StatusNoContent {
		t.Fatal("cancelled validation was recorded as a successful adoption")
	}
}

func TestNonListingRequestsDoNotConsumeRestoreFault(t *testing.T) {
	g := NewGateway(t, "127.0.0.1:0")
	g.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	fault, err := g.DelayNextList(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, g.URL+"/assets/object.bin", nil)
	response, err := g.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	select {
	case <-fault.Entered:
		t.Fatal("data request consumed a validation fault")
	default:
	}
	g.ClearFault()
	if _, err := g.DelayNextList(time.Second); err != nil {
		t.Fatal("unconsumed fault could not be retired")
	}
	g.ClearFault()
}

func observations(t *testing.T, g *Gateway, count int) []Observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if result := g.Observations(); len(result) >= count {
			return result
		}
		select {
		case <-ctx.Done():
			t.Fatal("missing completed gateway observations")
		case <-ticker.C:
		}
	}
}

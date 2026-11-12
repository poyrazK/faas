// Package registryproxy pulls from an app's private registry through the
// control plane's secretd (ADR-20261110210000 slice 3j). A compute node
// never holds the registry password: oci.BasicAuth{Transport: For(...)}
// sends each registry request to secretd's RegistryFetch, which adds the
// credential, follows the registry's redirects and streams the answer back.
package registryproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
)

// forwardHeaders are the request headers secretd accepts.
var forwardHeaders = []string{"Accept", "Range", "If-None-Match", "If-Modified-Since"}

// Proxy hands out per-app transports over one secretd client.
type Proxy struct {
	Client secretdpb.SecretdClient
}

// For returns the transport that pulls appID's private images.
func (p *Proxy) For(appID string) http.RoundTripper {
	return &transport{client: p.Client, appID: appID}
}

type transport struct {
	client secretdpb.SecretdClient
	appID  string
}

// RoundTrip implements http.RoundTripper.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, fmt.Errorf("registryproxy: %s is not a pull", req.Method)
	}
	in := &secretdpb.RegistryFetchRequest{AppId: t.appID, Method: req.Method, Url: req.URL.String()}
	for _, name := range forwardHeaders {
		for _, v := range req.Header.Values(name) {
			in.Headers = append(in.Headers, &secretdpb.RegistryHeader{Name: name, Value: v})
		}
	}
	ctx, cancel := context.WithCancel(req.Context())
	stream, err := t.client.RegistryFetch(ctx, in)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("registryproxy: secretd: %w", err)
	}
	first, err := stream.Recv()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("registryproxy: secretd: %w", err)
	}
	head := first.GetHead()
	if head == nil {
		cancel()
		return nil, errors.New("registryproxy: secretd answered without a head")
	}
	resp := &http.Response{
		StatusCode: int(head.GetStatus()),
		Status:     strconv.Itoa(int(head.GetStatus())) + " " + http.StatusText(int(head.GetStatus())),
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:        http.Header{},
		Request:       req,
		ContentLength: -1,
	}
	for _, h := range head.GetHeaders() {
		resp.Header.Add(h.GetName(), h.GetValue())
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(resp.Header.Get("Content-Length")), 10, 64); err == nil && n >= 0 {
		resp.ContentLength = n
	}
	if req.Method == http.MethodHead {
		cancel()
		resp.Body = http.NoBody
		return resp, nil
	}
	resp.Body = &streamBody{recv: stream.Recv, cancel: cancel}
	return resp, nil
}

// streamBody reads the body frames of one RegistryFetch.
type streamBody struct {
	recv   func() (*secretdpb.RegistryFetchResponse, error)
	cancel context.CancelFunc
	buf    []byte
	done   error
}

func (b *streamBody) Read(p []byte) (int, error) {
	for len(b.buf) == 0 {
		if b.done != nil {
			return 0, b.done
		}
		frame, err := b.recv()
		if errors.Is(err, io.EOF) {
			b.done = io.EOF
			continue
		}
		if err != nil {
			b.done = fmt.Errorf("registryproxy: body: %w", err)
			continue
		}
		b.buf = frame.GetBodyChunk()
	}
	n := copy(p, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}

func (b *streamBody) Close() error {
	b.cancel()
	return nil
}

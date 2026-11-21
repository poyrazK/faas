package registryproxy_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/registryproxy"
)

const manifest = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:c0","size":2},"layers":[]}`

// fakeSecretd answers RegistryFetch from a map of URL -> body, and records
// what it was asked.
type fakeSecretd struct {
	secretdpb.SecretdClient
	asked   []*secretdpb.RegistryFetchRequest
	refuse  bool
	answers map[string]string
}

type fakeFetchStream struct {
	grpc.ClientStream
	frames []*secretdpb.RegistryFetchResponse
}

func (s *fakeFetchStream) Recv() (*secretdpb.RegistryFetchResponse, error) {
	if len(s.frames) == 0 {
		return nil, io.EOF
	}
	f := s.frames[0]
	s.frames = s.frames[1:]
	return f, nil
}

func (f *fakeSecretd) RegistryFetch(_ context.Context, in *secretdpb.RegistryFetchRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[secretdpb.RegistryFetchResponse], error) {
	f.asked = append(f.asked, in)
	if f.refuse {
		return nil, status.Error(codes.PermissionDenied, "not the owner")
	}
	body, ok := f.answers[in.GetUrl()]
	code := int32(200)
	if !ok {
		code = 404
	}
	frames := []*secretdpb.RegistryFetchResponse{{Frame: &secretdpb.RegistryFetchResponse_Head{Head: &secretdpb.RegistryFetchHead{
		Status: code, Headers: []*secretdpb.RegistryHeader{
			{Name: "Content-Type", Value: "application/vnd.oci.image.manifest.v1+json"},
			{Name: "Docker-Content-Digest", Value: digestOf(body)},
		}}}}}
	if in.GetMethod() == http.MethodGet {
		for i := 0; i < len(body); i += 7 {
			frames = append(frames, &secretdpb.RegistryFetchResponse{Frame: &secretdpb.RegistryFetchResponse_BodyChunk{BodyChunk: []byte(body[i:min(i+7, len(body))])}})
		}
	}
	return &fakeFetchStream{frames: frames}, nil
}

func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// adr: 20261110210000 — slice 3j: a proxied pull sends every registry
// request to secretd for the app, with no credential, and reads the answer
// back in frames.
func TestProxiedPullGoesThroughSecretd(t *testing.T) {
	sd := &fakeSecretd{answers: map[string]string{"https://ghcr.io/v2/team/app/manifests/v1": manifest}}
	proxy := &registryproxy.Proxy{Client: sd}
	auth := &oci.BasicAuth{Transport: proxy.For("app-a")}
	client := oci.NewRegistryClient()
	digest, err := client.PullDigestWithAuth(context.Background(), "ghcr.io/team/app:v1", auth)
	if err != nil {
		t.Fatalf("pull = %v", err)
	}
	if digest == "" || len(sd.asked) == 0 {
		t.Fatalf("digest %q after %d asks", digest, len(sd.asked))
	}
	for _, in := range sd.asked {
		if in.GetAppId() != "app-a" || !strings.HasPrefix(in.GetUrl(), "https://ghcr.io/v2/") {
			t.Fatalf("asked %+v", in)
		}
		for _, h := range in.GetHeaders() {
			if strings.EqualFold(h.GetName(), "Authorization") {
				t.Fatal("a credential header was sent to secretd")
			}
		}
	}

	// A refusal fails the pull instead of falling back to an anonymous one.
	sd.refuse = true
	if _, err := client.PullDigestWithAuth(context.Background(), "ghcr.io/team/app:v1", auth); err == nil {
		t.Fatal("a refused proxy pull succeeded")
	}
}

func TestTransportRefusesWritesAndStreamsBodies(t *testing.T) {
	sd := &fakeSecretd{answers: map[string]string{"https://r.example/v2/x/blobs/sha256:a": strings.Repeat("ab", 20)}}
	rt := (&registryproxy.Proxy{Client: sd}).For("app-a")
	req, _ := http.NewRequest(http.MethodPut, "https://r.example/v2/x/blobs/uploads/", nil)
	if resp, err := rt.RoundTrip(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("a PUT went through the pull proxy")
	}
	req, _ = http.NewRequest(http.MethodGet, "https://r.example/v2/x/blobs/sha256:a", nil)
	req.Header.Set("Authorization", "Bearer node-token")
	req.Header.Set("Range", "bytes=0-")
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != strings.Repeat("ab", 20) || resp.StatusCode != 200 {
		t.Fatalf("body = %q %v %d", body, err, resp.StatusCode)
	}
	got := sd.asked[len(sd.asked)-1].GetHeaders()
	if len(got) != 1 || got[0].GetName() != "Range" {
		t.Fatalf("forwarded headers = %v", got)
	}
	sd.refuse = true
	resp, err = rt.RoundTrip(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	if status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("refusal = %v", err)
	}
}

package vmmdgrpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

type fakeEdgeHeaderOpener struct {
	values map[string]string // secret id -> value
	asked  []string
}

func (f *fakeEdgeHeaderOpener) OpenEdgeHeaderSecret(_ context.Context, instance, app, id string) (string, error) {
	f.asked = append(f.asked, instance+"/"+app+"/"+id)
	v, ok := f.values[id]
	if !ok {
		return "", errors.New("secretd: not found")
	}
	return v, nil
}

// Slice 3i: vmmd opens the values a forward names and adds them after the
// request's headers; anything it cannot open refuses the forward.
func TestOpenEdgeSecretHeaders(t *testing.T) {
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ref := func(name, action, id string) *vmmdpb.EdgeSecretHeader {
		return &vmmdpb.EdgeSecretHeader{Name: name, Action: action, SecretAppId: "app-a", SecretId: id}
	}
	ctx := context.Background()
	if got, err := s.openEdgeSecretHeaders(ctx, "i-1", nil); err != nil || got != nil {
		t.Fatalf("no refs = %v, %v", got, err)
	}
	if _, err := s.openEdgeSecretHeaders(ctx, "i-1", []*vmmdpb.EdgeSecretHeader{ref("Authorization", "set", "s-1")}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("without an opener = %v, want FailedPrecondition", err)
	}
	opener := &fakeEdgeHeaderOpener{values: map[string]string{"s-1": "Bearer up", "s-2": "k2", "bad": "a\r\nInjected: 1"}}
	s.WithEdgeHeaderSecrets(opener)
	got, err := s.openEdgeSecretHeaders(ctx, "i-1", []*vmmdpb.EdgeSecretHeader{ref("Authorization", "set", "s-1"), ref("X-Api-Key", "add", "s-2")})
	if err != nil || len(got) != 2 || got[0].value != "Bearer up" || got[1].value != "k2" {
		t.Fatalf("open = %+v, %v", got, err)
	}
	if opener.asked[0] != "i-1/app-a/s-1" {
		t.Fatalf("asked = %v", opener.asked)
	}
	for name, refs := range map[string][]*vmmdpb.EdgeSecretHeader{
		"unknown value":      {ref("Authorization", "set", "s-9")},
		"CR/LF in the value": {ref("Authorization", "set", "bad")},
		"one of two unknown": {ref("Authorization", "set", "s-1"), ref("X-Other", "add", "s-9")},
	} {
		if _, err := s.openEdgeSecretHeaders(ctx, "i-1", refs); status.Code(err) != codes.Unavailable {
			t.Errorf("%s = %v, want Unavailable", name, err)
		}
	}
	for name, refs := range map[string][]*vmmdpb.EdgeSecretHeader{
		"remove action":      {ref("Authorization", "remove", "s-1")},
		"reserved name":      {ref("Host", "set", "s-1")},
		"platform header":    {ref("X-Faas-Instance", "set", "s-1")},
		"invalid name":       {ref("Bad Name", "set", "s-1")},
		"missing secret app": {{Name: "Authorization", Action: "set", SecretId: "s-1"}},
	} {
		if _, err := s.openEdgeSecretHeaders(ctx, "i-1", refs); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s = %v, want InvalidArgument", name, err)
		}
	}
	many := make([]*vmmdpb.EdgeSecretHeader, maxEdgeSecretHeaders+1)
	for i := range many {
		many[i] = ref("X-K", "add", "s-2")
	}
	if _, err := s.openEdgeSecretHeaders(ctx, "i-1", many); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("too many = %v", err)
	}
}

func TestWithEdgeSecretHeaders(t *testing.T) {
	headers := []*vmmdpb.Header{{Name: "authorization", Value: "client"}, {Name: "Accept", Value: "*/*"}, {Name: "X-Key", Value: "a"}}
	got := withEdgeSecretHeaders(headers, []edgeSecretHeader{
		{name: "Authorization", action: "set", value: "Bearer up"},
		{name: "X-Key", action: "add", value: "b"},
	})
	var lines []string
	for _, h := range got {
		lines = append(lines, h.GetName()+"="+h.GetValue())
	}
	if want := "Accept=*/*,X-Key=a,Authorization=Bearer up,X-Key=b"; strings.Join(lines, ",") != want {
		t.Fatalf("headers = %s, want %s", strings.Join(lines, ","), want)
	}
}

func TestWithEdgeSecretHead(t *testing.T) {
	head := []byte("GET /ws HTTP/1.1\r\nHost: a.example.com\r\nAuthorization: client\r\nUpgrade: websocket\r\n\r\n")
	got, err := withEdgeSecretHead(head, []edgeSecretHeader{{name: "Authorization", action: "set", value: "Bearer up"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "GET /ws HTTP/1.1\r\nHost: a.example.com\r\nUpgrade: websocket\r\nAuthorization: Bearer up\r\n\r\n"; string(got) != want {
		t.Fatalf("head = %q, want %q", got, want)
	}
	if same, err := withEdgeSecretHead(head, nil); err != nil || string(same) != string(head) {
		t.Fatalf("no values = %q, %v", same, err)
	}
	for name, bad := range map[string]string{
		"partial head":  "GET / HTTP/1.1\r\nHost: a\r\n",
		"head and body": "GET / HTTP/1.1\r\nHost: a\r\n\r\nbody",
		"body chunk":    "hello",
	} {
		if _, err := withEdgeSecretHead([]byte(bad), []edgeSecretHeader{{name: "X", action: "add", value: "v"}}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

type fakeRawStream struct {
	edgeSecretHeadStream
	frames []*vmmdpb.ForwardRawRequest
}

func (f *fakeRawStream) Recv() (*vmmdpb.ForwardRawRequest, error) {
	if len(f.frames) == 0 {
		return nil, io.EOF
	}
	frame := f.frames[0]
	f.frames = f.frames[1:]
	return frame, nil
}

func TestEdgeSecretHeadStreamRewritesOnlyTheFirstFrame(t *testing.T) {
	chunk := func(b string) *vmmdpb.ForwardRawRequest {
		return &vmmdpb.ForwardRawRequest{Frame: &vmmdpb.ForwardRawRequest_BodyChunk{BodyChunk: []byte(b)}}
	}
	inner := &fakeRawStream{frames: []*vmmdpb.ForwardRawRequest{chunk("GET / HTTP/1.1\r\nHost: a\r\n\r\n"), chunk("frame-2\r\n\r\n")}}
	s := &edgeSecretHeadStream{BidiStreamingServer: inner, add: []edgeSecretHeader{{name: "X-Key", action: "add", value: "v"}}}
	first, err := s.Recv()
	if err != nil || string(first.GetBodyChunk()) != "GET / HTTP/1.1\r\nHost: a\r\nX-Key: v\r\n\r\n" {
		t.Fatalf("first = %q, %v", first.GetBodyChunk(), err)
	}
	second, err := s.Recv()
	if err != nil || string(second.GetBodyChunk()) != "frame-2\r\n\r\n" {
		t.Fatalf("second = %q, %v", second.GetBodyChunk(), err)
	}
	if _, err := s.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("end = %v", err)
	}
}

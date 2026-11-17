package vmmdgrpc

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"golang.org/x/net/http/httpguts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// EdgeHeaderSecretOpener opens one sealed edge-rule request header value for
// an instance this node runs (ADR-20261110210000 slice 3i). cmd/vmmd asks the
// control plane's secretd as this node, naming the instance's app.
type EdgeHeaderSecretOpener interface {
	OpenEdgeHeaderSecret(ctx context.Context, instance, secretAppID, secretID string) (string, error)
}

// WithEdgeHeaderSecrets lets the forwarders add sealed edge-rule request
// headers the gateway names by reference. Without it, a forward that names
// one is refused, so a rule is never applied without its credential.
func (s *Server) WithEdgeHeaderSecrets(o EdgeHeaderSecretOpener) *Server {
	if s != nil {
		s.edgeHeaderSecrets = o
	}
	return s
}

// maxEdgeSecretHeaders bounds the values one request may name; a headers
// rule carries a handful of ops.
const maxEdgeSecretHeaders = 32

// edgeSecretHeader is one opened value.
type edgeSecretHeader struct {
	name, action, value string
}

// openEdgeSecretHeaders opens every value a forward names. Any failure
// refuses the forward: the request never reaches the guest without the
// credential its rule adds.
func (s *Server) openEdgeSecretHeaders(ctx context.Context, instance string, ops []*vmmdpb.EdgeSecretHeader) ([]edgeSecretHeader, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	if len(ops) > maxEdgeSecretHeaders {
		return nil, status.Error(codes.InvalidArgument, "too many edge rule header values")
	}
	if s.edgeHeaderSecrets == nil {
		return nil, status.Error(codes.FailedPrecondition, "this vmmd does not open edge rule header values")
	}
	out := make([]edgeSecretHeader, 0, len(ops))
	for _, op := range ops {
		name, action := op.GetName(), op.GetAction()
		if !httpguts.ValidHeaderFieldName(name) || api.EdgeRuleHeaderNameForbidden(name) ||
			(action != "set" && action != "add") || op.GetSecretAppId() == "" || op.GetSecretId() == "" {
			clearEdgeSecretHeaders(out)
			return nil, status.Error(codes.InvalidArgument, "invalid edge rule header value reference")
		}
		value, err := s.edgeHeaderSecrets.OpenEdgeHeaderSecret(ctx, instance, op.GetSecretAppId(), op.GetSecretId())
		if err == nil && (value == "" || !httpguts.ValidHeaderFieldValue(value)) {
			err = errors.New("the opened value is not a valid header value")
		}
		if err != nil {
			clearEdgeSecretHeaders(out)
			s.log.Warn("vmmd: edge rule header value unavailable; refusing the request",
				"instance", instance, "secret_app_id", logsanitize.Field(op.GetSecretAppId()),
				"secret_id", logsanitize.Field(op.GetSecretId()), "err", err)
			return nil, status.Error(codes.Unavailable, "vmmd could not open an edge rule header value")
		}
		out = append(out, edgeSecretHeader{name: name, action: action, value: value})
	}
	return out, nil
}

func clearEdgeSecretHeaders(hs []edgeSecretHeader) {
	for i := range hs {
		hs[i] = edgeSecretHeader{}
	}
}

// withEdgeSecretHeaders adds opened values after the request's own headers:
// set replaces every value of the name, add appends one.
func withEdgeSecretHeaders(headers []*vmmdpb.Header, add []edgeSecretHeader) []*vmmdpb.Header {
	for _, h := range add {
		if h.action == "set" {
			kept := headers[:0]
			for _, existing := range headers {
				if !strings.EqualFold(existing.GetName(), h.name) {
					kept = append(kept, existing)
				}
			}
			headers = kept
		}
		headers = append(headers, &vmmdpb.Header{Name: h.name, Value: h.value})
	}
	return headers
}

var headEnd = []byte("\r\n\r\n")

// withEdgeSecretHead adds opened values to a raw request head, which must be
// exactly one whole head (the gateway sends it as the first body chunk).
func withEdgeSecretHead(head []byte, add []edgeSecretHeader) ([]byte, error) {
	if len(add) == 0 {
		return head, nil
	}
	if bytes.Index(head, headEnd) != len(head)-len(headEnd) {
		return nil, errors.New("the first raw frame is not one whole request head")
	}
	lines := strings.Split(string(head[:len(head)-len(headEnd)]), "\r\n")
	for _, h := range add {
		if h.action == "set" {
			kept := lines[:1]
			for _, line := range lines[1:] {
				name, _, _ := strings.Cut(line, ":")
				if !strings.EqualFold(strings.TrimSpace(name), h.name) {
					kept = append(kept, line)
				}
			}
			lines = kept
		}
		lines = append(lines, h.name+": "+h.value)
	}
	return []byte(strings.Join(lines, "\r\n") + string(headEnd)), nil
}

// edgeSecretHeadStream adds opened values to the raw request head, the first
// frame its forwarder receives.
type edgeSecretHeadStream struct {
	grpc.BidiStreamingServer[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]
	add  []edgeSecretHeader
	done bool
}

func (s *edgeSecretHeadStream) Recv() (*vmmdpb.ForwardRawRequest, error) {
	frame, err := s.BidiStreamingServer.Recv()
	if err != nil || s.done {
		return frame, err
	}
	s.done = true
	head, err := withEdgeSecretHead(frame.GetBodyChunk(), s.add)
	clearEdgeSecretHeaders(s.add)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &vmmdpb.ForwardRawRequest{Frame: &vmmdpb.ForwardRawRequest_BodyChunk{BodyChunk: head}}, nil
}

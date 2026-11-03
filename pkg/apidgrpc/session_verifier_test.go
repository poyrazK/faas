package apidgrpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/session"
)

// sessionAuthServer answers VerifySession with a canned response.
type sessionAuthServer struct {
	apidpb.UnimplementedAuthServer
	resp *apidpb.VerifySessionResponse
	err  error
}

func (s *sessionAuthServer) VerifySession(context.Context, *apidpb.VerifySessionRequest) (*apidpb.VerifySessionResponse, error) {
	return s.resp, s.err
}

func dialSessionAuth(t *testing.T, srv apidpb.AuthServer) *apidgrpc.AuthClientImpl {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	apidpb.RegisterAuthServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(func() { s.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return apidgrpc.NewAuthClient(conn)
}

// The verifier maps apid's answer onto the middleware's contract:
// session.ErrInvalid for a cookie apid did not open or that has expired,
// any other error when apid could not be asked (ADR-20261102191500).
func TestSessionVerifierAsksApid(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	envelope := func(expires time.Time) []byte {
		raw, err := json.Marshal(session.Envelope{AccountID: "acct-1", Sid: "sid-1", ExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for name, tc := range map[string]struct {
		srv         *sessionAuthServer
		wantAccount string
		wantInvalid bool
	}{
		"valid":       {srv: &sessionAuthServer{resp: &apidpb.VerifySessionResponse{Valid: true, Envelope: envelope(now.Add(time.Hour))}}, wantAccount: "acct-1"},
		"rejected":    {srv: &sessionAuthServer{resp: &apidpb.VerifySessionResponse{}}, wantInvalid: true},
		"expired":     {srv: &sessionAuthServer{resp: &apidpb.VerifySessionResponse{Valid: true, Envelope: envelope(now.Add(-time.Minute))}}, wantInvalid: true},
		"apid failed": {srv: &sessionAuthServer{err: status.Error(codes.Unavailable, "sessions are not configured")}},
		"old apid":    {srv: &sessionAuthServer{err: status.Error(codes.Unimplemented, "unknown method")}},
	} {
		t.Run(name, func(t *testing.T) {
			v := apidgrpc.SessionVerifier{Auth: dialSessionAuth(t, tc.srv), Now: func() time.Time { return now }}
			env, err := v.VerifyContext(t.Context(), "cookie")
			switch {
			case tc.wantAccount != "":
				if err != nil || env.AccountID != tc.wantAccount || env.Sid != "sid-1" {
					t.Fatalf("got %+v, %v; want %s", env, err, tc.wantAccount)
				}
			case tc.wantInvalid:
				if !errors.Is(err, session.ErrInvalid) {
					t.Fatalf("err = %v; want session.ErrInvalid", err)
				}
			default:
				if err == nil || errors.Is(err, session.ErrInvalid) {
					t.Fatalf("err = %v; want an error that is not session.ErrInvalid", err)
				}
			}
		})
	}
	if _, err := (apidgrpc.SessionVerifier{}).Verify("cookie"); err == nil || errors.Is(err, session.ErrInvalid) {
		t.Fatalf("Verify without a context = %v; want a refusal that is not session.ErrInvalid", err)
	}
}

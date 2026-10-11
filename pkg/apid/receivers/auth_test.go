// Unit tests for the apid Auth gRPC receiver (ADR-127 PR-D).
//
// Coverage:
//   - Valid token returns (account_id, plan) from the store.
//   - Empty token returns InvalidArgument without touching the store.
//   - Hash miss returns Unauthenticated.
//   - Expired key returns Unauthenticated (IAM-5 sentinel).
//   - Revoked key returns Unauthenticated (IAM-5 sentinel).
//   - Postgres error returns Internal.
//
// The fakeStore substitutes for state.Store without spinning a real
// Postgres pool — every test is hermetic.

package receivers

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeAuthStore is a hand-rolled fake for the authStore interface
// the receiver consumes. Tests populate the lookup function via the
// `lookup` field; nil lookup returns "hash miss" semantics.
type fakeAuthStore struct {
	lookup func(hash []byte) (state.Account, state.APIKey, error)
	calls  int
}

func (f *fakeAuthStore) AuthenticateKey(_ context.Context, hash []byte) (state.Account, state.APIKey, error) {
	f.calls++
	if f.lookup == nil {
		return state.Account{}, state.APIKey{}, state.ErrNotFound
	}
	return f.lookup(hash)
}

// dialAuthBufconn brings up an in-process gRPC server backed by a
// bufconn listener and returns a connected AuthClient. The listener
// is cleaned up by t.Cleanup.
func dialAuthBufconn(t *testing.T, store authStore) apidpb.AuthClient {
	t.Helper()
	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	RegisterAuthReceiver(srv, store)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("bufconn dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return apidpb.NewAuthClient(conn)
}

// TestAuthReceiver_HappyPath: known token → (account_id, plan) echo.
func TestAuthReceiver_HappyPath(t *testing.T) {
	acctID := uuid.New().String()
	store := &fakeAuthStore{
		lookup: func(hash []byte) (state.Account, state.APIKey, error) {
			return state.Account{ID: acctID, Plan: api.PlanHobby}, state.APIKey{}, nil
		},
	}
	cli := dialAuthBufconn(t, store)
	resp, err := cli.AuthenticateKey(context.Background(), &apidpb.AuthenticateKeyRequest{Token: "faas_live_hobby"})
	if err != nil {
		t.Fatalf("AuthenticateKey: %v", err)
	}
	if resp.GetAccountId() != acctID {
		t.Errorf("account_id = %q, want %q", resp.GetAccountId(), acctID)
	}
	if resp.GetPlan() != "hobby" {
		t.Errorf("plan = %q, want %q", resp.GetPlan(), "hobby")
	}
	if store.calls != 1 {
		t.Errorf("store.calls = %d, want 1", store.calls)
	}
}

// TestAuthReceiver_EmptyToken: empty token → InvalidArgument, store
// NOT called (load-bearing for the validator-first shape).
func TestAuthReceiver_EmptyToken(t *testing.T) {
	store := &fakeAuthStore{}
	cli := dialAuthBufconn(t, store)
	_, err := cli.AuthenticateKey(context.Background(), &apidpb.AuthenticateKeyRequest{Token: "   "})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
	if store.calls != 0 {
		t.Errorf("store.calls = %d, want 0", store.calls)
	}
}

// TestAuthReceiver_UnknownKey: hash miss → Unauthenticated.
func TestAuthReceiver_UnknownKey(t *testing.T) {
	store := &fakeAuthStore{
		lookup: func(hash []byte) (state.Account, state.APIKey, error) {
			return state.Account{}, state.APIKey{}, state.ErrNotFound
		},
	}
	cli := dialAuthBufconn(t, store)
	_, err := cli.AuthenticateKey(context.Background(), &apidpb.AuthenticateKeyRequest{Token: "faas_live_unknown"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

// TestAuthReceiver_ExpiredKey: ErrAPIKeyExpired → Unauthenticated.
// Mirrors pkg/auth/middleware/middleware.go:464-475 (IAM-5).
func TestAuthReceiver_ExpiredKey(t *testing.T) {
	store := &fakeAuthStore{
		lookup: func(hash []byte) (state.Account, state.APIKey, error) {
			return state.Account{}, state.APIKey{}, state.ErrAPIKeyExpired
		},
	}
	cli := dialAuthBufconn(t, store)
	_, err := cli.AuthenticateKey(context.Background(), &apidpb.AuthenticateKeyRequest{Token: "faas_live_expired"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

// TestAuthReceiver_RevokedKey: ErrAPIKeyRevoked → Unauthenticated.
func TestAuthReceiver_RevokedKey(t *testing.T) {
	store := &fakeAuthStore{
		lookup: func(hash []byte) (state.Account, state.APIKey, error) {
			return state.Account{}, state.APIKey{}, state.ErrAPIKeyRevoked
		},
	}
	cli := dialAuthBufconn(t, store)
	_, err := cli.AuthenticateKey(context.Background(), &apidpb.AuthenticateKeyRequest{Token: "faas_live_revoked"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

// TestAuthReceiver_InternalError: arbitrary store error → Internal.
// The gateway should log + drop (the request envelope was bad).
func TestAuthReceiver_InternalError(t *testing.T) {
	store := &fakeAuthStore{
		lookup: func(hash []byte) (state.Account, state.APIKey, error) {
			return state.Account{}, state.APIKey{}, errors.New("postgres down")
		},
	}
	cli := dialAuthBufconn(t, store)
	_, err := cli.AuthenticateKey(context.Background(), &apidpb.AuthenticateKeyRequest{Token: "faas_live_during_outage"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("code = %v, want Internal", got)
	}
}

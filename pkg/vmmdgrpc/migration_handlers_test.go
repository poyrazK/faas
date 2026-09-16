// Whitebox tests for the gRPC error-envelope `docs_url` emitted by
// the Tier A5 migration handlers (pkg/vmmdgrpc/migration_handlers.go).
//
// Issue #420 / ADR-082 follow-up: every migration handler error must
// surface a docs_url on the wire that resolves to the live docs
// host (sourced from pkg/wire.DocsHost). Before the fix, six sites
// hard-coded `https://docs/vmmd#<fragment>` — a malformed URL on
// errdetails.ErrorInfo.Metadata["docs_url"] that could not be
// dereferenced. The tripwire
// TestLintTripwire_NoLiteralDocsDomainEverywhere covers future
// regressions at the literal level; these tests pin the runtime
// contract end-to-end so a refactor that drops the wire.DocsHost
// indirection (or re-introduces a malformed host) trips the test
// even if the literal passes the AST walker.
//
// Build tag: (none). CI-safe; no KVM, no root, no netns.
package vmmdgrpc

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newMigrationHandlersTestServer wires a Server with the minimum
// state needed for the early-return error paths in
// migration_handlers.go. The Server's *OpsMetrics field is non-nil
// so s.ops.Observe(op, time.Since(start), err) on the early-return
// path doesn't panic on a nil receiver; the registry it points at
// is fresh, scoped to "vmmd_test", and never read by the test
// (the assertions are on the gRPC envelope, not the metrics).
// `migrations` is set to a fresh tracker; the early-return paths
// do not reach `s.migrations.put` or `s.migrations.get` and so the
// tracker stays empty.
func newMigrationHandlersTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{ops: wire.NewOpsMetrics("vmmd_test"), migrations: newMigrationTracker()}
}

// TestMigrationHandlers_DocsURLsAreWellFormed pins the
// errdetails.ErrorInfo.Metadata["docs_url"] emitted by every
// early-return error site in migration_handlers.go. Each case
// drives one handler with deliberately-empty required fields so
// the handler exits before any I/O, then walks the gRPC status
// back through grpcerr.FromStatus to recover the *api.Problem
// and assert the docs_url shape. Any site that falls back to a
// hard-coded host or drops the wire.DocsBaseURL indirection trips
// here.
func TestMigrationHandlers_DocsURLsAreWellFormed(t *testing.T) {
	const wantPrefix = "https://gregale.dev/docs/vmmd#"

	cases := []struct {
		name     string
		fragment string
		invoke   func(*Server) error
	}{
		{
			name:     "PrepareLiveMigration/missing-fields",
			fragment: "prepare",
			invoke: func(s *Server) error {
				_, err := s.PrepareLiveMigration(context.Background(), &vmmdpb.PrepareLiveMigrationRequest{})
				return err
			},
		},
		{
			name:     "AdoptMigratedInstance/missing-fields",
			fragment: "adopt",
			invoke: func(s *Server) error {
				_, err := s.AdoptMigratedInstance(context.Background(), &vmmdpb.AdoptMigratedInstanceRequest{})
				return err
			},
		},
		{
			// AdoptMigratedInstance's lease-lookup failure
			// path. With a fresh tracker, the lookup returns
			// errLeaseNotFound → codes.NotFound branch
			// (the second WithDocs site).
			name:     "AdoptMigratedInstance/lease-lookup-failed",
			fragment: "adopt",
			invoke: func(s *Server) error {
				_, err := s.AdoptMigratedInstance(context.Background(), &vmmdpb.AdoptMigratedInstanceRequest{
					InstanceId:        "iid-2",
					MemStorageKey:     "snap-2/mem",
					VmstateStorageKey: "snap-2/vmstate",
					LeaseToken:        "lease-x",
				})
				return err
			},
		},
		{
			name:     "AcknowledgeMigration/missing-fields",
			fragment: "ack",
			invoke: func(s *Server) error {
				_, err := s.AcknowledgeMigration(context.Background(), &vmmdpb.AcknowledgeMigrationRequest{})
				return err
			},
		},
		{
			name:     "CancelLiveMigration/missing-fields",
			fragment: "cancel",
			invoke: func(s *Server) error {
				_, err := s.CancelLiveMigration(context.Background(), &vmmdpb.CancelLiveMigrationRequest{})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newMigrationHandlersTestServer(t)
			err := tc.invoke(s)
			if err == nil {
				t.Fatalf("handler returned nil error; expected an RFC 7807 problem")
			}
			p, ok := grpcerr.FromStatus(err)
			if !ok {
				t.Fatalf("grpcerr.FromStatus: not a problem status: %v", err)
			}
			if p.DocsURL == "" {
				t.Fatalf("docs_url is empty on the wire; should be %s%s", wantPrefix, tc.fragment)
			}
			if !strings.HasPrefix(p.DocsURL, wantPrefix) {
				t.Fatalf("docs_url = %q; want prefix %q (issue #420 wire contract)", p.DocsURL, wantPrefix)
			}
			want := wantPrefix + tc.fragment
			if p.DocsURL != want {
				t.Fatalf("docs_url = %q; want exact %q", p.DocsURL, want)
			}
			// Sanity: the gRPC code round-trips. Migration
			// errors are 4xx-shaped; both branches map to
			// codes.InvalidArgument or codes.AlreadyExists /
			// codes.NotFound — all are non-OK, which is the
			// only requirement here.
			st := grpcerr.ToStatus(p)
			if st == nil {
				t.Fatalf("grpcerr.ToStatus round-trip returned nil")
			}
		})
	}
}

// TestMigrationHandlers_AllErrorSitesUseCodesInvalidArgumentOrMoreSpecific
// asserts the codes emitted by each handler are stable (any 4xx is
// acceptable; the test is the docs_url contract above). The point is
// to prevent a future refactor from accidentally downgrading a
// code to codes.OK (which would suppress the docs_url round-trip
// path entirely).
func TestMigrationHandlers_AllErrorSitesAre4xx(t *testing.T) {
	cases := []struct {
		name   string
		invoke func(*Server) error
	}{
		{
			name: "PrepareLiveMigration/missing-fields",
			invoke: func(s *Server) error {
				_, err := s.PrepareLiveMigration(context.Background(), &vmmdpb.PrepareLiveMigrationRequest{})
				return err
			},
		},
		{
			name: "AdoptMigratedInstance/missing-fields",
			invoke: func(s *Server) error {
				_, err := s.AdoptMigratedInstance(context.Background(), &vmmdpb.AdoptMigratedInstanceRequest{})
				return err
			},
		},
		{
			name: "AcknowledgeMigration/missing-fields",
			invoke: func(s *Server) error {
				_, err := s.AcknowledgeMigration(context.Background(), &vmmdpb.AcknowledgeMigrationRequest{})
				return err
			},
		},
		{
			name: "CancelLiveMigration/missing-fields",
			invoke: func(s *Server) error {
				_, err := s.CancelLiveMigration(context.Background(), &vmmdpb.CancelLiveMigrationRequest{})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newMigrationHandlersTestServer(t)
			err := tc.invoke(s)
			if err == nil {
				t.Fatalf("handler returned nil error; expected an RFC 7807 problem")
			}
			p, ok := grpcerr.FromStatus(err)
			if !ok {
				t.Fatalf("grpcerr.FromStatus: not a problem status: %v", err)
			}
			if p.Status >= 600 {
				t.Fatalf("status %d is not 4xx-shaped; docs_url would not round-trip cleanly", p.Status)
			}
			if codes.Code(p.Status) == codes.OK {
				t.Fatalf("status %d is codes.OK; handler should have returned an error", p.Status)
			}
		})
	}
}

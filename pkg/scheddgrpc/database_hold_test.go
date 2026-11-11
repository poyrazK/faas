// adr: 1027 — compute holds and quotas (amendment 2026-11-10): apid alone
// holds a database and sets its quota, and a refused wake carries its reason
// to pgproxyd.
package scheddgrpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbrefusal"
	"github.com/onebox-faas/faas/pkg/dbsched"
)

func TestHoldAndQuotaRPCs(t *testing.T) {
	s := New(nil, nil, nil).WithDatabases(&fakeDatabases{vm: dbsched.VM{DatabaseID: testDBID, State: dbsched.StateParked}})
	hold := func(c context.Context) (*scheddpb.DatabaseRuntime, error) {
		return s.HoldDatabase(c, &scheddpb.HoldDatabaseRequest{DatabaseId: testDBID, Held: true})
	}
	quota := func(c context.Context, q int64) (*scheddpb.DatabaseRuntime, error) {
		return s.SetDatabaseComputeQuota(c, &scheddpb.SetDatabaseComputeQuotaRequest{DatabaseId: testDBID, QuotaUnitSeconds: q})
	}
	if rt, err := hold(asCaller(callerAPID)); err != nil || !rt.GetHeld() {
		t.Fatalf("apid hold = %v, %v", rt, err)
	}
	if rt, err := quota(asCaller(callerAPID), 3600); err != nil || rt.GetComputeQuotaUnitSeconds() != 3600 {
		t.Fatalf("apid quota = %v, %v", rt, err)
	}
	if _, err := quota(asCaller(callerAPID), -1); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative quota = %v", err)
	}
	if _, err := hold(asCaller(callerPgproxy)); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("pgproxyd hold = %v, want PermissionDenied", err)
	}
	if _, err := quota(asCaller(callerPgproxy), 1); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("pgproxyd quota = %v, want PermissionDenied", err)
	}
}

func TestRefusedWakesCarryTheirReason(t *testing.T) {
	cases := []struct {
		err    error
		reason string
	}{
		{dbsched.ErrHeld, dbrefusal.Held},
		{dbsched.ErrComputeQuota, dbrefusal.ComputeQuota},
		{dbsched.ErrConflict, ""},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			s := New(nil, nil, nil).WithDatabases(&fakeDatabases{err: tc.err})
			_, err := s.WakeDatabase(asCaller(callerPgproxy), &scheddpb.DatabaseRef{DatabaseId: testDBID})
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition", status.Code(err))
			}
			if got := dbrefusal.Reason(err); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

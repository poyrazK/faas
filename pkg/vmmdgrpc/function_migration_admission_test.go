package vmmdgrpc

// adr: 1663

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type admittedSourceHandlerVMM struct {
	*functionMigrationHandlerVMM
	admittedCalls int
}

func (*admittedSourceHandlerVMM) FunctionMigrationSourceAdmissionRequired(string) bool { return true }

func (v *admittedSourceHandlerVMM) PrepareAdmittedFunctionMigrationSource(context.Context, fcvm.FunctionMigrationBinding, fcvm.SnapshotSpec,
	func(context.Context, runtimeadmission.Receipt) (runtimeadmission.FunctionMigrationGrant, error),
	func(context.Context, runtimeadmission.FunctionMigrationGrant) error) (fcvm.SnapshotInfo, error) {
	v.admittedCalls++
	return fcvm.SnapshotInfo{}, io.EOF // Refused before requesting durable permission.
}

func (v *admittedSourceHandlerVMM) RecoverAdmittedFunctionMigrationSource(context.Context, fcvm.FunctionMigrationBinding, runtimeadmission.FunctionMigrationGrant,
	func(context.Context, runtimeadmission.FunctionMigrationGrant) error) (bool, error) {
	v.admittedCalls++
	return false, io.EOF
}

func TestFunctionMigrationAdmittedHandlerRequiresSchedulerPeer(t *testing.T) {
	for _, unix := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing peer", true: "unix scheduler"}[unix], func(t *testing.T) {
			s, _, original, source := functionMigrationHandlerFixture(t, false)
			v := &admittedSourceHandlerVMM{functionMigrationHandlerVMM: original}
			ctx := t.Context()
			if unix {
				ctx = peer.NewContext(ctx, &peer.Peer{Addr: &net.UnixAddr{Name: "/tmp/schedd-test.sock", Net: "unix"}})
			}
			attempted, err := s.prepareAdmittedFunctionSource(ctx, &activeMigration{instanceID: source.InstanceID, leaseToken: source.LeaseToken}, fcvm.SnapshotSpec{}, source, v)
			if attempted || (!unix && (status.Code(err) != codes.Unauthenticated || v.admittedCalls != 0)) || (unix && (!errors.Is(err, io.EOF) || v.admittedCalls != 1)) {
				t.Fatal("capture bypassed peer boundary or fabricated registration", attempted, err)
			}
			s.vmm = v
			v.admittedCalls = 0
			_, err = s.recoverAdmittedFunctionSource(t.Context(), source)
			if err == nil || v.admittedCalls != 0 {
				t.Fatal("recovery without scheduler peer reached physical owner", err)
			}
		})
	}
}

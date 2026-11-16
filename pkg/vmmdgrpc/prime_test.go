// adr: 20261114153000 — vmmd's PrimeInstance runs a priming burst and maps
// refusals to stable codes; a vmmd without the capability answers
// Unimplemented.
package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type primeStubVMM struct {
	stubVMM
	got fcvm.PrimeOptions
	err error
}

func (p *primeStubVMM) Prime(_ context.Context, _ string, opts fcvm.PrimeOptions) (fcvm.PrimeReport, error) {
	p.got = opts
	if p.err != nil {
		return fcvm.PrimeReport{}, p.err
	}
	return fcvm.PrimeReport{Sent: 20, OK: 19, Duration: 1500 * time.Microsecond}, nil
}

func TestPrimeInstanceHandler(t *testing.T) {
	for _, tc := range []struct {
		name      string
		instance  string
		vmm       VmmdAPI
		wantCode  codes.Code
		wantProbe string
	}{
		{name: "primed", instance: "vm-a", vmm: &primeStubVMM{}, wantCode: codes.OK},
		{name: "missing instance", vmm: &primeStubVMM{}, wantCode: codes.InvalidArgument, wantProbe: api.CodeValidation},
		{name: "no capability", instance: "vm-a", vmm: stubVMM{}, wantCode: codes.Unimplemented, wantProbe: api.CodeNotImplemented},
		{name: "invalid path", instance: "vm-a", vmm: &primeStubVMM{err: fmt.Errorf("prime vm-a: %w", fcvm.ErrPrimeInvalidPath)},
			wantCode: codes.InvalidArgument, wantProbe: api.CodeValidation},
		{name: "not live", instance: "vm-a", vmm: &primeStubVMM{err: errors.New("prime vm-a: instance is not live")}, wantCode: codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.vmm, nil, "1.17.0", nil)
			resp, err := s.PrimeInstance(t.Context(), &vmmdpb.PrimeInstanceRequest{Instance: tc.instance, Path: "/work", Requests: 20, BudgetMillis: 5000})
			if status.Code(err) != tc.wantCode {
				t.Fatalf("PrimeInstance err = %v, want %v", err, tc.wantCode)
			}
			if tc.wantProbe != "" && problemCode(t, err) != tc.wantProbe {
				t.Fatalf("problem code = %q, want %q", problemCode(t, err), tc.wantProbe)
			}
			if tc.wantCode != codes.OK {
				return
			}
			if got := tc.vmm.(*primeStubVMM).got; got != (fcvm.PrimeOptions{Path: "/work", Requests: 20, Budget: 5 * time.Second}) {
				t.Fatalf("prime options = %+v", got)
			}
			if resp.GetSent() != 20 || resp.GetOk() != 19 || resp.GetDurationMicros() != 1500 {
				t.Fatalf("response = %+v", resp)
			}
		})
	}
}

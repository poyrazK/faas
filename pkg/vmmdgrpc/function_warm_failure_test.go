package vmmdgrpc

// adr: 1544

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFunctionResourceWarmFailureCannotBecomeHealthyRefusal(t *testing.T) {
	for _, cause := range []error{fcvm.ErrInstanceBusy, fcvm.ErrAppAdmissionFenced, runtimeadmission.ErrUnavailable, errors.New("private-driver-value")} {
		failure := errors.Join(fcvm.ErrFunctionWarmCapture, cause)
		for _, err := range []error{grpcerr.ToStatus(toProblem(failure)), admissionStatus(failure)} {
			if status.Code(err) != codes.Internal || problemCode(t, err) != api.CodeInternal || strings.Contains(err.Error(), "private-driver-value") {
				t.Fatalf("post-checkpoint failure became a healthy refusal or leaked diagnostics: %v", err)
			}
		}
	}
	if err := admissionStatus(fcvm.ErrInstanceBusy); problemCode(t, err) != api.CodeInstanceBusy {
		t.Fatal("ordinary idle admission refusal lost its recoverable busy code", err)
	}
}

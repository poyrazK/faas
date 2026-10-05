// adr: 468
package vmmdgrpc

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAppAdmissionProblemHasStableSafeCode(t *testing.T) {
	for _, lift := range []func(error) *api.Problem{toProblem, appTaskProblem} {
		problem := lift(fmt.Errorf("restore: %w", fcvm.ErrAppAdmissionFenced))
		wireErr := grpcerr.ToStatus(problem)
		if status.Code(wireErr) != codes.FailedPrecondition {
			t.Fatalf("fenced RPC status = %v", wireErr)
		}
		roundTrip, ok := grpcerr.FromStatus(wireErr)
		if !ok || roundTrip.Code != api.CodeDatabaseCutoverFenced || roundTrip.Status != 409 {
			t.Fatalf("fenced error round trip = %#v", roundTrip)
		}
		problem = lift(errors.Join(fcvm.ErrAppAdmissionUnavailable, errors.New("private DSN and password")))
		wireErr = grpcerr.ToStatus(problem)
		roundTrip, ok = grpcerr.FromStatus(wireErr)
		if status.Code(wireErr) != codes.Unavailable || !ok || roundTrip.Status != 503 || roundTrip.Code != api.CodeAppAdmissionUnavailable {
			t.Fatalf("admission read failure = %v", wireErr)
		}
		if strings.Contains(problem.Detail, "private") || strings.Contains(problem.Detail, "password") {
			t.Fatal("admission read error crossed the safe RPC boundary")
		}
	}
}

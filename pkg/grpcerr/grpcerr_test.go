// Tests for grpcerr. Round-trip every Code*, the limit/observed/docs
// metadata, and the edge cases (nil, unknown code, foreign error).

package grpcerr_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatus_NilIsNil(t *testing.T) {
	if err := grpcerr.ToStatus(nil); err != nil {
		t.Fatalf("nil Problem must produce nil error, got %v", err)
	}
}

func TestFromStatus_NilIsNil(t *testing.T) {
	if p, ok := grpcerr.FromStatus(nil); !ok || p != nil {
		t.Fatalf("nil error must produce (nil, true), got (%v, %v)", p, ok)
	}
}

func TestRoundTrip_StableCodes(t *testing.T) {
	cases := []struct {
		code  string
		grpc  codes.Code
		title string
	}{
		{api.CodePlanLimitRAM, codes.ResourceExhausted, "Plan RAM cap"},
		{api.CodePlanLimitApps, codes.ResourceExhausted, "Plan apps cap"},
		{api.CodeValidation, codes.InvalidArgument, "Validation"},
		{api.CodeNotFound, codes.InvalidArgument, "Not found"},
		{api.CodeUnauthorized, codes.PermissionDenied, "Unauthorized"},
		{api.CodeCapacity, codes.ResourceExhausted, "No capacity"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			p := api.NewProblem(int(tc.grpc), tc.code, tc.title, "we hit the cap").
				WithLimit(42, 50).
				WithDocs("https://docs/DOMAIN/plans")
			err := grpcerr.ToStatus(p)
			if err == nil {
				t.Fatalf("ToStatus produced nil")
			}
			if !grpcerr.IsCode(err, tc.grpc) {
				t.Fatalf("code = %v, want %v", status.Code(err), tc.grpc)
			}

			got, ok := grpcerr.FromStatus(err)
			if !ok {
				t.Fatalf("FromStatus did not recognise our own error: %v", err)
			}
			if got.Code != tc.code {
				t.Errorf("code round-trip: %q → %q", tc.code, got.Code)
			}
			if got.DocsURL != "https://docs/DOMAIN/plans" {
				t.Errorf("docs_url round-trip: %q", got.DocsURL)
			}
			if got.Limit == nil || *got.Limit != 42 {
				t.Errorf("limit round-trip: %v", got.Limit)
			}
			if got.Observed == nil || *got.Observed != 50 {
				t.Errorf("observed round-trip: %v", got.Observed)
			}
		})
	}
}

func TestRoundTrip_UnknownCodeFallsThroughToInternal(t *testing.T) {
	p := api.NewProblem(500, "definitely_new_code", "T", "D")
	err := grpcerr.ToStatus(p)
	if !grpcerr.IsCode(err, codes.Internal) {
		t.Fatalf("unknown code should map to Internal; got %v", status.Code(err))
	}
}

func TestFromStatus_ForeignError(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"plain", errors.New("plain error")},
		{"not_status", errors.New("not a status either")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ours := grpcerr.FromStatus(tc.err)
			if ours {
				t.Fatalf("foreign error must not be marked as ours")
			}
			if p != nil {
				t.Fatalf("foreign error should yield (nil, false); got (%v, false)", p)
			}
		})
	}

	// Status from a foreign gRPC error: Method with no ErrorInfo in details.
	st := status.New(codes.Unauthenticated, "third-party token expired")
	got, ours := grpcerr.FromStatus(st.Err())
	if ours {
		t.Fatalf("foreign status must not be marked as ours")
	}
	if got == nil || got.Code != "internal" {
		t.Fatalf("foreign status should yield a synthetic Problem with Code=internal; got %+v", got)
	}
	if got.Title == "" {
		t.Fatalf("foreign status should carry the status message as Title")
	}
}

func TestDetailsAttach(t *testing.T) {
	p := api.NewProblem(int(codes.ResourceExhausted), api.CodePlanLimitRAM,
		"RAM cap", "Hobby plan caps at 256 MiB; requested 512.").
		WithLimit(256, 512).
		WithDocs("https://docs/DOMAIN/plans#ram")
	err := grpcerr.ToStatus(p)

	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("not a status")
	}

	// Find an ErrorInfo among the details.
	var ei *errdetails.ErrorInfo
	for _, det := range st.Details() {
		if x, ok := det.(*errdetails.ErrorInfo); ok {
			ei = x
		}
	}
	if ei == nil {
		t.Fatalf("expected *ErrorInfo in details, got %T", st.Details()[0])
	}
	if ei.Reason != api.CodePlanLimitRAM {
		t.Errorf("reason = %q, want %q", ei.Reason, api.CodePlanLimitRAM)
	}
	if ei.Metadata["docs_url"] != "https://docs/DOMAIN/plans#ram" {
		t.Errorf("docs_url = %q", ei.Metadata["docs_url"])
	}
	if ei.Metadata["limit"] != "256" {
		t.Errorf("limit metadata = %q", ei.Metadata["limit"])
	}
	if ei.Metadata["observed"] != "512" {
		t.Errorf("observed metadata = %q", ei.Metadata["observed"])
	}
}

func TestNew_Convenience(t *testing.T) {
	err := grpcerr.New(codes.ResourceExhausted, api.CodeCapacity,
		"No slot", "Box at MaxSlots")
	if !grpcerr.IsCode(err, codes.ResourceExhausted) {
		t.Fatalf("code mismatch: %v", status.Code(err))
	}
	p, ok := grpcerr.FromStatus(err)
	if !ok || p.Code != api.CodeCapacity {
		t.Fatalf("FromStatus failed: %v %v", p, ok)
	}
}

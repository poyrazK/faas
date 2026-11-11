// Package dbrefusal names why schedd refuses to wake a native database
// (ADR-1027 amendment 2026-11-10): schedd's database scheduler sets the
// reason and pgproxyd reads it, without either importing the other.
package dbrefusal

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The ErrorInfo reasons (domain Domain) on a refused wake's
// FAILED_PRECONDITION.
const (
	Domain = "schedd.faas"
	// Held: apid holds the database (its delete grace period, or its
	// account's compute cap).
	Held = "database_held"
	// ComputeQuota: the database's compute this month reached its quota.
	ComputeQuota = "database_compute_quota"
)

// Status is the gRPC status of a wake refused for reason.
func Status(op, reason string) error {
	st := status.New(codes.FailedPrecondition, "database "+op+": "+reason)
	if detailed, derr := st.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: Domain}); derr == nil {
		st = detailed
	}
	return st.Err()
}

// Reason returns why schedd refused to wake a database, or "" for any
// other error.
func Reason(err error) string {
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		return ""
	}
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok && info.GetDomain() == Domain {
			return info.GetReason()
		}
	}
	return ""
}

// Package grpcerr bridges the platform's RFC 7807 error envelope
// (pkg/api/errors.go: Problem) and google.golang.org/grpc/status so that
// gRPC handlers can return the same stable Codes + Limit/Observed/DocsURL
// semantics that apid's REST surface already sends. ADR-013 names this
// adapter explicitly — every handler in pkg/vmmdgrpc routes its error
// returns through ToStatus and (for client-side demux) FromStatus.
//
// What's preserved across the wire:
//   - status.Code    (InvalidArgument / ResourceExhausted / NotFound / etc.)
//   - status.Message (human-readable; the platform's *Problem.Detail)
//   - status.Details (one google.protobuf.Struct carrying 'code', 'limit',
//     'observed', 'docs_url' — the four extra RFC 7807
//     fields the SPEC says every limit error must carry).
//
// What's NOT preserved:
//   - *Problem.Title (we encode in the message; clients display their own
//     title based on Code).
package grpcerr

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// codeToGRPC translates the platform's stable Code* strings (api/errors.go) to
// google.rpc.Code. Codes we don't model fall through to Internal; if a caller
// needs a new Code-to-Code mapping they MUST add it here so all surfaces
// (HTTP + gRPC) stay aligned.
func codeToGRPC(code string) codes.Code {
	switch code {
	case api.CodePlanLimitApps,
		api.CodePlanLimitRAM,
		api.CodePlanLimitConcur,
		api.CodeSourceTooLarge,
		api.CodeAppLayerTooBig,
		api.CodeQuotaExhausted,
		api.CodeCapacity:
		return codes.ResourceExhausted
	case api.CodeBuildUndetected,
		api.CodeValidation,
		api.CodeNotFound:
		return codes.InvalidArgument
	case api.CodeBuildOOM,
		api.CodeBuildTimeout:
		return codes.ResourceExhausted
	case api.CodeBillingPastDue,
		api.CodeUnauthorized:
		return codes.PermissionDenied
	default:
		return codes.Internal
	}
}

// ToStatus wraps a Problem into a google.rpc.Status. The wire shape:
//
//	status.code    = grpc code from codeToGRPC(p.Code)
//	status.message = p.Title: p.Detail
//	status.details = ErrorInfo{...}
//
// ErrorInfo is the standard google.rpc detail type for structured code/limit
// fields — every gRPC ecosystem (incl. grpc-gateway, postman, grpcurl) knows
// to render it. We use it instead of Struct for forward compatibility.
func ToStatus(p *api.Problem) error {
	if p == nil {
		return nil
	}

	st := status.New(codeToGRPC(p.Code), fmt.Sprintf("%s: %s", p.Title, p.Detail))
	ei := &errdetails.ErrorInfo{
		Reason: p.Code,
		// Metadata survives the round-trip; values are string-coerced because
		// ErrorInfo.Metadata only allows string values (rationale: this is a
		// generic error envelope; the Limit/Observed int64s lose two bits of
		// precision in the worst case but never more than Int63).
		Metadata: map[string]string{},
	}
	if p.DocsURL != "" {
		ei.Metadata["docs_url"] = p.DocsURL
	}
	if p.Limit != nil {
		ei.Metadata["limit"] = fmt.Sprintf("%d", *p.Limit)
	}
	if p.Observed != nil {
		ei.Metadata["observed"] = fmt.Sprintf("%d", *p.Observed)
	}

	withDetails, err := st.WithDetails(ei)
	if err != nil {
		// Failed to attach details; degrade gracefully to a plain status — the
		// platform's Code+Title still travels, the structured fields are lost.
		return st.Err()
	}
	return withDetails.Err()
}

// FromStatus is the inverse of ToStatus.
//
// Returns:
//   - (nil, true) for nil err.
//   - (nil, false) for errs that aren't *status.Status (e.g. plain errors).
//   - (*api.Problem, true) for Problems we produced — Code + Limit + Observed
//   - DocsURL populated from ErrorInfo.
//   - (synthetic Problem with Code="internal", false) for *status.Status from
//     outside this package — the caller's discriminator is the second return.
func FromStatus(err error) (*api.Problem, bool) {
	if err == nil {
		return nil, true
	}

	st, ok := status.FromError(err)
	if !ok {
		return nil, false
	}

	p := &api.Problem{
		Title: st.Message(),
	}

	for _, det := range st.Details() {
		ei, ok := det.(*errdetails.ErrorInfo)
		if !ok {
			continue
		}
		p.Code = ei.Reason
		if v, ok := ei.Metadata["docs_url"]; ok {
			p.DocsURL = v
		}
		if v, ok := ei.Metadata["limit"]; ok {
			var i int64
			if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
				p.Limit = &i
			}
		}
		if v, ok := ei.Metadata["observed"]; ok {
			var i int64
			if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
				p.Observed = &i
			}
		}
	}

	if p.Code == "" {
		// *status.Status from outside this package (no ErrorInfo). Caller
		// distinguishes via the second return; we still give them a uniform
		// Problem-shaped object rather than letting gRPC status leak.
		return &api.Problem{
			Code:   "internal",
			Status: http.StatusInternalServerError,
			Title:  st.Message(),
			Detail: st.Message(),
		}, false
	}

	// Recover the HTTP status from the stable Code so HTTP surfaces that render
	// the lifted Problem verbatim (gatewayd's wake-denial path → WriteProblem)
	// emit the right status rather than WriteHeader(0). The gRPC code is lossy
	// (both 429 and 503 map to ResourceExhausted), so we key off the Code.
	p.Status = api.StatusForCode(p.Code)
	return p, true
}

// New builds an error compatible with this package from a few primitives.
// Convenience for handlers that synthesise error responses inline rather
// than constructing a *api.Problem first.
func New(c codes.Code, code, title, detail string) error {
	return ToStatus(&api.Problem{
		Title:  title,
		Status: int(c),
		Code:   code,
		Detail: detail,
	})
}

// IsCode is the predicate equivalent of status.Code(err) == c when err may
// already be a wrapped chain. Useful in handler tests.
func IsCode(err error, c codes.Code) bool {
	if err == nil {
		return c == codes.OK
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == c
}

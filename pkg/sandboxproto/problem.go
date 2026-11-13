package sandboxproto

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// guestRefusals maps each guest refusal to its HTTP status and problem code
// (ADR-20261027090013 step 4).
var guestRefusals = map[string]struct {
	status int
	code   string
}{
	CodeInvalidRequest:   {http.StatusBadRequest, api.CodeValidation},
	CodeNotFound:         {http.StatusNotFound, api.CodeSandboxEntryNotFound},
	CodeExists:           {http.StatusConflict, api.CodeSandboxPathExists},
	CodeOutsideWorkspace: {http.StatusForbidden, api.CodeSandboxOutsideWorkspace},
	CodeNotADirectory:    {http.StatusBadRequest, api.CodeSandboxNotADirectory},
	CodeIsADirectory:     {http.StatusBadRequest, api.CodeSandboxIsADirectory},
	CodeNotEmpty:         {http.StatusConflict, api.CodeSandboxDirectoryNotEmpty},
	CodeTooLarge:         {http.StatusRequestEntityTooLarge, api.CodeSandboxTooLarge},
	CodeLimit:            {http.StatusTooManyRequests, api.CodeSandboxBusy},
	CodeUnsupported:      {http.StatusUnprocessableEntity, api.CodeSandboxUnsupported},
	CodeStartFailed:      {http.StatusUnprocessableEntity, api.CodeSandboxCommandStartFailed},
	CodePortClosed:       {http.StatusBadGateway, api.CodeSandboxPortClosed},
}

// Problem maps a guest's error frame to the platform's problem. A guest
// failure that is not a refusal is the platform's, and reports nothing of
// the guest's message.
func Problem(e ErrorFrame) *api.Problem {
	r, ok := guestRefusals[e.Code]
	if !ok {
		return api.NewProblem(http.StatusBadGateway, api.CodeInternal, "Sandbox failed", "the session could not complete the call")
	}
	return api.NewProblem(r.status, r.code, "Sandbox refused the call", e.Message)
}

// ProblemStatus is the HTTP status of a problem code Problem produces, for
// a problem lifted from gRPC, which carries the code but not the status.
func ProblemStatus(code string) (int, bool) {
	for _, r := range guestRefusals {
		if r.code == code && code != api.CodeValidation {
			return r.status, true
		}
	}
	return 0, false
}

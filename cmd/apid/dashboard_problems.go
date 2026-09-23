package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// writeDashboardUnauthorized is the defensive fallback for handlers that are
// normally protected by sessionAuth. A missing principal should send the
// customer back through sign-in, not expose a bare "unauthorized" response.
func writeDashboardUnauthorized(w http.ResponseWriter, r *http.Request) {
	api.WriteProblemForRequest(w, r, api.NewProblem(
		http.StatusUnauthorized,
		api.CodeUnauthorized,
		"Sign in required",
		"Your dashboard session is missing or has expired.",
	).WithHint("Sign in again, then retry this action.").WithDocs("https://gregale.dev/docs/auth/sign-in"))
}

// writeDashboardBadRequest gives malformed path/form fallbacks a stable code
// and a safe recovery step. Callers should keep using feature-specific
// validation problems when they can name the exact field that failed.
func writeDashboardBadRequest(w http.ResponseWriter, r *http.Request, detail string) {
	api.WriteProblemForRequest(w, r, api.NewProblem(
		http.StatusBadRequest,
		api.CodeBadRequest,
		"Invalid dashboard request",
		detail,
	).WithHint("Return to the dashboard and try the action again."))
}

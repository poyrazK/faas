package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
)

// agentPreviewGuard lets a budget preview (ADR-20261111114059, amendment
// 2026-11-13) reach only routes whose handlers price every accepted request
// before writing; anywhere else the header could be ignored and the change
// made, so it is refused. TestAgentBudgetPreviewWritesNothing keeps each
// previewable route answering at its price.
func agentPreviewGuard(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := r.Header.Get(api.AgentPreviewHeader)
		if value == "" {
			next.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		switch {
		case value != api.AgentPreviewBudget:
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Unsupported preview",
				api.AgentPreviewHeader+" accepts only "+api.AgentPreviewBudget))
		case !agentapproval.Previewable(pattern):
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Preview not available",
				"this route cannot be previewed; send the request without "+api.AgentPreviewHeader))
		case r.Header.Get("Idempotency-Key") != "":
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Preview not available",
				"a preview takes no Idempotency-Key"))
		default:
			next.ServeHTTP(w, r)
		}
	})
}

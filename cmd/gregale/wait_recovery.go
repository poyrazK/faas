package main

import (
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// Wait commands retain their own exit contracts. Their diagnostics share the
// Problem envelope, correlation handling and optional resource-specific recovery.
// Callers apply diagnosticProblem before encoding server or local error text.
func writeJSONWaitProblem[T any](w io.Writer, problem api.Problem, code int, recovery *T) error {
	return json.NewEncoder(w).Encode(struct {
		api.Problem
		Recovery  *T     `json:"recovery,omitempty"`
		Category  string `json:"category"`
		ExitCode  int    `json:"exit_code"`
		RequestID string `json:"request_id,omitempty"`
	}{Problem: problem, Recovery: recovery, Category: exitCategory(code), ExitCode: code, RequestID: safeProblemRequestID(problem)})
}

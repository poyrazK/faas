package main

import (
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) ingestIssueOTLP(w http.ResponseWriter, r *http.Request) {
	st, ok := s.store.(state.IssueStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("issues persistence is unavailable"))
		return
	}
	credential, acct, ok := s.authenticateIssueIngest(w, r, st)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("OTLP exception export exceeds the body limit"))
		return
	}
	batch, err := issues.ExtractOTLP(raw, r.PathValue("signal"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	inputs, err := prepareIssueOTLP(batch, credential, acct, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	for _, input := range inputs {
		out, err := st.RecordIssue(r.Context(), input)
		s.observeIssueEvent(out, err)
		if err != nil {
			writeIssueError(w, err)
			return
		}
	}
	// Empty ExportTraceServiceResponse / ExportLogsServiceResponse in OTLP JSON.
	writeJSON(w, http.StatusOK, struct{}{})
}

func prepareIssueOTLP(batch []api.IssueEvent, credential state.IssueCredential, acct state.Account, now time.Time) ([]state.RecordIssueParams, error) {
	// Validate the whole batch before writing. A transient failure after a
	// partial commit is safe to retry because every occurrence has a stable ID.
	inputs := make([]state.RecordIssueParams, 0, len(batch))
	for _, event := range batch {
		normalized, fp, title, err := issues.Normalize(event, now, acct.Plan.IssueLimits())
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, state.RecordIssueParams{Credential: credential, Event: normalized, Fingerprint: fp, Title: title, PayloadHash: issues.PayloadDigest(event), GroupingVersion: issues.GroupingVersion, Limits: acct.Plan.IssueLimits(), Now: now})
	}
	return inputs, nil
}

package apidserver

import (
	"errors"
	"io"
	"net/http"
	"strconv"
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
	signal := r.PathValue("signal")
	groups, err := issues.ExtractOTLPGroups(raw, signal)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	now := time.Now().UTC()
	var prepared [][]state.RecordIssueParams
	rejected := 0
	for _, group := range groups {
		if group.Err != nil {
			rejected++
			s.ops.ObserveIssueEvent("invalid")
			continue
		}
		items, prepareErr := prepareIssueOTLP(group.Events, credential, acct, now)
		if prepareErr != nil {
			rejected++
			s.ops.ObserveIssueEvent("invalid")
			continue
		}
		prepared = append(prepared, items)
	}
	batchStore, ok := st.(state.IssueBatchStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("atomic issue batch persistence is unavailable"))
		return
	}
	results, storeRejected, err := issues.DeliverOTLPGroups(r.Context(), prepared, batchStore.RecordIssueBatch, permanentIssueOTLPError, splittableIssueOTLPError, func(err error) {
		if errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrNotFound) {
			s.ops.ObserveIssueEvent("invalid")
		} else {
			s.observeIssueEvent(api.IssueEventResponse{}, err)
		}
	})
	rejected += storeRejected
	for _, out := range results {
		s.observeIssueEvent(out, nil)
	}
	if err != nil {
		s.observeIssueEvent(api.IssueEventResponse{}, err)
		writeIssueError(w, err)
		return
	}
	if rejected != 0 {
		field := "rejectedLogRecords"
		if signal == "traces" {
			field = "rejectedSpans"
		}
		writeJSON(w, http.StatusOK, map[string]any{"partialSuccess": map[string]string{
			field: strconv.Itoa(rejected), "errorMessage": "some exception records could not be accepted",
		}})
		return
	}
	// Empty ExportTraceServiceResponse / ExportLogsServiceResponse in OTLP JSON.
	writeJSON(w, http.StatusOK, struct{}{})
}

func splittableIssueOTLPError(err error) bool {
	var limit *state.IssueLimitError
	return errors.Is(err, state.ErrIssueSourceMapsUnavailable) || errors.As(err, &limit) && (limit.Rate || limit.Resource == "events")
}

func permanentIssueOTLPError(err error) bool {
	var limit *state.IssueLimitError
	if errors.As(err, &limit) {
		return !limit.Rate && limit.Resource != "events"
	}
	return errors.Is(err, state.ErrIssueEventConflict) || errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrNotFound)
}

func prepareIssueOTLP(batch []api.IssueEvent, credential state.IssueCredential, acct state.Account, now time.Time) ([]state.RecordIssueParams, error) {
	// Validate the whole batch before the store commits it atomically.
	inputs := make([]state.RecordIssueParams, 0, len(batch))
	for _, event := range batch {
		normalized, fp, title, err := issues.Normalize(event, now, acct.Plan.IssueLimits())
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, state.RecordIssueParams{Credential: credential, Event: normalized, Fingerprint: fp, Title: title, PayloadHash: issues.PayloadDigest(event), GroupingVersion: issues.GroupingVersionFor(normalized), Limits: acct.Plan.IssueLimits(), Now: now, EnforceResourcePolicy: true})
	}
	return inputs, nil
}

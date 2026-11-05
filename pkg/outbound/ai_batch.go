package outbound

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// AIBatch is a provider batch the gateway reserved for (ADR-1431). Path and
// RawQuery are the creation call's escaped provider path and query; status
// and results reads are built from them.
type AIBatch struct {
	ID                     string
	IntegrationID          string
	AccountID              string
	Caller                 string
	BindingAppID           string
	Provider               string
	ProviderBatchID        string
	Path                   string
	RawQuery               string
	Model                  string
	Requests               int
	ReservedTokens         int64
	ReservedCostMillicents int64
	CreatedAt              time.Time
}

// AIBatchBackend keeps a batch's reservation in admission until it settles.
type AIBatchBackend interface {
	// ReserveAIBatch stores a pending batch before its provider call.
	ReserveAIBatch(ctx context.Context, batch AIBatch) error
	// ConfirmAIBatch opens a pending batch with the provider's batch ID.
	ConfirmAIBatch(ctx context.Context, id, providerBatchID string) error
	// DropAIBatch deletes a pending batch the provider did not create.
	DropAIBatch(ctx context.Context, id string) error
	// ClaimAIBatches returns open batches due for a check and pushes each
	// back by the poll interval; it also deletes expired pending batches.
	ClaimAIBatches(ctx context.Context, limit int) ([]AIBatch, error)
	// SettleAIBatch ends a batch and releases its reservation.
	SettleAIBatch(ctx context.Context, id string, outcome AIUsageOutcome, tokens, costMillicents int64) error
}

// batchEstimate is a batch's reservation: its requests' input bytes as
// tokens plus their output ceilings, priced at the batch price.
type batchEstimate struct {
	requests int
	tokens   int64
	cost     int64
	models   map[string]int
}

func (e *batchEstimate) add(integration Integration, model string, bodyBytes int, outputCeiling int64) {
	if e.models == nil {
		e.models = map[string]int{}
	}
	e.requests++
	input := (int64(bodyBytes) + api.OutboundAIReserveBytesPerToken - 1) / api.OutboundAIReserveBytesPerToken
	e.tokens += input + outputCeiling
	model = sanitizeAIModel(model)
	if model == "" {
		model = "unknown"
	}
	e.models[model]++
	e.cost += batchCost(aiCallCostMillicents(integration, AIUsage{Model: model, Known: true, InputTokens: input, OutputTokens: outputCeiling}))
}

// model is the batch's most frequent model.
func (e batchEstimate) model() string {
	best, count := "unknown", 0
	for model, n := range e.models {
		if n > count || (n == count && model < best) {
			best, count = model, n
		}
	}
	return best
}

// batchCost is a list-price cost at the batch price, rounded up.
func batchCost(millicents int64) int64 {
	return (millicents*api.OutboundAIBatchPricePercent + 99) / 100
}

// batchRequestBody names the fields of one request body that size its
// reservation.
type batchRequestBody struct {
	Model               string `json:"model"`
	MaxTokens           *int64 `json:"max_tokens"`
	MaxCompletionTokens *int64 `json:"max_completion_tokens"`
	MaxOutputTokens     *int64 `json:"max_output_tokens"`
}

func (b batchRequestBody) outputCeiling(generation bool) int64 {
	if !generation {
		return 0
	}
	for _, ceiling := range []*int64{b.MaxCompletionTokens, b.MaxOutputTokens, b.MaxTokens} {
		if ceiling != nil && *ceiling > 0 {
			return *ceiling
		}
	}
	return api.OutboundAIDefaultOutputReserveTokens
}

// estimateAnthropicBatch reads the inline requests of a Message Batches
// creation body.
func estimateAnthropicBatch(integration Integration, body []byte) (batchEstimate, bool) {
	var batch struct {
		Requests []struct {
			Params json.RawMessage `json:"params"`
		} `json:"requests"`
	}
	if json.Unmarshal(body, &batch) != nil || len(batch.Requests) == 0 {
		return batchEstimate{}, false
	}
	var estimate batchEstimate
	for _, request := range batch.Requests {
		var params batchRequestBody
		if json.Unmarshal(request.Params, &params) != nil {
			return batchEstimate{}, false
		}
		estimate.add(integration, params.Model, len(request.Params), params.outputCeiling(true))
	}
	return estimate, true
}

// batchLineEstimator sizes an OpenAI batch input file as it streams by, one
// JSONL line at a time. A line longer than OutboundAIBatchLineMaxBytes is
// sized by its bytes with the default output reserve.
type batchLineEstimator struct {
	integration Integration
	estimate    batchEstimate
	line        []byte
	longLine    int
	seen        int64
	invalid     bool
}

func (e *batchLineEstimator) Write(p []byte) (int, error) {
	written := len(p)
	e.seen += int64(len(p))
	if e.seen > api.OutboundAIBatchInputMaxBytes {
		return 0, errBatchInputTooLarge
	}
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		chunk := p
		if end >= 0 {
			chunk = p[:end]
		}
		if e.longLine > 0 || len(e.line)+len(chunk) > api.OutboundAIBatchLineMaxBytes {
			e.longLine += len(e.line) + len(chunk)
			e.line = e.line[:0]
		} else {
			e.line = append(e.line, chunk...)
		}
		if end < 0 {
			break
		}
		e.endLine()
		p = p[end+1:]
	}
	return written, nil
}

func (e *batchLineEstimator) endLine() {
	defer func() { e.line, e.longLine = e.line[:0], 0 }()
	if e.longLine > 0 {
		e.estimate.add(e.integration, "", e.longLine, api.OutboundAIDefaultOutputReserveTokens)
		return
	}
	trimmed := bytes.TrimSpace(e.line)
	if len(trimmed) == 0 {
		return
	}
	var request struct {
		URL  string          `json:"url"`
		Body json.RawMessage `json:"body"`
	}
	var body batchRequestBody
	if json.Unmarshal(trimmed, &request) != nil || json.Unmarshal(request.Body, &body) != nil {
		e.invalid = true
		return
	}
	endpoint := openAIEndpoint(request.URL)
	e.estimate.add(e.integration, body.Model, len(request.Body), body.outputCeiling(endpoint.generation))
}

func (e *batchLineEstimator) finish() (batchEstimate, bool) {
	if len(bytes.TrimSpace(e.line)) > 0 || e.longLine > 0 {
		e.endLine()
	}
	return e.estimate, !e.invalid && e.estimate.requests > 0
}

// aiBatchReader names the gateway's batch input, status and results reads.
// They are GETs, which never settle, so it never appears in usage.
const aiBatchReader = "batch"

var (
	errBatchInputTooLarge   = errors.New("outbound: the batch input is larger than the gateway reads")
	errBatchInputUnreadable = errors.New("outbound: the batch input could not be read")
)

// streamRecorder hands a successful in-process response's body to w as it
// arrives and keeps the status.
type streamRecorder struct {
	header  http.Header
	status  int
	w       io.Writer
	err     error
	aborted bool
}

func (r *streamRecorder) Header() http.Header { return r.header }

func (r *streamRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *streamRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	if r.err != nil {
		// The reader gave up, such as on an input past its size: stop the copy.
		return 0, r.err
	}
	if r.status < 200 || r.status > 299 {
		// A refusal's body is not the stream the reader wants.
		return len(p), nil
	}
	if _, err := r.w.Write(p); err != nil {
		r.err = err
		return 0, err
	}
	return len(p), nil
}

func (r *streamRecorder) Flush() {}

// serveInternal runs an in-process GET for an internal principal and streams
// its body to w. It reports the status, or an error when the call broke.
func (h *Handler) serveInternal(ctx context.Context, principal internalPrincipal, integrationID, path, rawQuery string,
	timeout time.Duration, w io.Writer) (int, error) {
	callCtx, cancel := internalCallContext(ctx, principal, timeout)
	defer cancel()
	request := routeAttemptRequest(&http.Request{Method: http.MethodGet, Header: http.Header{}}, integrationID, path, rawQuery, nil, true).
		WithContext(callCtx) //nolint:contextcheck // internal calls carry no request values; internalCallContext carries cancellation.
	recorder := &streamRecorder{header: make(http.Header), w: w}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered != http.ErrAbortHandler { //nolint:errorlint // the handler panics with this exact sentinel value.
					panic(recovered)
				}
				recorder.aborted = true
			}
		}()
		h.ServeHTTP(recorder, request)
	}()
	switch {
	case recorder.err != nil:
		return recorder.status, recorder.err
	case recorder.aborted:
		return recorder.status, errBatchInputUnreadable
	}
	return recorder.status, nil
}

// batchSibling replaces a batch creation path's final "/batches" with
// suffix, keeping any prefix such as "/v1" or Azure's "/openai".
func batchSibling(path, suffix string) string {
	return strings.TrimSuffix(path, "/batches") + suffix
}

// estimateBatch sizes a batch creation call before admission (ADR-1431):
// Anthropic's requests from the body, OpenAI's from its input file, which
// it reads from the provider.
func (h *Handler) estimateBatch(ctx context.Context, integration Integration, path, rawQuery string, ai *aiRequest) (batchEstimate, *aiCallRejection) {
	tooLarge := &aiCallRejection{http.StatusRequestEntityTooLarge, "outbound_ai_batch_too_large",
		"The batch is larger than the gateway can size against the integration's caps"}
	unreadable := &aiCallRejection{http.StatusBadRequest, "outbound_ai_batch_unreadable",
		"The gateway could not read the batch's requests to size them against the integration's caps"}
	if ai == nil {
		return batchEstimate{}, tooLarge
	}
	if integration.AIProvider == api.OutboundAIProviderAnthropic {
		estimate, ok := estimateAnthropicBatch(integration, ai.body)
		if !ok {
			return batchEstimate{}, unreadable
		}
		return estimate, nil
	}
	var create struct {
		InputFileID string `json:"input_file_id"`
	}
	if json.Unmarshal(ai.body, &create) != nil || create.InputFileID == "" || strings.ContainsAny(create.InputFileID, "/?#%") {
		return batchEstimate{}, unreadable
	}
	estimator := &batchLineEstimator{integration: integration}
	status, err := h.serveInternal(ctx, internalPrincipal{accountID: integration.AccountID, caller: aiBatchReader},
		integration.ID, batchSibling(path, "/files/"+url.PathEscape(create.InputFileID)+"/content"), rawQuery,
		api.OutboundAIEvaluationCallTimeoutSeconds*time.Second, estimator)
	if errors.Is(err, errBatchInputTooLarge) {
		return batchEstimate{}, tooLarge
	}
	estimate, ok := estimator.finish()
	if err != nil || status < 200 || status > 299 || !ok {
		return batchEstimate{}, unreadable
	}
	return estimate, nil
}

// planBatch sizes a batch creation and sets the call's reservation to the
// estimate. It writes a refusal and returns false when the batch cannot be
// sized or kept.
func (h *Handler) planBatch(w http.ResponseWriter, r *http.Request, integration Integration, plan *aiCallPlan, ai *aiRequest,
	path, appID, bindingAppID, metricIntegrationID string) (*AIBatch, bool) {
	if _, ok := h.Backend.(AIBatchBackend); !ok {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_ai_batch_unavailable", "AI batches are unavailable", "1")
		return nil, false
	}
	if _, ok := h.Backend.(AIBudgetAlertRecorder); !ok {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_ai_batch_unavailable", "AI batches are unavailable", "1")
		return nil, false
	}
	estimate, rejection := h.estimateBatch(r.Context(), integration, path, r.URL.RawQuery, ai)
	if rejection == nil && (estimate.tokens > 3*api.OutboundAIMaxTokensPerResponse || estimate.cost > api.MaxOutboundAICostMillicents) {
		// Admission bounds one reservation; a batch past it is past any cap.
		rejection = &aiCallRejection{http.StatusRequestEntityTooLarge, "outbound_ai_batch_too_large",
			"The batch is larger than the gateway can size against the integration's caps"}
	}
	if rejection != nil {
		h.Metrics.ObserveRejection(metricIntegrationID, rejection.code)
		writeProblem(w, rejection.status, rejection.code, rejection.title, "")
		return nil, false
	}
	model := estimate.model()
	plan.reserve = aiReservation{tokens: estimate.tokens, cost: estimate.cost,
		estimate: AIUsage{Model: model, Known: true, InputTokens: estimate.tokens}}
	caller := appID
	if caller == "" {
		caller = AIUsageCallerRuns
	}
	return &AIBatch{
		ID: uuid.NewString(), IntegrationID: integration.ID, AccountID: integration.AccountID, Caller: caller,
		BindingAppID: bindingAppID, Provider: integration.AIProvider, Path: path, RawQuery: r.URL.RawQuery, Model: model,
		Requests: estimate.requests, ReservedTokens: estimate.tokens, ReservedCostMillicents: estimate.cost,
	}, true
}

func (h *Handler) reserveBatch(ctx context.Context, w http.ResponseWriter, batch AIBatch, metricIntegrationID string) bool {
	if err := h.Backend.(AIBatchBackend).ReserveAIBatch(context.WithoutCancel(ctx), batch); err != nil {
		h.Metrics.ObserveRejection(metricIntegrationID, "outbound_ai_batch_unavailable")
		writeProblem(w, http.StatusServiceUnavailable, "outbound_ai_batch_unavailable", "The batch could not be reserved", "1")
		return false
	}
	return true
}

// dropUnconfirmedBatch releases a batch whose creation never reached a
// provider answer. A confirmed or settled batch is left alone.
func (h *Handler) dropUnconfirmedBatch(ctx context.Context, batch *AIBatch) {
	if batch.ProviderBatchID == "" {
		_ = h.Backend.(AIBatchBackend).DropAIBatch(context.WithoutCancel(ctx), batch.ID)
	}
}

// finishBatchCreation passes the provider's answer through. A created batch
// opens with its provider ID; a refused one is dropped; a created batch
// without a readable ID cannot be polled, so it settles at its reservation.
func (h *Handler) finishBatchCreation(ctx context.Context, w http.ResponseWriter, resp *http.Response, batch *AIBatch, metricIntegrationID string) {
	head, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		backend := h.Backend.(AIBatchBackend)
		if id := batchProviderID(head); id != "" {
			batch.ProviderBatchID = id
			if err := backend.ConfirmAIBatch(context.WithoutCancel(ctx), batch.ID, id); err != nil {
				h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "state_error")
			}
		} else {
			batch.ProviderBatchID = "unknown"
			if err := settleAIBatchAtReservation(context.WithoutCancel(ctx), h.Backend.(AIBudgetAlertRecorder), *batch); err != nil {
				h.Metrics.ObserveAIUsageRecord(metricIntegrationID, "state_error")
			}
			_ = backend.DropAIBatch(context.WithoutCancel(ctx), batch.ID)
		}
	}
	for k, values := range resp.Header {
		if isHopByHop(resp.Header, k) || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(head)
	_, _ = io.Copy(w, resp.Body)
}

// batchProviderID reads the batch ID from a creation response.
func batchProviderID(body []byte) string {
	var created struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &created) != nil || len(created.ID) > 128 || created.ID == "" ||
		strings.IndexFunc(created.ID, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-'
		}) >= 0 {
		return ""
	}
	return created.ID
}

// AIBatchWorker settles batches once their provider ends them (ADR-1431).
// Every outboundd replica may run it: claims skip locked batches, and each
// model's settlement is keyed by the batch.
type AIBatchWorker struct {
	Backend  AIBatchBackend
	Usage    AIBudgetAlertRecorder
	Handler  *Handler
	Interval time.Duration
	Log      *slog.Logger
}

// Run checks due batches until ctx ends.
func (w *AIBatchWorker) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = api.OutboundAIBatchPollIntervalSeconds * time.Second / 2
	}
	for {
		if _, err := w.CheckOnce(ctx); err != nil && ctx.Err() == nil && w.Log != nil {
			w.Log.Warn("outboundd: AI batches", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// CheckOnce checks the due batches and returns how many it settled.
func (w *AIBatchWorker) CheckOnce(ctx context.Context) (int, error) {
	batches, err := w.Backend.ClaimAIBatches(ctx, 10)
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, batch := range batches {
		done, err := w.check(ctx, batch)
		if err != nil {
			return settled, err
		}
		if done {
			settled++
		}
	}
	return settled, nil
}

// batchStatus is what a status read tells the worker.
type batchStatus struct {
	ended      bool
	resultPath string
}

func (w *AIBatchWorker) check(ctx context.Context, batch AIBatch) (bool, error) {
	integration, err := w.Handler.Resolver.Integration(ctx, batch.IntegrationID)
	if err != nil {
		return false, nil //nolint:nilerr // an integration that cannot be read is retried at the next check.
	}
	status, err := w.status(ctx, integration, batch)
	if err != nil || !status.ended {
		if time.Since(batch.CreatedAt) > api.OutboundAIBatchAbandonHours*time.Hour {
			return true, w.settleAtReservation(ctx, batch)
		}
		return false, nil
	}
	totals := map[string]AIUsage{}
	if status.resultPath != "" {
		reader := &batchResultReader{provider: batch.Provider, totals: totals}
		code, err := w.Handler.serveInternal(ctx, internalPrincipal{accountID: batch.AccountID, caller: aiBatchReader},
			batch.IntegrationID, status.resultPath, batch.RawQuery, api.OutboundAIEvaluationCallTimeoutSeconds*time.Second, reader)
		reader.finish()
		if err != nil || code < 200 || code > 299 {
			return false, nil //nolint:nilerr // unreadable results are read again at the next check.
		}
	}
	return true, w.settle(ctx, integration, batch, totals)
}

// status reads a batch from its provider.
func (w *AIBatchWorker) status(ctx context.Context, integration Integration, batch AIBatch) (batchStatus, error) {
	var body bytes.Buffer
	path := batch.Path + "/" + url.PathEscape(batch.ProviderBatchID)
	code, err := w.Handler.serveInternal(ctx, internalPrincipal{accountID: batch.AccountID, caller: aiBatchReader},
		integration.ID, path, batch.RawQuery, 30*time.Second, &limitedBuffer{buffer: &body, limit: 1 << 20})
	if err != nil || code < 200 || code > 299 {
		return batchStatus{}, fmt.Errorf("outbound: batch status: %d %w", code, err)
	}
	var read struct {
		Status           string  `json:"status"`
		ProcessingStatus string  `json:"processing_status"`
		OutputFileID     *string `json:"output_file_id"`
	}
	if err := json.Unmarshal(body.Bytes(), &read); err != nil {
		return batchStatus{}, err
	}
	if batch.Provider == api.OutboundAIProviderAnthropic {
		if read.ProcessingStatus != "ended" {
			return batchStatus{}, nil
		}
		return batchStatus{ended: true, resultPath: path + "/results"}, nil
	}
	switch read.Status {
	case "completed", "failed", "expired", "cancelled":
	default:
		return batchStatus{}, nil
	}
	status := batchStatus{ended: true}
	if read.OutputFileID != nil && *read.OutputFileID != "" && !strings.ContainsAny(*read.OutputFileID, "/?#%") {
		status.resultPath = batchSibling(batch.Path, "/files/"+url.PathEscape(*read.OutputFileID)+"/content")
	}
	return status, nil
}

// settle records each model's usage, keyed by the batch and model, and ends
// the batch.
func (w *AIBatchWorker) settle(ctx context.Context, integration Integration, batch AIBatch, totals map[string]AIUsage) error {
	var tokens, cost int64
	for model, usage := range totals {
		// One settlement holds at most one reservation's bound.
		bound := 3 * api.OutboundAIMaxTokensPerResponse
		usage.InputTokens, usage.OutputTokens = min(usage.InputTokens, bound), min(usage.OutputTokens, bound)
		usage.CachedInputTokens, usage.CacheWriteTokens = min(usage.CachedInputTokens, bound), min(usage.CacheWriteTokens, bound)
		usage.WebSearches, usage.FileSearches = min(usage.WebSearches, bound), min(usage.FileSearches, bound)
		usageCost := min(batchCost(aiCallCostMillicents(integration, usage)), api.MaxOutboundAICostMillicents)
		record := AIUsageRecord{IntegrationID: batch.IntegrationID, Caller: batch.Caller, Outcome: AIUsageOutcomeKnown,
			Usage: usage, CostMillicents: usageCost, LeaseID: batchSettlementKey(batch.ID, model)}
		if _, err := w.Usage.RecordAIUsageAlerts(ctx, record); err != nil {
			return err
		}
		tokens, cost = tokens+usage.BudgetTokens(), cost+usageCost
	}
	outcome := AIUsageOutcomeKnown
	if len(totals) == 0 {
		outcome = AIUsageOutcomeError
	}
	if err := w.Backend.SettleAIBatch(ctx, batch.ID, outcome, tokens, cost); err != nil {
		return err
	}
	w.Handler.Metrics.ObserveAIBatchSettled(outcome)
	return nil
}

// settleAtReservation settles a batch that never ended at its reservation,
// as unknown usage (ADR-996).
func (w *AIBatchWorker) settleAtReservation(ctx context.Context, batch AIBatch) error {
	if err := settleAIBatchAtReservation(ctx, w.Usage, batch); err != nil {
		return err
	}
	if err := w.Backend.SettleAIBatch(ctx, batch.ID, AIUsageOutcomeUnknown, batch.ReservedTokens, batch.ReservedCostMillicents); err != nil {
		return err
	}
	w.Handler.Metrics.ObserveAIBatchSettled(AIUsageOutcomeUnknown)
	return nil
}

// settleAIBatchAtReservation records a batch's reservation as its usage,
// keyed by the batch.
func settleAIBatchAtReservation(ctx context.Context, usage AIBudgetAlertRecorder, batch AIBatch) error {
	record := AIUsageRecord{IntegrationID: batch.IntegrationID, Caller: batch.Caller, Outcome: AIUsageOutcomeUnknown,
		Usage:          AIUsage{Model: batch.Model, InputTokens: batch.ReservedTokens},
		CostMillicents: batch.ReservedCostMillicents, LeaseID: batchSettlementKey(batch.ID, "")}
	_, err := usage.RecordAIUsageAlerts(ctx, record)
	return err
}

// batchSettlementKey derives one settlement key per batch and model.
func batchSettlementKey(batchID, model string) string {
	sum := sha256.Sum256([]byte(batchID + "\x00" + model))
	return uuid.NewSHA1(uuid.NameSpaceOID, sum[:]).String()
}

// batchResultReader totals a results file's usage by model as it streams.
type batchResultReader struct {
	provider string
	totals   map[string]AIUsage
	line     []byte
	skip     bool
}

func (r *batchResultReader) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		chunk := p
		if end >= 0 {
			chunk = p[:end]
		}
		if r.skip || len(r.line)+len(chunk) > api.OutboundAIBatchLineMaxBytes {
			r.skip, r.line = true, r.line[:0]
		} else {
			r.line = append(r.line, chunk...)
		}
		if end < 0 {
			break
		}
		r.endLine()
		p = p[end+1:]
	}
	return written, nil
}

func (r *batchResultReader) endLine() {
	defer func() { r.line, r.skip = r.line[:0], false }()
	if r.skip {
		return
	}
	var line struct {
		Response *struct {
			StatusCode int             `json:"status_code"`
			Body       json.RawMessage `json:"body"`
		} `json:"response"`
		Result *struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		} `json:"result"`
	}
	if json.Unmarshal(bytes.TrimSpace(r.line), &line) != nil {
		return
	}
	format := aiFormatOpenAI
	var body json.RawMessage
	switch {
	case line.Response != nil && line.Response.StatusCode >= 200 && line.Response.StatusCode <= 299:
		body = line.Response.Body
	case line.Result != nil && line.Result.Type == "succeeded":
		format, body = aiFormatAnthropic, line.Result.Message
	default:
		return
	}
	observer := newAIUsageObserverFormat(format, "application/json", time.Now)
	_, _ = observer.Write(body)
	usage := observer.Finish()
	if !usage.Known || usage.Model == "" {
		return
	}
	total := r.totals[usage.Model]
	total.Model, total.Known = usage.Model, true
	total.InputTokens += usage.InputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.CacheWriteTokens += usage.CacheWriteTokens
	total.OutputTokens += usage.OutputTokens
	// ADR-1433: a batch request's web and file searches.
	total.WebSearches += usage.WebSearches
	total.FileSearches += usage.FileSearches
	r.totals[usage.Model] = total
}

func (r *batchResultReader) finish() {
	if len(bytes.TrimSpace(r.line)) > 0 {
		r.endLine()
	}
}

// limitedBuffer keeps at most limit bytes.
type limitedBuffer struct {
	buffer *bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buffer.Len(); room > 0 {
		b.buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

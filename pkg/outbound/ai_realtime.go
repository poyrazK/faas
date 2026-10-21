package outbound

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/onebox-faas/faas/pkg/api"
)

// Realtime sessions (ADR 20261019072722): an OpenAI or Azure OpenAI
// Realtime WebSocket passes through outboundd byte for byte. Each response
// the provider starts is admitted against the integration's caps and settled
// from the usage it reports.

// Events counted in outbound_ai_realtime_sessions_total.
const (
	aiRealtimeOpened           = "opened"
	aiRealtimeUnmetered        = "unmetered"
	aiRealtimeClosedClient     = "closed_client"
	aiRealtimeClosedProvider   = "closed_provider"
	aiRealtimeClosedCap        = "closed_cap"
	aiRealtimeClosedDuration   = "closed_duration"
	aiRealtimeClosedUnreadable = "closed_unreadable"
	aiRealtimeClosedError      = "closed_error"
)

var aiRealtimeSessionEvents = []string{aiRealtimeOpened, aiRealtimeUnmetered, aiRealtimeClosedClient, aiRealtimeClosedProvider,
	aiRealtimeClosedCap, aiRealtimeClosedDuration, aiRealtimeClosedUnreadable, aiRealtimeClosedError}

var (
	realtimePolicyRejection = &aiCallRejection{http.StatusForbidden, "outbound_ai_realtime_policy_unenforceable",
		"The integration's content policy cannot check realtime sessions; turn its request, moderation and response checks off to use them"}
	realtimeRouteRejection = &aiCallRejection{http.StatusBadRequest, "outbound_ai_route_realtime_unsupported",
		"Routes cannot carry realtime sessions; call an integration directly"}
)

// realtimeOpenResponsesMax bounds the responses one session has in progress
// at once: the default conversation's and out-of-band ones.
const realtimeOpenResponsesMax = 16

// realtimeAdmissionTimeout bounds one response's admission.
const realtimeAdmissionTimeout = 10 * time.Second

// realtimeMode is how an AI integration carries a WebSocket upgrade.
type realtimeMode int

const (
	realtimeNone realtimeMode = iota
	realtimeMetered
	realtimeUnmetered
)

// isWebSocketUpgrade reports a WebSocket opening handshake.
func isWebSocketUpgrade(r *http.Request) bool {
	return r.Method == http.MethodGet && headerHasToken(r.Header, "Connection", "upgrade") &&
		strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func headerHasToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// realtimePath reports a provider path that opens a metered session:
// OpenAI's /v1/realtime, Azure OpenAI's /openai/v1/realtime and
// /openai/realtime, and Gemini's and Vertex's Live API.
func realtimePath(provider, path string) bool {
	switch provider {
	case api.OutboundAIProviderOpenAI:
		return path == "/v1/realtime"
	case api.OutboundAIProviderAzureOpenAI:
		return path == "/openai/v1/realtime" || path == "/openai/realtime"
	case api.OutboundAIProviderGemini, api.OutboundAIProviderVertex:
		return livePath(provider, path)
	}
	return false
}

// planRealtime decides how a WebSocket upgrade on an AI integration is
// carried, or refuses it. Integrations without a provider keep ignoring
// Upgrade, as before.
func planRealtime(r *http.Request, integration Integration, path string) (realtimeMode, *aiCallRejection) {
	if integration.AIProvider == "" || !isWebSocketUpgrade(r) {
		return realtimeNone, nil
	}
	if !realtimePath(integration.AIProvider, path) {
		if !integration.AIAllowUnmetered {
			return realtimeNone, unmeteredRejection
		}
		return realtimeUnmetered, nil
	}
	policy := integration.AIContentPolicy
	if policy.Active() || policy.Moderation.Active() || policy.Response.Active() {
		return realtimeNone, realtimePolicyRejection
	}
	return realtimeMetered, nil
}

// realtimeURLModel is the model a session URL names: OpenAI's model, or
// an Azure deployment.
func realtimeURLModel(query url.Values) string {
	if model := query.Get("model"); model != "" {
		return sanitizeAIModel(model)
	}
	return sanitizeAIModel(query.Get("deployment"))
}

// realtimeSession carries one session between the caller and the provider.
type realtimeSession struct {
	h           *Handler
	integration Integration
	appID       string
	metricID    string
	metered     bool
	// spec is the upgrade's admission; each response is admitted with it
	// and its own reservation.
	spec    AdmissionSpec
	endUser string
	tags    []string
	// traceID is the upgrade's AI trace; every response settles into it
	// (ADR 20261020202035).
	traceID string
	// model prices the session's responses: session.model when the
	// provider names it, else the URL's. transcriptionModel prices input
	// transcriptions.
	model              string
	transcriptionModel string
	// lastInput is the latest response's input tokens; the next response
	// reserves at least as much, since a session's context only grows.
	lastInput int64
	open      map[string]*realtimeResponse
	// live marks a Gemini or Vertex Live session (ADR 20261020161811):
	// turns are admitted and waiting for usage, oldest first, and
	// generating marks the newest still producing output.
	live       bool
	turns      []*realtimeResponse
	generating bool

	client   net.Conn
	clientMu sync.Mutex
	// midMessage marks a provider message half-forwarded to the caller,
	// guarded by clientMu: a gateway error event must not split it.
	midMessage bool
	upstream   io.ReadWriteCloser
	message    wsMessage
	endOnce    sync.Once
	end        string
	settles    sync.WaitGroup
	// span is a no-op span for per-response attributes: a session's span
	// would otherwise carry only its last response's usage.
	span oteltrace.Span
}

// realtimeResponse is a response admitted and not yet done.
type realtimeResponse struct {
	leaseID   string
	reserve   aiReservation
	started   time.Time
	firstByte time.Time
	// grounding is a Live turn's search grounding (ADR-1433), seen before
	// its usage.
	grounding aiRawUsage
}

// serveRealtime opens the session at the provider, switches the caller's
// connection to it and carries frames until either side ends. upstreamReq
// is the call as the gateway would send it, credential applied.
func (h *Handler) serveRealtime(w http.ResponseWriter, r *http.Request, session *realtimeSession, upstreamReq *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), api.OutboundAIRealtimeSessionMaxSeconds*time.Second)
	defer cancel()
	req := upstreamReq.Clone(ctx)
	req.Body, req.ContentLength = http.NoBody, 0
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	// No compression: every provider message must stay readable.
	req.Header.Del("Sec-WebSocket-Extensions")
	resp, err := h.Client.Do(req)
	if err != nil {
		writeProblem(w, http.StatusBadGateway, "outbound_upstream_unavailable", "Outbound provider could not be reached", "1")
		return
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		// The provider refused the session; its answer reaches the caller.
		defer func() { _ = resp.Body.Close() }()
		forwardRefusedUpgrade(w, resp, h.MaxResponseBytes)
		return
	}
	upstream, ok := resp.Body.(io.ReadWriteCloser)
	if !ok || resp.Header.Get("Sec-WebSocket-Extensions") != "" {
		_ = resp.Body.Close()
		writeProblem(w, http.StatusBadGateway, "outbound_ai_realtime_unreadable", "The provider's realtime session cannot be metered", "")
		return
	}
	defer func() { _ = upstream.Close() }()
	client, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "outbound_ai_realtime_unavailable", "The gateway cannot carry a realtime session on this connection", "")
		return
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Time{})
	if err := writeSwitchingProtocols(buffered.Writer, resp.Header); err != nil {
		return
	}
	session.client, session.upstream = client, upstream
	session.open = make(map[string]*realtimeResponse)
	session.message = wsMessage{maxSize: api.OutboundAIRealtimeMaxMessageBytes, binary: session.live}
	session.span = noop.Span{}
	event := aiRealtimeOpened
	if !session.metered {
		event = aiRealtimeUnmetered
		h.Metrics.ObserveAIUnmetered(session.metricID)
	}
	h.Metrics.ObserveAIRealtimeSession(event)
	h.Metrics.AddAIRealtimeActive(1)
	defer h.Metrics.AddAIRealtimeActive(-1)
	session.run(ctx, buffered.Reader)
}

// forwardRefusedUpgrade sends the provider's refusal of a session.
func forwardRefusedUpgrade(w http.ResponseWriter, resp *http.Response, maxBytes int64) {
	for name, values := range resp.Header {
		if isHopByHop(resp.Header, name) {
			continue
		}
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if maxBytes <= 0 {
		maxBytes = api.OutboundAIRealtimeMaxMessageBytes
	}
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxBytes))
}

// writeSwitchingProtocols completes the caller's handshake with the
// provider's answer: the caller's key went to the provider, so its accept
// value fits.
func writeSwitchingProtocols(out *bufio.Writer, header http.Header) error {
	if _, err := out.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"); err != nil {
		return err
	}
	for _, name := range []string{"Sec-WebSocket-Accept", "Sec-WebSocket-Protocol"} {
		if value := header.Get(name); value != "" && !strings.ContainsAny(value, "\r\n") {
			if _, err := out.WriteString(name + ": " + value + "\r\n"); err != nil {
				return err
			}
		}
	}
	if _, err := out.WriteString("\r\n"); err != nil {
		return err
	}
	return out.Flush()
}

// run carries the session: the caller's frames go to the provider
// untouched, and the provider's are read, metered and forwarded.
func (s *realtimeSession) run(ctx context.Context, clientIn *bufio.Reader) {
	if s.live && !s.forwardLiveSetup(clientIn) {
		s.h.Metrics.ObserveAIRealtimeSession(s.endEvent())
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(s.upstream, clientIn)
		s.finish(aiRealtimeClosedClient)
	}()
	go func() {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.closeWith(aiRealtimeClosedDuration, "", "", "outbound_ai_realtime_session_expired")
			}
		case <-done:
		}
	}()
	s.forwardProvider(ctx)
	<-done
	s.settleOpen(ctx)
	s.settles.Wait()
	s.h.Metrics.ObserveAIRealtimeSession(s.endEvent())
}

// endEvent is how the session ended. Do waits for the call that set it.
func (s *realtimeSession) endEvent() string {
	s.endOnce.Do(func() { s.end = aiRealtimeClosedError })
	return s.end
}

// forwardProvider reads the provider's frames until the session ends.
func (s *realtimeSession) forwardProvider(ctx context.Context) {
	in := bufio.NewReaderSize(s.upstream, 64<<10)
	for {
		frame, err := readWSFrame(in, api.OutboundAIRealtimeMaxMessageBytes)
		if err != nil {
			s.readFailed(err)
			return
		}
		if s.metered {
			message, messageErr := s.message.add(frame)
			if messageErr != nil {
				s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
				return
			}
			if message != nil {
				if end := s.meter(ctx, message, time.Now()); end != "" {
					return
				}
			}
		}
		mid := false
		if frame.opcode < wsOpClose {
			mid = !frame.fin
		}
		if !s.writeClientFrame(append(frame.header, frame.payload...), frame.opcode >= wsOpClose, mid) {
			s.finish(aiRealtimeClosedClient)
			return
		}
		if frame.opcode == wsOpClose {
			s.finish(aiRealtimeClosedProvider)
			return
		}
	}
}

// readFailed ends the session on a provider read error: a frame the proxy
// cannot read ends it as unreadable, a closed connection as the provider's.
func (s *realtimeSession) readFailed(err error) {
	if errors.Is(err, errWSFrameTooLarge) || errors.Is(err, errWSCompressed) || errors.Is(err, errWSProtocol) {
		s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
		return
	}
	s.finish(aiRealtimeClosedProvider)
}

// realtimeMeteredTypes are the event types metering reads. A quoted type
// cannot appear unescaped inside another event's strings, so a byte search
// skips the many audio deltas without decoding them.
var realtimeMeteredTypes = [][]byte{
	[]byte(`"response.created"`), []byte(`"response.done"`), []byte(`"session.created"`), []byte(`"session.updated"`),
	[]byte(`"conversation.item.input_audio_transcription.completed"`),
}

// realtimeDeltaMarker marks a response's output deltas, the first of which
// is its time to first byte.
var realtimeDeltaMarker = []byte(`.delta"`)

// realtimeEvent is what metering reads from one provider message.
type realtimeEvent struct {
	Type     string `json:"type"`
	Response *struct {
		ID     string         `json:"id"`
		Status string         `json:"status"`
		Usage  *aiUsageFields `json:"usage"`
	} `json:"response"`
	Session *realtimeSessionConfig `json:"session"`
	// Usage is a transcription's usage, in tokens or seconds.
	Usage *aiUsageFields `json:"usage"`
}

// realtimeSessionConfig carries the session's model and its input
// transcription model, in the GA and the earlier beta shape.
type realtimeSessionConfig struct {
	Model string `json:"model"`
	Audio *struct {
		Input *struct {
			Transcription *struct {
				Model string `json:"model"`
			} `json:"transcription"`
		} `json:"input"`
	} `json:"audio"`
	InputAudioTranscription *struct {
		Model string `json:"model"`
	} `json:"input_audio_transcription"`
}

// meter reads one complete provider message. It returns the end event when
// the message ends the session.
func (s *realtimeSession) meter(ctx context.Context, message []byte, now time.Time) string {
	if s.live {
		return s.meterLive(ctx, message, now)
	}
	if bytes.Contains(message, realtimeDeltaMarker) {
		for _, open := range s.open {
			if open.firstByte.IsZero() {
				open.firstByte = now
			}
		}
	}
	if !realtimeMeteredMessage(message) {
		return ""
	}
	var event realtimeEvent
	if err := json.Unmarshal(message, &event); err != nil {
		s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
		return aiRealtimeClosedUnreadable
	}
	switch event.Type {
	case "session.created", "session.updated":
		s.noteSession(event.Session)
	case "response.created":
		if event.Response != nil {
			return s.admitResponse(ctx, event.Response.ID, now)
		}
	case "response.done":
		if event.Response != nil {
			s.settleResponse(ctx, event.Response.ID, event.Response.Usage, now)
		}
	case "conversation.item.input_audio_transcription.completed":
		s.settleTranscription(ctx, event.Usage, now)
	}
	return ""
}

func realtimeMeteredMessage(message []byte) bool {
	for _, marker := range realtimeMeteredTypes {
		if bytes.Contains(message, marker) {
			return true
		}
	}
	return false
}

// noteSession keeps the models a session reports.
func (s *realtimeSession) noteSession(config *realtimeSessionConfig) {
	if config == nil {
		return
	}
	if model := sanitizeAIModel(config.Model); model != "" {
		s.model = model
	}
	transcription := ""
	if config.Audio != nil && config.Audio.Input != nil && config.Audio.Input.Transcription != nil {
		transcription = config.Audio.Input.Transcription.Model
	} else if config.InputAudioTranscription != nil {
		transcription = config.InputAudioTranscription.Model
	}
	if model := sanitizeAIModel(transcription); model != "" {
		s.transcriptionModel = model
	}
}

// reservation is a response's admission estimate: the previous response's
// input, or the first response's reserve, and the output reserve, both
// priced as audio, the dearer kind.
func (s *realtimeSession) reservation() aiReservation {
	input := s.lastInput
	if input <= 0 {
		input = api.OutboundAIRealtimeInputReserveTokens
	}
	estimate := AIUsage{Model: s.usageModel(), Known: true, InputTokens: min(input, api.OutboundAIMaxTokensPerResponse),
		OutputTokens: api.OutboundAIRealtimeOutputReserveTokens}
	estimate.InputAudioTokens, estimate.OutputAudioTokens = estimate.InputTokens, estimate.OutputTokens
	return aiReservation{tokens: estimate.InputTokens + estimate.OutputTokens, cost: aiCallCostMillicents(s.integration, estimate),
		estimate: estimate}
}

func (s *realtimeSession) usageModel() string {
	if s.model == "" {
		return "unknown"
	}
	return s.model
}

// admitResponse admits a response the provider started. A refusal ends the
// session: the provider stops when its connection closes.
func (s *realtimeSession) admitResponse(ctx context.Context, id string, now time.Time) string {
	if !s.metered {
		return ""
	}
	if len(s.open) >= realtimeOpenResponsesMax {
		s.closeWith(aiRealtimeClosedUnreadable, "", "", "outbound_ai_realtime_unreadable")
		return aiRealtimeClosedUnreadable
	}
	response, end := s.admit(ctx, now)
	if end != "" {
		return end
	}
	if id == "" {
		id = response.leaseID
	}
	s.open[id] = response
	return ""
}

// admit admits one response or Live turn at its reservation. A refusal ends
// the session: the provider stops when its connection closes.
func (s *realtimeSession) admit(ctx context.Context, now time.Time) (*realtimeResponse, string) {
	reserve := s.reservation()
	spec := s.spec
	spec.AIReservedTokens, spec.AIReservedCostMillicents = reserve.tokens, reserve.cost
	admitCtx, cancel := context.WithTimeout(ctx, realtimeAdmissionTimeout)
	decision, err := s.h.Backend.Admit(admitCtx, spec)
	cancel()
	if err != nil {
		s.h.Metrics.ObserveAdmission(s.metricID, "error")
		s.h.Metrics.ObserveRejection(s.metricID, "backend_unavailable")
		s.closeWith(aiRealtimeClosedError, "outbound_admission_unavailable", "Outbound admission is temporarily unavailable",
			"outbound_admission_unavailable")
		return nil, aiRealtimeClosedError
	}
	if !decision.Granted {
		s.h.Metrics.ObserveAdmission(s.metricID, "rejected")
		s.h.Metrics.ObserveRejection(s.metricID, decision.Reason)
		s.closeWith(aiRealtimeClosedCap, "outbound_budget_exhausted", "Outbound integration budget is exhausted: "+decision.Reason,
			"outbound_budget_exhausted")
		return nil, aiRealtimeClosedCap
	}
	s.h.Metrics.ObserveAdmission(s.metricID, "granted")
	return &realtimeResponse{leaseID: decision.LeaseID, reserve: reserve, started: now}, ""
}

// settleResponse settles a finished response from its usage. A response
// that reports no usage settles at its reservation.
func (s *realtimeSession) settleResponse(ctx context.Context, id string, fields *aiUsageFields, now time.Time) {
	open := s.open[id]
	delete(s.open, id)
	if open == nil {
		if !s.metered {
			open = &realtimeResponse{started: now}
		} else {
			// Not admitted (its start was missed): settle without a lease.
			open = &realtimeResponse{reserve: s.reservation(), started: now}
		}
	}
	var raw aiRawUsage
	raw.merge(fields)
	usage := raw.normalize(aiFormatOpenAI, aiMediaNone)
	usage.Model = s.usageModel()
	outcome := AIUsageOutcomeKnown
	if !usage.Known {
		outcome = AIUsageOutcomeUnknown
	} else {
		s.lastInput = usage.InputTokens
	}
	s.settle(ctx, open, usage, outcome, aiMediaNone, now)
}

// settleTranscription settles one input transcription under the session's
// transcription model. Transcriptions follow input audio, so they are not
// admitted ahead; the next response's admission counts them.
func (s *realtimeSession) settleTranscription(ctx context.Context, fields *aiUsageFields, now time.Time) {
	if !s.metered || fields == nil {
		return
	}
	var raw aiRawUsage
	raw.merge(fields)
	usage := raw.normalize(aiFormatOpenAI, aiMediaTranscription)
	if !usage.Known {
		return
	}
	usage.Model = s.transcriptionModel
	if usage.Model == "" {
		usage.Model = "unknown"
	}
	s.settle(ctx, &realtimeResponse{started: now}, usage, AIUsageOutcomeKnown, aiMediaTranscription, now)
}

// settleOpen settles the responses and Live turns a session ended in the
// middle of at their reservations.
func (s *realtimeSession) settleOpen(ctx context.Context) {
	now := time.Now()
	for id, open := range s.open {
		delete(s.open, id)
		s.settle(ctx, open, open.reserve.estimate, AIUsageOutcomeUnknown, aiMediaNone, now)
	}
	for _, turn := range s.turns {
		s.settle(ctx, turn, turn.reserve.estimate, AIUsageOutcomeUnknown, aiMediaNone, now)
	}
	s.turns = nil
}

// settle records one response or transcription in the background, so a
// slow settlement never holds the provider's frames.
func (s *realtimeSession) settle(ctx context.Context, response *realtimeResponse, usage AIUsage, outcome AIUsageOutcome,
	media aiMediaKind, now time.Time) {
	if !s.metered {
		return
	}
	settlement := aiSettlement{status: http.StatusOK, attempts: 1, started: response.started, endUser: s.endUser, tags: s.tags,
		traceID: s.traceID, leaseID: response.leaseID, reserve: response.reserve, billable: true, media: media}
	cost := aiCallCostMillicents(s.integration, usage)
	if estimated, estimatedCost, ok := estimatedAIUsage(outcome, usage, settlement); ok {
		usage, cost = estimated, estimatedCost
	}
	var timeToFirstByte time.Duration
	if !response.firstByte.IsZero() {
		timeToFirstByte = response.firstByte.Sub(response.started)
	}
	settled := settledAIUsage{usage: usage, outcome: outcome, cost: cost, timeToFirstByte: timeToFirstByte}
	s.settles.Add(1)
	go func() {
		defer s.settles.Done()
		s.h.recordSettledAIUsage(context.WithoutCancel(ctx), s.integration, s.appID, settlement, settled, s.span, s.metricID)
	}()
}

// writeClientFrame forwards one provider frame to the caller; frames from
// different goroutines never interleave. A control frame leaves the
// half-forwarded state alone, since it may arrive between fragments.
func (s *realtimeSession) writeClientFrame(data []byte, control, mid bool) bool {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	if !control {
		s.midMessage = mid
	}
	_, err := s.client.Write(data)
	return err == nil
}

// closeWith ends the session from the gateway's side: an error event when
// no provider message is half-forwarded, then a close frame, then both
// connections close. Live has no error event, so its close reason alone
// names the refusal.
func (s *realtimeSession) closeWith(event, code, message, reason string) {
	s.endOnce.Do(func() {
		s.end = event
		s.clientMu.Lock()
		if code != "" && !s.midMessage && !s.live {
			payload, _ := json.Marshal(map[string]any{"type": "error",
				"error": map[string]string{"type": "gateway_error", "code": code, "message": message}})
			_, _ = s.client.Write(wsServerFrame(wsOpText, payload))
		}
		_, _ = s.client.Write(wsCloseFrame(wsClosePolicyViolation, reason))
		s.clientMu.Unlock()
		_ = s.upstream.Close()
		_ = s.client.Close()
	})
}

// finish ends the session after one side closed.
func (s *realtimeSession) finish(event string) {
	s.endOnce.Do(func() {
		s.end = event
		_ = s.upstream.Close()
		_ = s.client.Close()
	})
}

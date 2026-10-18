package outbound

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/airedact"
)

// responseStreamGuard applies the response policy to a streamed response
// event by event (ADR-1435). It keeps each channel's text, releases an
// event once OutboundAIResponseHoldBackBytes more text of its channels has
// arrived or the stream ended, and rewrites held events to redact. A block
// drops the held events and ends the stream with an error event. With
// moderation set to block, nothing is released before the check passes at
// the end; with flag, the check runs after the last event.
type responseStreamGuard struct {
	src    io.ReadCloser
	format aiWireFormat
	aws    bool
	scan   *responseScan
	// moderate is the moderation check, or nil; holdAll marks block.
	moderate func(string) ([]string, error)
	holdAll  bool
	onError  string
	check    *aiResponseCheck
	observe  func()

	pending  []byte
	queue    []*heldResponseEvent
	queued   int
	channels map[string]*responseChannel
	order    []string
	// wholeCounts is the most redactions of one kind in one whole text: a
	// Responses stream repeats its text in done events, so they count once.
	wholeCounts map[airedact.Kind]int
	wholeTexts  []string
	out         []byte
	buf         []byte
	srcErr      error
	ended       bool
	stopped     bool
	flagPending bool
}

type responseChannel struct {
	text    []byte
	checked int
	spans   []responseSpan
	// closed marks a channel its provider ended, so nothing waits for
	// more of its text.
	closed bool
}

// responseSpan is a finding to redact in a channel's text.
type responseSpan struct {
	kind       airedact.Kind
	start, end int
	placed     bool
}

type heldResponseEvent struct {
	raw    []byte
	value  any
	encode func(value any) ([]byte, bool)
	deltas []heldResponseDelta
	wholes []heldResponseWhole
}

type heldResponseDelta struct {
	loc        responseTextLoc
	channel    *responseChannel
	start, end int
}

type heldResponseWhole struct {
	loc      responseTextLoc
	redacted string
}

func newResponseStreamGuard(src io.ReadCloser, format aiWireFormat, aws bool, policy api.OutboundAIContentPolicy,
	moderate func(string) ([]string, error), check *aiResponseCheck, observe func()) *responseStreamGuard {
	policy = policy.Normalized()
	guard := &responseStreamGuard{
		src: src, format: format, aws: aws, scan: newResponseScan(policy), check: check, observe: observe,
		channels: make(map[string]*responseChannel), wholeCounts: make(map[airedact.Kind]int), onError: policy.Moderation.OnError,
	}
	if policy.Response.Moderation != api.OutboundAIModerationOff {
		guard.moderate, guard.holdAll = moderate, policy.Response.Moderation == api.OutboundAIModerationBlock
	}
	return guard
}

func (g *responseStreamGuard) Read(p []byte) (int, error) {
	for len(g.out) == 0 {
		if g.ended {
			if g.flagPending {
				g.flagPending = false
				g.runFlagCheck()
				continue
			}
			g.finish()
			if g.srcErr != nil && !errors.Is(g.srcErr, io.EOF) && !g.stopped {
				return 0, g.srcErr
			}
			return 0, io.EOF
		}
		if g.buf == nil {
			g.buf = make([]byte, 32<<10)
		}
		n, err := g.src.Read(g.buf)
		if n > 0 {
			g.accept(g.buf[:n])
		}
		if err != nil && !g.ended {
			g.srcErr = err
			g.end()
		}
	}
	n := copy(p, g.out)
	g.out = g.out[n:]
	return n, nil
}

// Close records the check, also for a stream the caller abandoned, and
// closes the provider body.
func (g *responseStreamGuard) Close() error {
	g.finish()
	return g.src.Close()
}

func (g *responseStreamGuard) finish() {
	for kind, count := range g.wholeCounts {
		if count > g.check.redacted[kind] {
			g.check.addRedactions(map[airedact.Kind]int{kind: count - g.check.redacted[kind]})
		}
	}
	g.wholeCounts = map[airedact.Kind]int{}
	if g.observe != nil {
		g.observe()
	}
}

func (g *responseStreamGuard) accept(chunk []byte) {
	g.pending = append(g.pending, chunk...)
	for !g.ended {
		raw, ok := g.nextEvent()
		if !ok {
			break
		}
		g.enqueue(raw)
	}
	if g.ended {
		return
	}
	if int64(len(g.pending)) > api.OutboundAIResponseCheckMaxBytes {
		g.stop(aiResponseUnchecked, aiResponseUncheckedCode, uncheckedMessage)
		return
	}
	g.step(false)
}

// nextEvent cuts the next complete event: an SSE event up to its blank
// line, or an AWS event-stream message. A malformed message ends the
// stream unchecked.
func (g *responseStreamGuard) nextEvent() ([]byte, bool) {
	if !g.aws {
		end, sep := sseEventEnd(g.pending)
		if end < 0 {
			return nil, false
		}
		raw := g.pending[: end+sep : end+sep]
		g.pending = g.pending[end+sep:]
		return raw, true
	}
	if len(g.pending) < awsEventStreamPreludeBytes {
		return nil, false
	}
	total := int64(binary.BigEndian.Uint32(g.pending[0:4]))
	headers := int64(binary.BigEndian.Uint32(g.pending[4:8]))
	minimum := int64(awsEventStreamPreludeBytes + awsEventStreamTrailerBytes)
	if total < minimum || headers > total-minimum || total > api.OutboundAIResponseCheckMaxBytes {
		g.stop(aiResponseUnchecked, aiResponseUncheckedCode, uncheckedMessage)
		return nil, false
	}
	if int64(len(g.pending)) < total {
		return nil, false
	}
	raw := g.pending[:total:total]
	g.pending = g.pending[total:]
	return raw, true
}

// enqueue parses one event, adds its delta text to its channels and checks
// its whole texts.
func (g *responseStreamGuard) enqueue(raw []byte) {
	event := &heldResponseEvent{raw: raw}
	texts, closes := g.parse(event)
	for _, key := range closes {
		if channel, ok := g.channels[key]; ok {
			channel.closed = true
		}
	}
	for _, loc := range texts {
		text := loc.text()
		if loc.channel == "" {
			redacted, blocked, counts := g.scan.whole(text)
			if blocked != "" {
				g.block(blocked)
				return
			}
			for kind, count := range counts {
				g.wholeCounts[kind] = max(g.wholeCounts[kind], count)
			}
			if len(counts) > 0 {
				event.wholes = append(event.wholes, heldResponseWhole{loc: loc, redacted: redacted})
			}
			g.wholeTexts = append(g.wholeTexts, redacted)
			continue
		}
		channel, ok := g.channels[loc.channel]
		if !ok {
			channel = &responseChannel{}
			g.channels[loc.channel] = channel
			g.order = append(g.order, loc.channel)
		}
		start := len(channel.text)
		channel.text = append(channel.text, text...)
		event.deltas = append(event.deltas, heldResponseDelta{loc: loc, channel: channel, start: start, end: len(channel.text)})
		if int64(len(channel.text)) > api.OutboundAIResponseCheckMaxBytes {
			g.stop(aiResponseUnchecked, aiResponseUncheckedCode, uncheckedMessage)
			return
		}
	}
	g.queue = append(g.queue, event)
	g.queued += len(raw)
	if int64(g.queued) > api.OutboundAIResponseCheckMaxBytes {
		g.stop(aiResponseUnchecked, aiResponseUncheckedCode, uncheckedMessage)
	}
}

// parse decodes an event's JSON and returns its texts in stream mode and
// the channels it ends. An event without JSON data has none and passes
// unchanged.
func (g *responseStreamGuard) parse(event *heldResponseEvent) ([]responseTextLoc, []string) {
	if !g.aws {
		data, ok := sseEventData(event.raw)
		if !ok {
			return nil, nil
		}
		value, ok := decodeResponseJSON(data)
		if !ok {
			return nil, nil
		}
		event.value = value
		event.encode = func(value any) ([]byte, bool) {
			encoded, err := encodeResponseJSON(value)
			return rebuildSSEEvent(event.raw, encoded), err == nil
		}
		return responseTexts(g.format, value, true), responseClosedChannels(g.format, value, "")
	}
	headerLen := int(binary.BigEndian.Uint32(event.raw[4:8]))
	headers := event.raw[awsEventStreamPreludeBytes : awsEventStreamPreludeBytes+headerLen]
	payload := event.raw[awsEventStreamPreludeBytes+headerLen : len(event.raw)-awsEventStreamTrailerBytes]
	messageType, eventType := awsEventStreamTypes(headers)
	if messageType != "" && messageType != "event" {
		return nil, nil
	}
	value, ok := decodeResponseJSON(payload)
	if !ok {
		return nil, nil
	}
	envelope, _ := value.(map[string]any)
	encoded, isChunk := envelope["bytes"].(string)
	if g.format != aiFormatBedrockInvoke || !isChunk {
		event.value = value
		event.encode = func(value any) ([]byte, bool) {
			payload, err := encodeResponseJSON(value)
			return encodeAWSEventMessage(headers, payload), err == nil
		}
		return responseTexts(g.format, value, true), responseClosedChannels(g.format, value, eventType)
	}
	// An invoke stream chunk wraps the model's own event in base64.
	inner, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, nil
	}
	innerValue, ok := decodeResponseJSON(inner)
	if !ok {
		return nil, nil
	}
	event.value = innerValue
	event.encode = func(value any) ([]byte, bool) {
		innerJSON, err := encodeResponseJSON(value)
		if err != nil {
			return nil, false
		}
		envelope["bytes"] = base64.StdEncoding.EncodeToString(innerJSON)
		payload, err := encodeResponseJSON(envelope)
		return encodeAWSEventMessage(headers, payload), err == nil
	}
	return responseTexts(g.format, innerValue, true), responseClosedChannels(g.format, innerValue, "")
}

// step scans each channel's new text, from OutboundAIResponseScanLookbackBytes
// before the checked text to the end, and releases the events whose text
// is checked. A channel's last OutboundAIResponseHoldBackBytes wait for
// more text unless the stream ended.
func (g *responseStreamGuard) step(final bool) {
	for _, key := range g.order {
		channel := g.channels[key]
		limit := len(channel.text)
		if !final && !channel.closed {
			limit -= api.OutboundAIResponseHoldBackBytes
		}
		if limit <= channel.checked {
			continue
		}
		from := max(0, channel.checked-api.OutboundAIResponseScanLookbackBytes)
		window := channel.text[from:]
		if blocked := g.scan.blockedBy(window); blocked != "" {
			g.block(blocked)
			return
		}
		if len(g.scan.redact) > 0 {
			for _, finding := range airedact.Find(window, g.scan.redact...) {
				channel.addSpan(finding.Kind, from+finding.Start, from+finding.End)
			}
		}
		channel.checked = limit
	}
	if !g.holdAll {
		g.release()
	}
}

// release renders, in order, every held event whose text is checked.
func (g *responseStreamGuard) release() {
	for len(g.queue) > 0 {
		event := g.queue[0]
		for _, delta := range event.deltas {
			if delta.end > delta.channel.checked {
				return
			}
		}
		g.out = append(g.out, g.render(event)...)
		g.queue = g.queue[1:]
		g.queued -= len(event.raw)
	}
}

// render returns an event as received, or rewritten when one of its texts
// is redacted.
func (g *responseStreamGuard) render(event *heldResponseEvent) []byte {
	changed := false
	for _, delta := range event.deltas {
		if text, redacted := delta.channel.segment(delta.start, delta.end, g.check); redacted {
			delta.loc.set(text)
			changed = true
		}
	}
	for _, whole := range event.wholes {
		whole.loc.set(whole.redacted)
		changed = true
	}
	if !changed || event.encode == nil {
		return event.raw
	}
	encoded, ok := event.encode(event.value)
	if !ok {
		return event.raw
	}
	return encoded
}

// end finishes the stream: the last event, a final scan, and the release
// or the moderation check.
func (g *responseStreamGuard) end() {
	if len(g.pending) > 0 && !g.aws {
		raw := g.pending
		g.pending = nil
		g.enqueue(raw)
		if g.ended {
			return
		}
	}
	g.step(true)
	if g.ended {
		return
	}
	g.ended = true
	if g.moderate == nil {
		return
	}
	if !g.holdAll {
		g.flagPending = true
		return
	}
	flagged, err := g.moderate(moderationResponseText(g.moderationTexts()))
	switch {
	case err != nil:
		g.check.moderation = aiModerationOutcome{result: aiModerationError}
		if g.onError == api.OutboundAIModerationOnErrorBlock {
			g.stop(aiResponseError, aiModerationUnavailable, moderationUnavailableReply)
			return
		}
		g.check.raise(aiResponseError)
	case len(flagged) > 0:
		g.check.moderation = aiModerationOutcome{result: aiModerationBlocked, categories: flagged}
		g.stop(aiResponseBlocked, aiResponseFlaggedCode, flaggedMessage(flagged))
		return
	default:
		g.check.moderation = aiModerationOutcome{result: aiModerationPassed}
	}
	g.release()
}

// runFlagCheck checks the released stream and records the result.
func (g *responseStreamGuard) runFlagCheck() {
	flagged, err := g.moderate(moderationResponseText(g.moderationTexts()))
	switch {
	case err != nil:
		g.check.moderation = aiModerationOutcome{result: aiModerationError}
		g.check.raise(aiResponseError)
	case len(flagged) > 0:
		g.check.moderation = aiModerationOutcome{result: aiModerationFlagged, categories: flagged}
		g.check.raise(aiResponseFlagged)
	default:
		g.check.moderation = aiModerationOutcome{result: aiModerationPassed}
	}
}

// moderationTexts is the stream's text with redactions applied: each
// channel's in order of appearance, or the whole texts of a stream
// without deltas.
func (g *responseStreamGuard) moderationTexts() []string {
	if len(g.order) == 0 {
		return g.wholeTexts
	}
	texts := make([]string, 0, len(g.order))
	for _, key := range g.order {
		texts = append(texts, g.channels[key].redactedText())
	}
	return texts
}

func (g *responseStreamGuard) block(blocked string) {
	g.check.blocked = blocked
	g.stop(aiResponseBlocked, aiResponseBlockedCode, blockedMessage(blocked))
}

// stop drops the held events and ends the stream with a gateway error.
func (g *responseStreamGuard) stop(result, code, message string) {
	g.check.raise(result)
	g.queue, g.queued, g.pending = nil, 0, nil
	if g.aws {
		g.out = append(g.out, awsGatewayException(code, message)...)
	} else {
		g.out = append(g.out, "event: error\ndata: "...)
		g.out = append(g.out, gatewayErrorPayload(code, message)...)
		g.out = append(g.out, '\n', '\n')
	}
	g.ended, g.stopped, g.flagPending = true, true, false
}

// addSpan records a finding, merging it with one it overlaps: a later scan
// sees a finding that grew with more text.
func (c *responseChannel) addSpan(kind airedact.Kind, start, end int) {
	for index := range c.spans {
		span := &c.spans[index]
		if start < span.end && end > span.start {
			span.start, span.end = min(span.start, start), max(span.end, end)
			return
		}
	}
	c.spans = append(c.spans, responseSpan{kind: kind, start: start, end: end})
	for index := len(c.spans) - 1; index > 0 && c.spans[index].start < c.spans[index-1].start; index-- {
		c.spans[index], c.spans[index-1] = c.spans[index-1], c.spans[index]
	}
}

// segment returns a released delta's text with redactions applied. Each
// finding's placeholder goes where its first unreleased byte was, and the
// rest of the finding is dropped.
func (c *responseChannel) segment(start, end int, check *aiResponseCheck) (string, bool) {
	var out []byte
	position, redacted := start, false
	for index := range c.spans {
		span := &c.spans[index]
		if span.end <= start || span.start >= end {
			continue
		}
		cut := max(span.start, start)
		out = append(out, c.text[position:max(position, cut)]...)
		if !span.placed {
			out = append(out, "[REDACTED:"+string(span.kind)+"]"...)
			span.placed = true
			check.addRedactions(map[airedact.Kind]int{span.kind: 1})
		}
		position, redacted = max(position, min(span.end, end)), true
	}
	if !redacted {
		return "", false
	}
	return string(append(out, c.text[position:end]...)), true
}

// redactedText is the channel's whole text with every finding redacted.
func (c *responseChannel) redactedText() string {
	var out strings.Builder
	position := 0
	for _, span := range c.spans {
		if span.start < position {
			continue
		}
		out.Write(c.text[position:span.start])
		out.WriteString("[REDACTED:" + string(span.kind) + "]")
		position = span.end
	}
	out.Write(c.text[min(position, len(c.text)):])
	return out.String()
}

func decodeResponseJSON(data []byte) (any, bool) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	return value, true
}

// sseEventData joins an event's data lines, without the field name and the
// one space after it.
func sseEventData(raw []byte) ([]byte, bool) {
	var data []byte
	found := false
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		value, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		if found {
			data = append(data, '\n')
		}
		data = append(data, bytes.TrimPrefix(value, []byte{' '})...)
		found = true
	}
	if !found || bytes.Equal(data, []byte("[DONE]")) {
		return nil, false
	}
	return data, true
}

// rebuildSSEEvent replaces an event's data lines with one line of data,
// keeping its other fields and its line endings.
func rebuildSSEEvent(raw, data []byte) []byte {
	newline := []byte("\n")
	if bytes.Contains(raw, []byte("\r\n")) {
		newline = []byte("\r\n")
	}
	var out []byte
	wrote := false
	for _, line := range bytes.Split(bytes.TrimRight(raw, "\r\n"), []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if bytes.HasPrefix(line, []byte("data:")) {
			if !wrote {
				out = append(append(append(out, "data: "...), data...), newline...)
				wrote = true
			}
			continue
		}
		out = append(append(out, line...), newline...)
	}
	return append(out, newline...)
}

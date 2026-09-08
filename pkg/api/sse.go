package api

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"sync"
)

// sseClient returns the HTTP client used for SSE streams. It
// overrides the SDK's 30s default — a typical apid log stream is
// long-lived (heartbeat every 15s) and a 30s timeout would terminate
// it prematurely.
//
// Callers MUST consume the returned *http.Response.Body and call
// Close on EOF or context cancellation; otherwise the underlying
// goroutine in cmd/apid/handlers_ext.go leaks.
//
// No test ever relies on the timeout being infinite — we only need
// it to be longer than the 30s default so a quiet stream isn't
// killed by an idle-disconnect. Set to 0 (no timeout) so any
// context-aware HTTP/2 keepalive handles the disconnect.
func (c *Client) sseClient() *http.Client {
	// Reuse the default HTTP client but reset its timeout to 0. This
	// shares the *Transport (TLS session, dialer) so a session reuse
	// across API calls does not waste a TLS handshake on every SSE
	// open.
	return &http.Client{
		Timeout:   0,
		Transport: c.http.Transport,
	}
}

// LogEvent is the parsed shape of one app-logs frame. SDK
// callers wrap StreamAppLogs with their own SSE parser and
// json.Unmarshal each frame's data into this type. Defined here so
// the SDK owns the public type instead of leaking the server-side
// shape from cmd/apid/handlers_ext.go.
//
// Move 4 (issue #254) adds InstanceID so a multi-instance
// consumer can disambiguate interleaved frames: two instances
// emitting in parallel produce one LogEvent stream whose
// InstanceID values differ. The field is additive — old SDKs
// ignore it via stdlib JSON's default lenient-decoder behaviour
// (Go's encoding/json does not error on unknown fields).
//
// Since: v1.x. The field is empty on the streamDeploymentLogs
// wire (one deployment = one build VM, no instance granularity).
// Level is populated when the workload emits a recognized structured JSON
// severity (`level` or `severity`); plain-text lines leave it empty.
type LogEvent struct {
	Seq        int64  `json:"seq"`
	InstanceID string `json:"instance,omitempty"` // per-instance id (Move 4); empty for deployment logs
	Stream     string `json:"stream"`             // "stdout" or "stderr"
	Line       string `json:"line"`
	Level      string `json:"level,omitempty"` // canonical structured-log severity
	WrittenAt  string `json:"written_at"`
}

// LogGapEvent is the parsed shape of an `event: gap` SSE frame
// (issue #517 / PR-B, AC4). The frame is emitted by gatewayd-internal when
// the per-instance log ring no longer retains the cursor the
// caller asked for; downstream consumers use Reason +
// GapToWrittenAt to surface a banner ("some earlier logs were
// lost; latest surviving line at <ts>"). ReplayAdvised is a hint
// — the server never reaches back into history, so "advised" is
// the SDK-facing verb even when the producer cannot replay.
//
// Field names mirror the wire exactly so the SDK caller can
// json.Unmarshal the frame's Data into this struct directly.
type LogGapEvent struct {
	Reason         string `json:"reason"`            // "seq_below_retained" | "since_below_retained"
	GapToWrittenAt string `json:"gap_to_written_at"` // RFC3339Nano
	ReplayAdvised  bool   `json:"replay_advised"`
}

// Event is one Server-Sent Event frame as defined by the WHATWG
// spec the apid SSE handlers emit. Field names mirror the wire
// (`event:`, `data:`, `id:`) so a caller reading the source can map
// them without consulting the spec.
//
// Multi-line `data:` values are joined with a single '\n' between
// lines (the WHATWG rule) and the trailing blank line is consumed
// silently. Heartbeat comment lines (`:` prefix) are dropped — the
// server emits them every 15 s to keep idle proxies from timing out
// and the SDK treats them as no-ops.
//
// Move 3 / M7.5: replaces the private sseLineReader in
// cmd/faas/commands2.go that discarded the `event:` name. The new
// decoder preserves the name so callers can switch on Event to
// distinguish `event: log` from `event: degraded` (the Move 4 stub
// shape) from `event: invocation_done` (the dashboard's Move 3 frame).
type Event struct {
	Event string // "" if absent on the wire
	Data  string // joined with '\n' if multi-line
	ID    string // "" if absent on the wire
}

// Decoder is the typed SSE parser. Construct one with NewDecoder
// over an io.Reader (the response body from a streaming HTTP call)
// and consume the channels from Events() until the error channel
// closes. Close releases the reader's internal goroutine on
// cancellation; the public API never blocks on the reader after
// Close.
//
// Threading: a single goroutine parses the underlying reader and
// pushes to the channels. Receivers may consume from any number of
// goroutines; the channel buffers (32) absorb the typical
// EventSource-style bursty delivery. A slow consumer blocks the
// parser, which back-pressures the underlying TCP read — this is
// the correct behavior for an SSE pipeline where the producer is
// always ahead of the renderer.
type Decoder struct {
	r       io.Reader
	out     chan Event
	err     chan error
	closeFn func() error

	once sync.Once
}

// NewDecoder returns a Decoder that reads SSE frames from r. The
// goroutine starts immediately; call Close to release it on
// cancellation. The returned Decoder is the typed-channel analog of
// the cmd/faas sseLineReader; the CLI's three streaming commands
// (faas logs, faas tail, faas queue tail) share this one parser.
//
// The reader is read until io.EOF or until Close is called; the
// error channel closes on either path. A non-EOF read error is
// delivered on the error channel before close.
//
// Caller contract: range over Events() like
//
//	dec := api.NewDecoder(body)
//	defer dec.Close()
//	for {
//	    select {
//	    case e, ok := <-dec.Events():
//	        if !ok { return }
//	        fmt.Println(e.Event, e.Data)
//	    case err := <-dec.Errors():
//	        if !errors.Is(err, io.EOF) {
//	            log.Print(err)
//	        }
//	        return
//	    }
//	}
func NewDecoder(r io.Reader) *Decoder {
	out := make(chan Event, 32)
	errs := make(chan error, 1)
	d := &Decoder{
		r:   r,
		out: out,
		err: errs,
	}
	go d.run()
	return d
}

// Events returns the channel of parsed SSE frames. Closes when the
// underlying reader returns io.EOF (or any other error) and the
// parser goroutine exits; callers should also drain the Errors
// channel to distinguish clean EOF from a transport error.
func (d *Decoder) Events() <-chan Event { return d.out }

// Errors returns the channel of terminal errors. A successful
// io.EOF is delivered as nil (the clean-close signal); a transport
// read error is delivered as the underlying error. The channel
// closes after the first error.
func (d *Decoder) Errors() <-chan error { return d.err }

// Close signals the parser goroutine to stop. Idempotent; safe to
// call from a defer. After Close the Events() and Errors() channels
// will close within a few ms (the parser's read syscall returns).
func (d *Decoder) Close() error {
	var err error
	d.once.Do(func() {
		if d.closeFn != nil {
			err = d.closeFn()
		}
		// Closing the underlying reader mid-parse surfaces as a
		// read error in the parser goroutine, which then closes
		// both channels. We don't push a sentinel because the
		// parser owns the channel lifecycle.
	})
	return err
}

// run is the parser loop. It reads r line-by-line, accumulating
// fields per the SSE spec:
//   - blank line flushes the current frame onto the Events channel
//   - "field: value" accumulates into the current frame
//   - ":" (no value) is a comment; the line is dropped
//   - "data:" without a value is allowed (the spec defines empty
//     data as a valid frame whose Data is "")
//
// Multi-line data values are joined with '\n'. The trailing blank
// line that delimits a frame is consumed silently. Lines without
// a ':' are silently dropped (the spec says unknown lines are
// ignored; the WHATWG "field name" form is `field: value` so a line
// without ':' has no field name).
func (d *Decoder) run() {
	defer close(d.out)
	defer close(d.err)

	scanner := bufio.NewScanner(d.r)
	// Allow long lines (max SSE field size in spec is unbounded;
	// practical cap is 64 KB which fits apid's largest frame
	// (deployment_log line is the worst case at 1 KB)).
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)

	var (
		event string
		data  []string
		id    string
	)
	flush := func() {
		if event == "" && len(data) == 0 && id == "" {
			// Heartbeat or pure-comment frame; nothing to emit.
			return
		}
		// Plain send: the parser goroutine has no other wakeup
		// path (it exits on scanner EOF or closeFn firing) and the
		// out channel is buffered (32) so a slow consumer doesn't
		// stall the network read long-term — eventually the bufio
		// reader blocks, back-pressuring the TCP socket.
		d.out <- Event{Event: event, Data: strings.Join(data, "\n"), ID: id}
		event = ""
		data = nil
		id = ""
	}

	for scanner.Scan() {
		line := scanner.Text()
		// Strip a single trailing CR (the spec allows CRLF; many
		// servers send LF only).
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			// Comment / heartbeat / keep-alive. Drop.
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			// Line without colon — spec says "ignore". We
			// silently drop to be lenient with non-conformant
			// senders.
			continue
		}
		field := line[:colon]
		value := line[colon+1:]
		// Spec: a single leading space after the colon is
		// stripped. More than one space is preserved verbatim.
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		case "id":
			id = value
			// The literal id value U+0000 NULL is reserved
			// (used as a reconnect sentinel per the WHATWG
			// spec); we don't model the sentinel so we accept
			// any value verbatim.
		default:
			// Unknown field (retry, etc.). Spec says ignore.
		}
	}
	// Scanner hit an error or EOF. Flush any partial frame so a
	// server that closes mid-frame doesn't lose the last event.
	flush()
	if err := scanner.Err(); err != nil {
		d.err <- err
		return
	}
	d.err <- io.EOF
}

// SetCloseFn lets callers attach a Close hook the Decoder can
// invoke when its own Close is called. The typical use is to
// close the http.Response.Body (which cancels the in-flight
// request); without this, a CLI Ctrl-C would wait for the
// underlying TCP read to return rather than terminating the
// process.
func (d *Decoder) SetCloseFn(fn func() error) {
	d.closeFn = fn
}

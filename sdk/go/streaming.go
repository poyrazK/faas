package faas

import (
	"io"

	"github.com/poyrazK/faas/sdk/go/internal/api"
)

// Decoder is a typed SSE frame reader. It is returned by Client's
// Stream* methods (StreamAppLogs, StreamDeploymentLogs, StreamEvents)
// and is NOT safe for concurrent use — each goroutine that consumes
// a stream should construct its own Decoder.
//
// Frames are delivered on the Events channel; the channel closes
// when the underlying io.Reader is exhausted or the Decoder is
// Close()d. Errors (network truncation, JSON decode failures) are
// delivered on the Errors channel; consumers should drain Errors
// alongside Events to avoid deadlocking the reader goroutine.
type Decoder = api.Decoder

// NewDecoder wraps an io.Reader as a typed SSE frame source. The
// returned Decoder must be Close()d to release the underlying
// reader's resources.
//
// Use the Client's Stream* methods instead of constructing a Decoder
// directly; the Stream* methods own the HTTP response body and tear
// it down on Close.
func NewDecoder(r io.Reader) *Decoder {
	return api.NewDecoder(r)
}

// Event is a single SSE frame. Data is the JSON-decoded payload
// (already unmarshaled into a Go value). Event is the named event
// type from the `event:` field, or "" for unnamed frames. ID is the
// SSE `id:` field used for resumability; Retry is the SSE `retry:`
// hint in milliseconds.
type Event = api.Event

// LogEvent is the typed shape of a log line delivered by the
// StreamAppLogs / StreamDeploymentLogs methods. Timestamp is RFC3339;
// Level is one of "info", "warn", "error" (server-defined); Message
// is the line text.
type LogEvent = api.LogEvent

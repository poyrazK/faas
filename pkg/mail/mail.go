// Package mail is the outbound-email seam for the one-box FaaS platform
// (spec §4.7, gap G4). apid and meterd hold a Sender interface, not a
// concrete type, so a future Resend or Postmark transport slots in
// without touching call sites.
//
// Production today wires NewLogSender (writes the message to slog); the
// noop sender is for tests. Both implementations are goroutine-safe.
package mail

import (
	"context"
	"errors"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// ErrTransient signals a retryable mail-send failure (network error
// or upstream 5xx). Callers can use errors.Is(err, ErrTransient) to
// decide whether to retry on a fresh transport. Today the quota
// warning + dunning send paths (cmd/apid handlers_auth.go) retry
// exactly on this condition.
var ErrTransient = errors.New("mail: transient send failure")

// Message is the cross-component outbound email payload. Fields map
// roughly to RFC 5322 — recipients, subject, plain + html body. Attachments
// are out of scope for M7 (the dunning + quota-warning emails are
// notification-style).
type Message struct {
	To       []string // RFC 5322 addresses; the sender validates each
	Subject  string
	TextBody string // plain text; required (HTML may be missing)
	HTMLBody string // optional; empty string drops the HTML alt
	// Headers are extra headers (e.g. List-Unsubscribe). nil is fine.
	Headers map[string]string
}

// Sender is the interface every transport implements. Implementations
// should not block on the network — caller wraps Send in a goroutine +
// timeout when the underlying transport is slow (M7 wires a log-only
// sender; future Postmark/Resend impls follow).
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// NoopSender discards every message. Used by tests and by daemons that
// haven't wired a transport yet (cmd/meterd in dev).
type NoopSender struct{}

// Send always returns nil.
func (NoopSender) Send(_ context.Context, _ Message) error { return nil }

// LogSender writes the message to a slog handler as a single record.
// Production wires this until the real transport (Postmark/Resend) is
// introduced — the log line is enough for the M7 acceptance gates and
// keeps the platform observable while the email-provider decision
// (gap G4) stays open.
type LogSender struct {
	log *slog.Logger
}

// NewLogSender returns a Sender that emits one INFO record per message.
func NewLogSender(log *slog.Logger) *LogSender {
	if log == nil {
		log = slog.Default()
	}
	return &LogSender{log: log}
}

// Send emits a structured log record with the message fields. Always
// succeeds — log-only delivery is not a delivery contract.
func (l *LogSender) Send(_ context.Context, msg Message) error {
	// CodeQL go/log-injection (CWE-117): msg.To and msg.Subject come from
	// the dunning-timer path in cmd/meterd (meter package constructs them
	// from account.Email + a templated subject) and from
	// cmd/apid/handlers_auth.go for the quota-warning path. Both flows
	// are accounts the customer controls, so a hostile slug/email change
	// could otherwise smuggle CR/LF into slog. Per-element sanitize
	// keeps the slice shape so downstream json marshalling is unchanged.
	to := make([]string, len(msg.To))
	for i, a := range msg.To {
		to[i] = logsanitize.Field(a)
	}
	l.log.Info("mail.send",
		"to", to,
		"subject", logsanitize.Field(msg.Subject),
		"has_html", msg.HTMLBody != "",
		"text_bytes", len(msg.TextBody))
	return nil
}

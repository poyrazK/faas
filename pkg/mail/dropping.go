package mail

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// DroppingSender is what an explicit FAAS_MAIL_TRANSPORT=noop resolves to on a
// box that is not marked FAAS_DEV. It delivers nothing, exactly like
// NoopSender, but every drop is an ERROR log record and a
// <daemon>_mail_send_failures_total{reason="no_transport"} increment.
//
// Production ran FAAS_MAIL_TRANSPORT=noop from the control-plane bootstrap
// without a provider. Signup minted its verification token, the send "worked",
// and no email, log line or metric ever said otherwise, so no new account could
// verify its address. NoopSender stays silent for tests and FAAS_DEV boxes.
//
// Send returns nil on purpose. Callers treat an error as a failed notice and
// retry it (dunning), and the public auth endpoints keep their anti-enumeration
// response shape; the signal is the log record and the counter.
type DroppingSender struct {
	log     *slog.Logger
	metrics Metrics
}

// NewDroppingSender returns a DroppingSender that logs to log (slog.Default
// when nil).
func NewDroppingSender(log *slog.Logger) *DroppingSender {
	if log == nil {
		log = slog.Default()
	}
	return &DroppingSender{log: log, metrics: NoopMetrics{}}
}

// SetMetrics wires the counter the daemon registered. Call it once at startup,
// before the first Send; a nil m keeps the discarding default.
func (d *DroppingSender) SetMetrics(m Metrics) {
	if d == nil || m == nil {
		return
	}
	d.metrics = m
}

// Send records the drop and returns nil. It never logs a recipient address.
func (d *DroppingSender) Send(_ context.Context, msg Message) error {
	d.metrics.RecordFailure(ReasonNoTransport)
	d.log.Error("mail.dropped",
		"transport", TransportNoop,
		"recipients", len(msg.To),
		"subject", logsanitize.Field(msg.Subject),
		"reason", "FAAS_MAIL_TRANSPORT=noop on a production box: no email leaves it")
	return nil
}

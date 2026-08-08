// Mailer factory — picks the right transport based on env. This is
// the one place apid wires its outbound email. Today:
//
//	log      → NewLogSender (explicit opt-in; also the unset default on dev)
//	resend   → NewResendSender (FAAS_MAIL_RESEND_API_KEY required)
//	postmark → NewPostmarkSender (FAAS_MAIL_POSTMARK_TOKEN required)
//	noop     → NoopSender (silent drop, for tests)
//
// The transport name comes from FAAS_MAIL_TRANSPORT. On misconfig
// (e.g. transport=resend without an API key) we fall back to the
// log sender with a warning — better than failing to start.
//
// An UNSET transport is resolved by defaultSender (issue #246): dev
// boxes keep the LogSender, everything else gets a NoopSender plus a
// WARN naming the fix. A production box that "sends" every dunning
// notice to the journal looks healthy while the customer receives
// nothing — that silent-success default is the bug #246 reports.
package mail

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Transport names. Add a new one here when wiring a new provider.
const (
	TransportNoop     = "noop"
	TransportLog      = "log"
	TransportResend   = "resend"
	TransportPostmark = "postmark"
)

// SenderFromEnv picks a Sender based on the FAAS_MAIL_TRANSPORT env
// variable. Defaults to "log" when unset. On misconfig (transport set
// but required envs missing), logs a warning and falls back to log so
// the daemon still boots.
//
// Resend: needs FAAS_MAIL_RESEND_API_KEY + FAAS_MAIL_FROM.
// Postmark: needs FAAS_MAIL_POSTMARK_TOKEN + FAAS_MAIL_FROM.
func SenderFromEnv(getenv func(string) string, log *slog.Logger) Sender {
	if log == nil {
		log = slog.Default()
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	switch strings.ToLower(getenv("FAAS_MAIL_TRANSPORT")) {
	case TransportNoop:
		log.Info("mail.transport", "transport", TransportNoop)
		return NoopSender{}
	case TransportResend:
		cfg := ResendConfig{
			APIKey: getenv("FAAS_MAIL_RESEND_API_KEY"),
			From:   getenv("FAAS_MAIL_FROM"),
		}
		s, err := NewResendSender(cfg)
		if err != nil {
			log.Warn("mail.transport invalid; falling back to log", "transport", TransportResend, "err", err)
			return NewLogSender(log)
		}
		log.Info("mail.transport", "transport", TransportResend)
		return s
	case TransportPostmark:
		cfg := PostmarkConfig{
			ServerToken: getenv("FAAS_MAIL_POSTMARK_TOKEN"),
			From:        getenv("FAAS_MAIL_FROM"),
		}
		s, err := NewPostmarkSender(cfg)
		if err != nil {
			log.Warn("mail.transport invalid; falling back to log", "transport", TransportPostmark, "err", err)
			return NewLogSender(log)
		}
		log.Info("mail.transport", "transport", TransportPostmark)
		return s
	case TransportLog:
		log.Info("mail.transport", "transport", TransportLog)
		return NewLogSender(log)
	case "":
		return defaultSender(getenv, log)
	default:
		log.Warn("mail.transport unknown; falling back to log",
			"transport", getenv("FAAS_MAIL_TRANSPORT"))
		return NewLogSender(log)
	}
}

// defaultSender resolves the transport when FAAS_MAIL_TRANSPORT is
// unset (issue #246).
//
// Dev boxes keep the LogSender so a developer can read the dunning
// copy straight out of the journal. Every other box gets a
// NoopSender and a WARN.
//
// Dropping loudly beats "sending" to a log file: spec §11 requires a
// production mail transport, and the 4-step dunning ladder (§4.7) is
// the most operationally critical mail on the platform. A box that
// logs instead of sending presents as healthy while a customer whose
// card failed receives nothing, hits the 7-day suspension, and only
// finds out via a support ticket.
func defaultSender(getenv func(string) string, log *slog.Logger) Sender {
	if isDevBox(getenv) {
		log.Info("mail.transport", "transport", TransportLog, "reason", "FAAS_DEV set")
		return NewLogSender(log)
	}

	log.Warn("mail.transport unset on a non-dev box; outbound email is DISCARDED",
		"transport", TransportNoop,
		"fix", "set FAAS_MAIL_TRANSPORT=resend|postmark plus FAAS_MAIL_FROM and the provider key",
		"docs", "https://docs.gregale.dev/ops/mail")

	return NoopSender{}
}

// isDevBox reports whether FAAS_DEV marks this as a development
// environment. Both "1" and "true" are accepted so an operator can
// use either spelling in a systemd EnvironmentFile.
func isDevBox(getenv func(string) string) bool {
	switch strings.ToLower(getenv("FAAS_DEV")) {
	case "1", "true":
		return true
	default:
		return false
	}
}

// Sentinel error for upstream-config failures (so tests can assert
// on misconfig instead of substring-matching the warning string).
var (
	ErrResendMissingAPIKey  = fmt.Errorf("mail: Resend APIKey required")
	ErrPostmarkMissingToken = fmt.Errorf("mail: Postmark ServerToken required")
)

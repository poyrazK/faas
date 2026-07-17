// Mailer factory — picks the right transport based on env. This is
// the one place apid wires its outbound email. Today:
//
//	log      → NewLogSender (default; safe for dev)
//	resend   → NewResendSender (FAAS_MAIL_RESEND_API_KEY required)
//	postmark → NewPostmarkSender (FAAS_MAIL_POSTMARK_TOKEN required)
//	noop     → NoopSender (silent drop, for tests)
//
// The transport name comes from FAAS_MAIL_TRANSPORT. On misconfig
// (e.g. transport=resend without an API key) we fall back to the
// log sender with a warning — better than failing to start.
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
	case TransportLog, "":
		log.Info("mail.transport", "transport", TransportLog)
		return NewLogSender(log)
	default:
		log.Warn("mail.transport unknown; falling back to log",
			"transport", getenv("FAAS_MAIL_TRANSPORT"))
		return NewLogSender(log)
	}
}

// Sentinel error for upstream-config failures (so tests can assert
// on misconfig instead of substring-matching the warning string).
var (
	ErrResendMissingAPIKey = fmt.Errorf("mail: Resend APIKey required")
	ErrPostmarkMissingToken = fmt.Errorf("mail: Postmark ServerToken required")
)

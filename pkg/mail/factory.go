// Mailer factory — picks the right transport based on env. This is
// the one place apid wires its outbound email. Today:
//
//	log      → NewLogSender (default; safe for dev)
//	resend   → NewResendSender (FAAS_MAIL_RESEND_API_KEY required)
//	postmark → NewPostmarkSender (FAAS_MAIL_POSTMARK_TOKEN required)
//	internal → private maild socket (explicit master flag + service token)
//	internal-producer → private producer route (master + producer gates and scoped token)
//	noop     → NoopSender (silent drop, for tests)
//
// The transport name comes from FAAS_MAIL_TRANSPORT. Selecting a live
// transport (resend or postmark) without its credential is fail-closed:
// SenderFromEnv returns (nil, ErrMailerMisconfigured) wrapped with the
// underlying config error, and the daemon refuses to boot (ADR-115 §D5).
//
// Issue #246 extends that contract from "credential missing" to
// "transport unselected". On a box that is not marked FAAS_DEV, both an
// unset FAAS_MAIL_TRANSPORT and an unrecognised one now refuse to boot
// too, because falling back to LogSender means a production box reports
// healthy while silently dropping the dunning ladder into the journal.
//
// Two escape hatches remain, and both are explicit:
//
//	FAAS_MAIL_TRANSPORT=log   keep mail in the journal, anywhere
//	FAAS_DEV=1                dev/CI box; unset transport resolves to log
//
// Operator note: a box that relied on the old implicit default will stop
// booting after this change. That is the intent — see
// docs/adr/115-transactional-email-provider-resend.md and the deploy note
// on the issue #246 PR.
package mail

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

// Transport names. Add a new one here when wiring a new provider.
const (
	TransportNoop             = "noop"
	TransportLog              = "log"
	TransportResend           = "resend"
	TransportPostmark         = "postmark"
	TransportInternal         = "internal"
	TransportInternalProducer = "internal-producer"
)

// ErrMailerMisconfigured is the sentinel returned by SenderFromEnv when
// the operator selected a live transport (resend or postmark) but the
// required credential env var (FAAS_MAIL_RESEND_API_KEY or
// FAAS_MAIL_POSTMARK_TOKEN) is empty. apid + meterd catch this on boot,
// log a single ERROR record, and exit non-zero so the operator cannot
// accidentally run a daemon that silently drops email into slog.
// See ErrMailUnsetInProd for the unset-transport case, which issue #246
// gave the same fail-closed treatment.
var ErrMailerMisconfigured = errors.New("mail: transport misconfigured")

// ErrMailUnsetInProd is returned when FAAS_MAIL_TRANSPORT is unset on a
// box that is not marked FAAS_DEV. Before issue #246 this branch resolved
// to LogSender, so a production box booted clean, logged
// "mail.transport transport=log", and wrote every dunning notice to the
// journal instead of sending it — presenting as healthy while the
// customer received nothing, hit the 7-day suspension, and found out via
// a support ticket. Silence is the one failure mode a mail transport must
// not have, so the unset case now refuses to boot.
var ErrMailUnsetInProd = errors.New("mail: no transport selected on a production box")

// ErrMailUnknownTransport is returned when FAAS_MAIL_TRANSPORT holds a
// name we do not recognise on a non-dev box. A typo ("resned") used to
// fall back to LogSender, which is the same silent drop as the unset
// case with none of the visibility — the operator believes they selected
// a live transport.
var ErrMailUnknownTransport = errors.New("mail: unknown transport")

// ErrResendMissingAPIKey / ErrResendMissingFrom /
// ErrPostmarkMissingToken / ErrPostmarkMissingFrom document the four
// concrete credential-shape errors NewResendSender / NewPostmarkSender
// return. They are wrapped by ErrMailerMisconfigured at the factory
// layer so callers can errors.Is against either shape.
var (
	ErrResendMissingAPIKey  = errors.New("mail: Resend APIKey required")
	ErrResendMissingFrom    = errors.New("mail: Resend From address required")
	ErrPostmarkMissingToken = errors.New("mail: Postmark ServerToken required")
	ErrPostmarkMissingFrom  = errors.New("mail: Postmark From address required")
)

// SenderFromEnv picks a Sender based on the FAAS_MAIL_TRANSPORT env
// variable. Returns a non-nil error — and so refuses to boot the daemon —
// when the box is not FAAS_DEV and the transport is unset, unrecognised,
// or a live transport whose credential is missing. Explicit "log" and
// "noop" always succeed.
//
// Resend: needs FAAS_MAIL_RESEND_API_KEY + FAAS_MAIL_FROM.
// Postmark: needs FAAS_MAIL_POSTMARK_TOKEN + FAAS_MAIL_FROM.
func SenderFromEnv(getenv func(string) string, log *slog.Logger) (Sender, error) {
	if log == nil {
		log = slog.Default()
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	switch strings.ToLower(getenv("FAAS_MAIL_TRANSPORT")) {
	case TransportNoop:
		log.Info("mail.transport", "transport", TransportNoop)
		return NoopSender{}, nil
	case TransportInternal:
		socket := getenv("FAAS_INTERNAL_MAIL_SOCKET")
		if socket == "" {
			socket = internalmail.DefaultSocket
		}
		s, err := NewInternalSender(socket, getenv("FAAS_INTERNAL_MAIL_TOKEN"), getenv("FAAS_MAIL_FROM"), func() bool { return internalmail.Enabled(getenv) })
		if err != nil {
			return nil, fmt.Errorf("mail: internal transport: %w: %w", ErrMailerMisconfigured, err)
		}
		log.Info("mail.transport", "transport", TransportInternal)
		return s.WithScheduling(func() bool { return internalmail.SchedulingEnabled(getenv) }), nil
	case TransportInternalProducer:
		if !internalmail.ProducersEnabled(getenv) {
			return nil, fmt.Errorf("mail: internal producer transport: %w: %w", ErrMailerMisconfigured, internalmail.ErrDisabled)
		}
		socket := getenv("FAAS_INTERNAL_MAIL_SOCKET")
		if socket == "" {
			socket = internalmail.DefaultSocket
		}
		s, err := NewInternalProducerSender(socket, getenv("FAAS_INTERNAL_MAIL_PRODUCER_ID"), getenv("FAAS_INTERNAL_MAIL_PRODUCER_TOKEN"), getenv("FAAS_MAIL_FROM"), func() bool { return internalmail.ProducersEnabled(getenv) })
		if err != nil {
			return nil, fmt.Errorf("mail: internal producer transport: %w: %w", ErrMailerMisconfigured, err)
		}
		log.Info("mail.transport", "transport", TransportInternalProducer)
		return s.WithScheduling(func() bool { return internalmail.SchedulingEnabled(getenv) }), nil
	case TransportResend:
		cfg := ResendConfig{
			APIKey: getenv("FAAS_MAIL_RESEND_API_KEY"),
			From:   getenv("FAAS_MAIL_FROM"),
		}
		s, err := NewResendSender(cfg)
		if err != nil {
			return nil, fmt.Errorf("mail: transport=resend: %w: %w", ErrMailerMisconfigured, err)
		}
		log.Info("mail.transport", "transport", TransportResend)
		return s, nil
	case TransportPostmark:
		cfg := PostmarkConfig{
			ServerToken: getenv("FAAS_MAIL_POSTMARK_TOKEN"),
			From:        getenv("FAAS_MAIL_FROM"),
		}
		s, err := NewPostmarkSender(cfg)
		if err != nil {
			return nil, fmt.Errorf("mail: transport=postmark: %w: %w", ErrMailerMisconfigured, err)
		}
		log.Info("mail.transport", "transport", TransportPostmark)
		return s, nil
	case TransportLog:
		// Explicit opt-in: always honoured, on dev and production alike.
		// This is the documented escape hatch for an operator who really
		// does want mail in the journal.
		log.Info("mail.transport", "transport", TransportLog)
		return NewLogSender(log), nil
	case "":
		if !IsDevMode(getenv) {
			return nil, fmt.Errorf("%w: FAAS_MAIL_TRANSPORT is unset"+
				" (set it to resend or postmark plus FAAS_MAIL_FROM and the provider key;"+
				" set FAAS_MAIL_TRANSPORT=log to keep mail in the journal,"+
				" or FAAS_DEV=1 on a dev box)", ErrMailUnsetInProd)
		}
		log.Info("mail.transport", "transport", TransportLog, "reason", "FAAS_DEV")
		return NewLogSender(log), nil
	default:
		// An unrecognised name is a typo (e.g. "resned"). Falling back to
		// LogSender here is the same silent-drop failure as the unset case,
		// so it fails closed on production boxes too.
		if !IsDevMode(getenv) {
			return nil, fmt.Errorf("%w: FAAS_MAIL_TRANSPORT=%q is not a known transport"+
				" (want one of: resend, postmark, internal, log, noop)",
				ErrMailUnknownTransport, getenv("FAAS_MAIL_TRANSPORT"))
		}
		log.Warn("mail.transport unknown; falling back to log",
			"transport", getenv("FAAS_MAIL_TRANSPORT"))
		return NewLogSender(log), nil
	}
}

// devTruthyLiterals are the values FAAS_DEV accepts as "this is a dev
// box". Matches the repo-wide convention (pkg/api/flags.go:29,
// cmd/apid/rekey_runner.go:85).
var devTruthyLiterals = []string{"1", "true", "yes", "on"}

// IsDevMode reports whether FAAS_DEV marks this as a dev/CI box. Only an
// explicitly truthy value counts: unset, empty, "0" and anything
// unrecognised are all production, because the safe default for an
// ambiguous value is the strict one.
func IsDevMode(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	v := strings.ToLower(strings.TrimSpace(getenv("FAAS_DEV")))
	for _, literal := range devTruthyLiterals {
		if v == literal {
			return true
		}
	}
	return false
}

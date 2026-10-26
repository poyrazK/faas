// Package mailoutbox owns apid's transactional notification handoff. It never
// writes maild's delivery state or grants customer email capabilities.
package mailoutbox

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	platformmail "github.com/onebox-faas/faas/pkg/mail"
)

type recoverySender interface {
	SendTimedWithRecovery(context.Context, platformmail.Message, internalmail.MessageTiming, string) error
}

type Service struct {
	pool                      *pgxpool.Pool
	queries                   *q.Queries
	namespace, producer, from string
	sender                    recoverySender
	transport                 *platformmail.InternalSender
	verification              *credentialConfig
	passwordReset             *credentialConfig
	magicLink                 *credentialConfig
	mfaDisable                *credentialConfig
	mfaRecoveryAlert          *credentialConfig
	mfaDisabledAlert          *credentialConfig
	quotaWarning              *credentialConfig
	quotaWarningUnsubscribe   string
	nonpaymentDeletion        *credentialConfig
	accountSuspended          *credentialConfig
	managedPostgresCap        *credentialConfig
	deploymentFailed          *credentialConfig
	deploymentDashboardOrigin string
	subscriptionEnded         *credentialConfig
	accountRestored           *credentialConfig
	paymentFailed             *credentialConfig
	mfaEnabledAlert           *credentialConfig
	passwordChangedAlert      *credentialConfig
	requested, enabled        func() bool
}

// NewFromEnv performs no DB, credential or socket configuration reads with
// the new flag off. Enabled callers must explicitly select scoped maild and
// timed admission; another provider is never substituted for a pending intent.
func NewFromEnv(pool *pgxpool.Pool, getenv func(string) string) (*Service, error) {
	if getenv(internalmail.ProducerOutboxEnableEnv) != "1" {
		return nil, nil
	}
	on := func() bool {
		return internalmail.ProducerOutboxEnabled(getenv) && internalmail.ProducersEnabled(getenv) && internalmail.SchedulingEnabled(getenv)
	}
	if !on() || getenv("FAAS_MAIL_TRANSPORT") != platformmail.TransportInternalProducer {
		return nil, errors.New("producer outbox requires master, producer, scheduling gates and internal-producer transport")
	}
	if pool == nil {
		return nil, errors.New("producer outbox requires apid's PostgreSQL owner pool")
	}
	namespace := getenv("FAAS_INTERNAL_MAIL_NAMESPACE")
	if namespace == "" {
		namespace = "platform"
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(namespace) {
		return nil, errors.New("invalid producer outbox namespace")
	}
	from := getenv("FAAS_MAIL_FROM")
	if strings.ContainsAny(from, "\r\n\x00") {
		return nil, errors.New("invalid producer outbox From address")
	}
	address, err := mail.ParseAddress(from)
	if err != nil {
		return nil, errors.New("invalid producer outbox From address")
	}
	if _, err := internalmail.NormalizeSuppressionAddress(address.Address); err != nil {
		return nil, errors.New("invalid producer outbox From mailbox")
	}
	from = address.String()
	socket := getenv("FAAS_INTERNAL_MAIL_SOCKET")
	if socket == "" {
		socket = internalmail.DefaultSocket
	}
	producer := getenv("FAAS_INTERNAL_MAIL_PRODUCER_ID")
	sender, err := platformmail.NewInternalProducerSender(socket, producer, getenv("FAAS_INTERNAL_MAIL_PRODUCER_TOKEN"), from, on)
	if err != nil {
		return nil, errors.New("invalid producer outbox scoped maild configuration")
	}
	return &Service{pool: pool, queries: q.New(), namespace: namespace, producer: producer, from: from, transport: sender, sender: sender.WithScheduling(on).WithOutbox(on), requested: func() bool { return getenv(internalmail.ProducerOutboxEnableEnv) == "1" }, enabled: on}, nil
}

func (s *Service) Requested() bool { return s != nil && s.requested != nil && s.requested() }
func (s *Service) Enabled() bool   { return s != nil && s.enabled != nil && s.enabled() }

func (s *Service) Ready(ctx context.Context) error {
	if !s.Enabled() {
		return internalmail.ErrDisabled
	}
	return s.queries.Ready(ctx, s.pool)
}

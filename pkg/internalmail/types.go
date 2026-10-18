// Package internalmail implements Gregale's private, feature-gated mail
// service. Its HTTP handler belongs on a protected Unix socket, never apid's
// customer route table. SMTP delivery belongs to a separately enabled worker.
package internalmail

import (
	"context"
	"errors"
	"time"
)

var (
	ErrDisabled   = errors.New("internal mail disabled")
	ErrInvalid    = errors.New("invalid internal mail request")
	ErrNotFound   = errors.New("internal mail resource not found")
	ErrConflict   = errors.New("internal mail idempotency conflict")
	ErrQuota      = errors.New("internal mail safeguard exceeded")
	ErrUnverified = errors.New("internal mail sending domain is not verified")
	ErrLeaseLost  = errors.New("internal mail lease lost")
	ErrNotAllowed = errors.New("internal mail recipient is outside the pilot allowlist")
)

type Domain struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Status             string     `json:"status"`
	Selector           string     `json:"selector"`
	VerificationToken  string     `json:"-"`
	PublicKey          string     `json:"-"`
	SealedKey          []byte     `json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
	DKIMKeyID          string     `json:"dkim_key_id,omitempty"`
	DNSRevision        int64      `json:"dns_revision"`
	AuthorizationState string     `json:"authorization_state"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
	LastVerifiedAt     *time.Time `json:"last_verified_at,omitempty"`
	VerifiedSendingIP  string     `json:"verified_sending_ip,omitempty"`
	CheckResult        string     `json:"check_result"`
	FailureCount       int32      `json:"failure_count"`
	FirstFailureAt     *time.Time `json:"first_failure_at,omitempty"`
}

type DNSRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DomainResponse struct {
	Domain
	Records []DNSRecord `json:"records"`
}

type SendRequest struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	Text    string            `json:"text"`
	HTML    string            `json:"html,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	MessageTiming
}

type Message struct {
	ID                 string     `json:"id"`
	DomainID           string     `json:"domain_id"`
	ProducerID         string     `json:"producer_id,omitempty"`
	SenderAddress      string     `json:"-"`
	IdempotencyKey     string     `json:"-"`
	SealedPayload      []byte     `json:"-"`
	PayloadFingerprint []byte     `json:"-"`
	PayloadPrunedAt    *time.Time `json:"payload_pruned_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	MessageTiming
}

type Recipient struct {
	ID              string    `json:"id"`
	MessageID       string    `json:"message_id"`
	Address         string    `json:"address"`
	Status          string    `json:"status"`
	Attempts        int32     `json:"attempts"`
	LeaseID         string    `json:"-"`
	Code            string    `json:"code,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
	SigningSelector string    `json:"signing_selector,omitempty"`
}

type MessageResponse struct {
	Message
	Recipients []Recipient `json:"recipients"`
}

// Report is a trusted relay's observation, not a customer-supplied webhook.
// "accepted" means the destination SMTP server accepted the message; it does
// not attest inbox placement. A relay deferral never requeues a submitted mail.
type Report struct {
	EventID     string `json:"event_id"`
	RecipientID string `json:"recipient_id"`
	Kind        string `json:"kind"`
	Code        string `json:"code,omitempty"`
	// Set only by the trusted DSN parser, never by the JSON report API.
	RecipientAddress string `json:"-"`
	// A parsed external report must also match the original sending domain.
	// Neither correlation field can be supplied through the JSON report API.
	MessageDomain string `json:"-"`
	// Assigned only after source authentication by a private adapter. JSON
	// reports cannot impersonate an enrolled provider or the Postfix bridge.
	suppressionSource suppressionSource
}

type Event struct {
	ID          string    `json:"id"`
	RecipientID string    `json:"recipient_id"`
	Kind        string    `json:"kind"`
	Code        string    `json:"code,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Store owns the internal namespace's state. Message creation and its recipient
// outbox are atomic; lease tokens fence every worker transition.
type Store interface {
	CreateDomain(context.Context, Domain) (Domain, error)
	Domain(context.Context, string) (Domain, error)
	DomainByName(context.Context, string) (Domain, error)
	SetDomainStatus(context.Context, string, string) error
	Enqueue(context.Context, Message, []Recipient) (Message, bool, error)
	Message(context.Context, string) (MessageResponse, error)
	Events(context.Context, string) ([]Event, error)
	Claim(context.Context) (Recipient, error)
	BeginSubmission(context.Context, Recipient) error
	Settle(context.Context, Recipient, string, string, time.Time) error
	Suppressed(context.Context, string) (bool, error)
	Report(context.Context, Report) error
}

type Codec interface {
	Seal(string, []byte) ([]byte, error)
	Open(string, []byte) ([]byte, error)
	Fingerprint(string, []byte) ([]byte, error)
}

type TXTResolver interface {
	LookupTXT(context.Context, string) ([]string, error)
}

type Envelope struct {
	From        string
	To          string
	Data        []byte
	RecipientID string
	// ExpiresAt bounds a new SMTP handoff. Once DATA may have been accepted,
	// the transport must preserve submitted/uncertain custody after expiry.
	ExpiresAt *time.Time
}

// Submit returns a state and a bounded reason code. A network failure after
// transmitting DATA is "uncertain", never a safe automatic retry.
type Transport interface {
	Submit(context.Context, Envelope) (state, code string)
}

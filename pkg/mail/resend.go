// Resend transport for pkg/mail (gap G4 closure).
//
// Resend is a transactional-email API. POST https://api.resend.com/emails
// with `Authorization: Bearer <api_key>` and a JSON body that has at
// minimum {from, to, subject, text} (HTML optional). Response is 200
// + {"id": "..."} on success, 4xx + {"name": "..."} on validation
// failures, 5xx for upstream issues, 429 for rate-limit.
//
// We use the bare net/http client + json package (no SDK dependency).
// Sender's contract is non-blocking; callers wrap in a goroutine +
// timeout when slowness matters.
//
// Idempotency: when Message.MessageID is non-empty, the request
// carries an `Idempotency-Key` header so a network-level retry that
// the upstream already accepted is deduplicated within Resend's
// 24-hour replay window instead of double-charging.
//
// Retry classification (issue #246 acceptance item 3):
//   - 429 Too Many Requests  -> TransientError{RetryAfter from header}
//   - 5xx                    -> TransientError{RetryAfter from header}
//   - 4xx (other)            -> permanent error (not ErrTransient)
//   - network / dial failure -> TransientError{Status: 0}
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// ResendConfig is the configuration for the Resend transport.
// APIKey is the Bearer token from https://resend.com/api-keys.
// From is the sender ("you@yourdomain.test"); must be on a verified
// domain. BaseURL is the API root (defaults to the public endpoint
// when empty). HTTPClient is optional (defaults to a 10s-timeout
// client); tests inject a recording stub.
type ResendConfig struct {
	APIKey     string
	From       string
	BaseURL    string
	HTTPClient *http.Client
	Log        *slog.Logger
}

// ResendSender implements Sender via the Resend HTTP API.
type ResendSender struct {
	cfg ResendConfig
}

// ResendRequest is the JSON body the Resend API expects. Exported so
// tests in package mail_test can decode against it.
//
// `Headers` carries caller-supplied RFC 5322 headers (e.g.
// List-Unsubscribe + List-Unsubscribe-Post for bulk-sender compliance
// per RFC 8058). Resend's API treats these as opaque — they're
// attached verbatim to the outbound message, not parsed by Resend
// itself. Empty map omits the field so a non-bulk email doesn't
// add a noisy `"headers":{}` to the wire payload.
type ResendRequest struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	Text    string            `json:"text"`
	HTML    string            `json:"html,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// resendError is the JSON shape Resend returns on 4xx/5xx. We surface
// the Name field as the error so log lines are useful.
type resendError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// NewResendSender validates cfg and returns a Sender. An empty APIKey
// or From is an error — we'd rather fail closed than silently drop.
func NewResendSender(cfg ResendConfig) (Sender, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, ErrResendMissingAPIKey
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, ErrResendMissingFrom
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.resend.com"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &ResendSender{cfg: cfg}, nil
}

// Send POSTs msg to Resend. Translates 4xx/5xx into wrapped errors
// (with %w) so callers can errors.Is against ErrTransient to decide
// whether to retry.
func (s *ResendSender) Send(ctx context.Context, msg Message) error {
	_, err := s.send(ctx, msg, false)
	return err
}

// SendWithReceipt requires the provider's message UUID for authenticated event
// correlation. Ambiguous success is retried with the original idempotency key.
// Send keeps its existing error-only contract for other platform mail streams.
func (s *ResendSender) SendWithReceipt(ctx context.Context, msg Message) (string, error) {
	return s.send(ctx, msg, true)
}

func (s *ResendSender) send(ctx context.Context, msg Message, receipt bool) (string, error) {
	body, err := json.Marshal(ResendRequest{
		From:    s.cfg.From,
		To:      msg.To,
		Subject: msg.Subject,
		Text:    msg.TextBody,
		HTML:    msg.HTMLBody,
		// Caller-supplied extra headers (List-Unsubscribe etc) go
		// in the JSON body — Resend's API treats custom HTTP
		// request headers as unrelated to the email payload and
		// drops them. Verified by the dry-run + transport
		// integration tests.
		Headers: msg.Headers,
	})
	if err != nil {
		return "", fmt.Errorf("mail: resend: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.BaseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("mail: resend: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	// Idempotency-Key dedupes a retry that the upstream already
	// accepted within its 24h window — see pkg/mail/mail.go
	// Message.MessageID. The dunning + quota-warning senders
	// derive a stable id from (account_id, template, day) so an
	// HTTP-level retry never sends twice.
	//
	// Caller-supplied extra headers (List-Unsubscribe, etc.) are
	// delivered via the JSON body above — Resend's API drops
	// custom HTTP request headers, so the right channel is the
	// `headers` field on the JSON payload.
	if msg.MessageID != "" {
		req.Header.Set("Idempotency-Key", msg.MessageID)
	}

	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		// Network errors are transient by definition (caller can
		// retry on a fresh transport).
		return "", &TransientError{Err: fmt.Errorf("mail: resend: do: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		providerID := ""
		if receipt {
			var accepted struct {
				ID string `json:"id"`
			}
			idErr := json.Unmarshal(rawBody, &accepted)
			parsed, parseErr := uuid.Parse(accepted.ID)
			if idErr != nil || parseErr != nil || parsed == uuid.Nil || parsed.String() != accepted.ID {
				return "", &TransientError{Err: fmt.Errorf("mail: resend: accepted response lacks a valid message receipt")}
			}
			providerID = accepted.ID
		}
		// CodeQL go/log-injection (CWE-117): msg.To + msg.Subject are
		// caller-supplied (constructed from account.Email / templated
		// subject by the dunning + quota-warning senders). Sanitize
		// before logging so a hostile slug/email cannot smuggle CR/LF
		// into the success-path audit line.
		to := make([]string, len(msg.To))
		for i, a := range msg.To {
			to[i] = logsanitize.Field(a)
		}
		s.cfg.Log.Info("mail.resend.ok",
			"to", to, "subject", logsanitize.Field(msg.Subject), "status", resp.StatusCode)
		return providerID, nil
	}
	var rerr resendError
	_ = json.Unmarshal(rawBody, &rerr)
	detail := rerr.Name
	if rerr.Message != "" {
		detail = rerr.Name + ": " + rerr.Message
	}
	if detail == "" {
		detail = string(rawBody)
	}
	// 429 + 5xx are transient; honour Retry-After so the retry
	// decorator can back off with the provider's hint instead of
	// guessing.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		te := &TransientError{Status: resp.StatusCode}
		if ra := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ra > 0 {
			te.RetryAfter = ra
		}
		return "", fmt.Errorf("mail: resend: %s: %w", detail, te)
	}
	return "", fmt.Errorf("mail: resend: %d: %s", resp.StatusCode, detail)
}

// parseRetryAfter interprets an RFC 7231 §7.1.3 Retry-After header.
// Resend uses delta-seconds today; we still accept the HTTP-date form
// so a future provider or proxy does not silently get retried too
// soon. Returns 0 for empty / unparseable / past values so the caller
// can fall back to its base delay.
func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

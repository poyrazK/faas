// Resend transport for pkg/mail (gap G4 closure).
//
// Resend is a transactional-email API. POST https://api.resend.com/emails
// with `Authorization: Bearer <api_key>` and a JSON body that has at
// minimum {from, to, subject, text} (HTML optional). Response is 200
// + {"id": "..."} on success, 4xx + {"name": "..."} on validation
// failures, 5xx for upstream issues.
//
// We use the bare net/http client + json package (no SDK dependency).
// Sender's contract is non-blocking; callers wrap in a goroutine +
// timeout when slowness matters.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
type ResendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	HTML    string   `json:"html,omitempty"`
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
		return nil, errors.New("mail: Resend APIKey is required")
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, errors.New("mail: Resend From is required")
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
	body, err := json.Marshal(ResendRequest{
		From:    s.cfg.From,
		To:      msg.To,
		Subject: msg.Subject,
		Text:    msg.TextBody,
		HTML:    msg.HTMLBody,
	})
	if err != nil {
		return fmt.Errorf("mail: resend: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.BaseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mail: resend: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		// Network errors are transient by definition (caller can
		// retry on a fresh transport).
		return fmt.Errorf("mail: resend: do: %w", errors.Join(err, ErrTransient))
	}
	defer func() { _ = resp.Body.Close() }()

	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
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
		return nil
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
	// 5xx → ErrTransient so caller may retry.
	if resp.StatusCode >= 500 {
		return fmt.Errorf("mail: resend: %d %s: %w", resp.StatusCode, detail, ErrTransient)
	}
	return fmt.Errorf("mail: resend: %d: %s", resp.StatusCode, detail)
}

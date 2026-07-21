// Postmark transport for pkg/mail (gap G4 closure).
//
// Postmark is a transactional-email API. POST
// https://api.postmarkapp.com/email with `X-Postmark-Server-Token:
// <server_token>` and `Content-Type: application/json` + a body that
// has at minimum {From, To, Subject, TextBody} (HtmlBody optional).
// Response is 200 + {"To", "SubmittedAt", "MessageID", "ErrorCode",
// "Message"} on success; non-200 means the request failed.
//
// We use the bare net/http + json packages (no SDK dependency).
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

// PostmarkConfig is the configuration for the Postmark transport.
// ServerToken is the per-server token from
// https://postmarkapp.com/servers/{id}/tokens.
// From is the verified sender ("you@yourdomain.test"). BaseURL is
// the API root (defaults to the public endpoint when empty).
// HTTPClient is optional (defaults to a 10s-timeout client).
type PostmarkConfig struct {
	ServerToken string
	From        string
	BaseURL     string
	HTTPClient  *http.Client
	Log         *slog.Logger
}

// PostmarkSender implements Sender via the Postmark HTTP API.
type PostmarkSender struct {
	cfg PostmarkConfig
}

// PostmarkRequest is the JSON body the Postmark API expects. Exported
// so tests can decode against it.
type PostmarkRequest struct {
	From     string `json:"From"`
	To       string `json:"To"`
	Subject  string `json:"Subject"`
	TextBody string `json:"TextBody"`
	HtmlBody string `json:"HtmlBody,omitempty"`
}

// postmarkResponse is the success payload. We don't currently use it
// beyond logging.
type postmarkResponse struct {
	To          string `json:"To"`
	SubmittedAt string `json:"SubmittedAt"`
	MessageID   string `json:"MessageID"`
	ErrorCode   int    `json:"ErrorCode"`
	Message     string `json:"Message"`
}

// NewPostmarkSender validates cfg and returns a Sender. Empty
// ServerToken or From is an error — we fail closed.
func NewPostmarkSender(cfg PostmarkConfig) (Sender, error) {
	if strings.TrimSpace(cfg.ServerToken) == "" {
		return nil, errors.New("mail: Postmark ServerToken is required")
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, errors.New("mail: Postmark From is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.postmarkapp.com"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &PostmarkSender{cfg: cfg}, nil
}

// Send POSTs msg to Postmark. msg.To is joined with ", " since the API
// accepts a single comma-separated string.
func (s *PostmarkSender) Send(ctx context.Context, msg Message) error {
	body, err := json.Marshal(PostmarkRequest{
		From:     s.cfg.From,
		To:       strings.Join(msg.To, ", "),
		Subject:  msg.Subject,
		TextBody: msg.TextBody,
		HtmlBody: msg.HTMLBody,
	})
	if err != nil {
		return fmt.Errorf("mail: postmark: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.BaseURL+"/email", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mail: postmark: new request: %w", err)
	}
	req.Header.Set("X-Postmark-Server-Token", s.cfg.ServerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("mail: postmark: do: %w", errors.Join(err, ErrTransient))
	}
	defer func() { _ = resp.Body.Close() }()

	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// CodeQL go/log-injection (CWE-117): msg.To + msg.Subject are
		// caller-supplied (account.Email + templated subject). Sanitize
		// before logging the success-path audit line.
		to := make([]string, len(msg.To))
		for i, a := range msg.To {
			to[i] = logsanitize.Field(a)
		}
		s.cfg.Log.Info("mail.postmark.ok",
			"to", to, "subject", logsanitize.Field(msg.Subject), "status", resp.StatusCode)
		return nil
	}
	var perr postmarkResponse
	_ = json.Unmarshal(rawBody, &perr)
	detail := perr.Message
	if perr.ErrorCode != 0 {
		detail = fmt.Sprintf("error_code=%d %s", perr.ErrorCode, perr.Message)
	}
	if detail == "" {
		detail = string(rawBody)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("mail: postmark: %d %s: %w", resp.StatusCode, detail, ErrTransient)
	}
	return fmt.Errorf("mail: postmark: %d: %s", resp.StatusCode, detail)
}

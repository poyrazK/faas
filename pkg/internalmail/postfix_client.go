package internalmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PostfixBridge acknowledges a queued DSN only after maild has committed its
// observation. Postfix, rather than another local spool, owns retry durability.
type PostfixBridge struct {
	token         string
	enabled       func() bool
	client        *http.Client
	queues        func(context.Context) (RelayQueues, error)
	healthEnabled func() bool
	healthSMTP    string
	probe         func(context.Context, string) error
}

func NewPostfixBridge(socket, token string, enabled func() bool) (*PostfixBridge, error) {
	if enabled == nil || !enabled() {
		return nil, ErrDisabled
	}
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || !validToken(token) {
		return nil, ErrInvalid
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, DisableKeepAlives: true}
	return &PostfixBridge{token: token, enabled: enabled, queues: ReadPostfixQueues, client: &http.Client{
		Transport: transport, Timeout: api.InternalMailAPITimeout + 5*time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (b *PostfixBridge) Submit(ctx context.Context, sender, recipient string, input io.Reader) error {
	if !b.enabled() {
		return ErrDisabled
	}
	if sender != "<>" {
		return ErrInvalid
	}
	if _, err := bounceRecipientID(recipient); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(input, api.InternalMailMaxDSNBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > api.InternalMailMaxDSNBytes {
		return ErrInvalid
	}
	if !b.enabled() {
		return ErrDisabled
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://maild"+PostfixReportPath, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Content-Type", "message/rfc822")
	req.Header.Set(ReportSenderHeader, sender)
	req.Header.Set(ReportRecipientHeader, recipient)
	response, err := b.client.Do(req)
	if err != nil {
		// Do not return a URL, credential, DSN or provider diagnostic in the
		// pipe output, which Postfix may copy into another notification.
		return errors.New("internal mail report service unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("internal mail report not recorded (HTTP %d)", response.StatusCode)
	}
	return nil
}

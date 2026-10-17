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

// FeedbackClient is a private collector's delivery adapter. The caller retains
// its durable input until Submit succeeds; a lost response is safe to replay.
// It forwards original signed bytes over Unix HTTP only and follows no redirects.
type FeedbackClient struct {
	source  string
	token   string
	enabled func() bool
	client  *http.Client
}

func NewFeedbackClient(socket, source, token string, enabled func() bool) (*FeedbackClient, error) {
	if enabled == nil || !enabled() {
		return nil, ErrDisabled
	}
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || !validFeedbackSourceID(source) || !validToken(token) {
		return nil, ErrInvalid
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, DisableKeepAlives: true}
	return &FeedbackClient{source: source, token: token, enabled: enabled, client: &http.Client{
		Transport: transport, Timeout: api.InternalMailAPITimeout + 5*time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *FeedbackClient) Submit(ctx context.Context, input io.Reader) error {
	if !c.enabled() {
		return ErrDisabled
	}
	raw, err := io.ReadAll(io.LimitReader(input, api.InternalMailMaxFeedbackBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > api.InternalMailMaxFeedbackBytes {
		return ErrInvalid
	}
	if !c.enabled() {
		return ErrDisabled
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://maild"+FeedbackPath+"/"+c.source, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "message/rfc822")
	response, err := c.client.Do(req)
	if err != nil {
		return errors.New("internal mail feedback service unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("internal mail feedback not recorded (HTTP %d)", response.StatusCode)
	}
	return nil
}

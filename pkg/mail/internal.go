package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// InternalSender is an explicit opt-in for platform notifications. It speaks
// only HTTP over the maild Unix socket and never changes the default transport.
type InternalSender struct {
	token                       string
	from                        string
	route                       string
	enabled                     func() bool
	client                      *http.Client
	schedulingEnabled           func() bool
	outboxEnabled               func() bool
	recoveryGeneration          string
	verificationEnabled         func() bool
	passwordResetEnabled        func() bool
	magicLinkEnabled            func() bool
	mfaRecoveryAlertEnabled     func() bool
	mfaDisabledAlertEnabled     func() bool
	deploymentFailedEnabled     func() bool
	subscriptionEndedEnabled    func() bool
	quotaWarningEnabled         func() bool
	nonpaymentDeletionEnabled   func() bool
	accountSuspendedEnabled     func() bool
	managedPostgresCapEnabled   func() bool
	accountRestoredEnabled      func() bool
	paymentFailedEnabled        func() bool
	mfaEnabledAlertEnabled      func() bool
	passwordChangedAlertEnabled func() bool
	mfaDisableEnabled           func() bool
}

func NewInternalSender(socket, token, from string, enabled func() bool) (*InternalSender, error) {
	return newInternalSender(socket, token, from, internalmail.APIPath+"/messages", enabled)
}

// NewInternalProducerSender restricts a platform caller to its enrolled producer
// route. Selecting this transport never falls back to the full service token.
func NewInternalProducerSender(socket, producerID, token, from string, enabled func() bool) (*InternalSender, error) {
	if enabled == nil || !enabled() {
		return nil, internalmail.ErrDisabled
	}
	route, err := internalmail.ProducerMessagesRoute(producerID)
	if err != nil {
		return nil, err
	}
	return newInternalSender(socket, token, from, route, enabled)
}

func newInternalSender(socket, token, from, route string, enabled func() bool) (*InternalSender, error) {
	if enabled == nil || !enabled() {
		return nil, internalmail.ErrDisabled
	}
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(token) < api.InternalMailTokenMinBytes || strings.ContainsAny(token, " \r\n\t") || from == "" {
		return nil, errors.New("mail: internal transport needs a Unix socket, service token and From address")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &InternalSender{token: token, from: from, route: route, enabled: enabled, client: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (s *InternalSender) Send(ctx context.Context, msg Message) error {
	return s.send(ctx, msg, internalmail.MessageTiming{})
}

func (s *InternalSender) send(ctx context.Context, msg Message, timing internalmail.MessageTiming) error {
	template, templateEnabled := s.credentialOutboxGate()
	if templateEnabled != nil && (!templateEnabled() || s.recoveryGeneration == "") {
		return internalmail.ErrDisabled
	}
	if s.recoveryGeneration != "" && (s.outboxEnabled == nil || !s.outboxEnabled()) {
		return internalmail.ErrDisabled
	}
	if !s.enabled() {
		return internalmail.ErrDisabled
	}
	key := msg.MessageID
	stableKey := key != ""
	if !stableKey {
		key = uuid.NewString()
	}
	body, err := json.Marshal(internalmail.SendRequest{From: s.from, To: msg.To, Subject: msg.Subject, Text: msg.TextBody, HTML: msg.HTMLBody, Headers: msg.Headers, MessageTiming: timing})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://maild"+s.route, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if s.recoveryGeneration != "" {
		req.Header.Set(internalmail.ProducerOutboxGenerationHeader, s.recoveryGeneration)
	}
	if templateEnabled != nil {
		req.Header.Set(internalmail.ProducerOutboxTemplateHeader, template)
		if !templateEnabled() {
			return internalmail.ErrDisabled
		}
	}
	response, err := s.client.Do(req)
	if err != nil {
		// Without a caller-stable ID, a decorator retry could enqueue a second
		// copy after a lost HTTP response. Keep ambiguous failures permanent.
		if stableKey {
			return &TransientError{Err: errors.New("internal mail submission unavailable")}
		}
		return errors.New("mail: internal submission outcome unknown")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode == http.StatusAccepted {
		return nil
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 || s.recoveryGeneration != "" && response.StatusCode == http.StatusNotFound {
		return &TransientError{Status: response.StatusCode}
	}
	return fmt.Errorf("mail: internal service rejected submission (HTTP %d)", response.StatusCode)
}

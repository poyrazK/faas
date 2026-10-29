package internalmail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type Service struct {
	Store                             Store
	Codec                             Codec
	Resolver                          TXTResolver
	SendingIP                         netip.Addr
	Enabled                           func() bool
	DeliveryEnabled                   func() bool
	ReportsEnabled                    func() bool
	ReportToken                       string
	FeedbackEnabled                   func() bool
	FeedbackSources                   map[string]FeedbackSource
	RetentionEnabled                  func() bool
	RetentionPolicy                   RetentionPolicy
	OperationsEnabled                 func() bool
	OperationsToken                   string
	RotationEnabled                   func() bool
	ReverificationEnabled             func() bool
	DeliveryControlsEnabled           func() bool
	ProducersEnabled                  func() bool
	Producers                         map[string]Producer
	SchedulingEnabled                 func() bool
	PacingEnabled                     func() bool
	PacingPolicy                      PacingPolicy
	BackpressureEnabled               func() bool
	ReputationEnabled                 func() bool
	EventFeedEnabled                  func() bool
	ProducerSchedulingEnabled         func() bool
	ProducerLifecycleEnabled          func() bool
	EncryptionRotationEnabled         func() bool
	SuppressionControlsEnabled        func() bool
	RecoveryEnabled                   func() bool
	ProducerOutboxEnabled             func() bool
	OutboxReconciliationEnabled       func() bool
	VerificationOutboxEnabled         func() bool
	PasswordResetOutboxEnabled        func() bool
	MagicLinkOutboxEnabled            func() bool
	MFARecoveryAlertOutboxEnabled     func() bool
	MFADisabledAlertOutboxEnabled     func() bool
	DeploymentFailedOutboxEnabled     func() bool
	SubscriptionEndedOutboxEnabled    func() bool
	QuotaWarningOutboxEnabled         func() bool
	NonpaymentDeletionOutboxEnabled   func() bool
	AccountSuspendedOutboxEnabled     func() bool
	ManagedPostgresCapOutboxEnabled   func() bool
	AccountRestoredOutboxEnabled      func() bool
	PaymentFailedOutboxEnabled        func() bool
	MFAEnabledAlertOutboxEnabled      func() bool
	PasswordChangedAlertOutboxEnabled func() bool
	MFADisableOutboxEnabled           func() bool
	OnDomainCheck                     func(error)
	OnRetention                       func(RetentionResult, error)
	RecipientAllowed                  func(string) bool
	Transport                         Transport
	Now                               func() time.Time
	Ready                             func(context.Context) error
	Metrics                           http.Handler
	Monitoring                        http.Handler
	OnTick                            func()
}

func (s *Service) enabled() bool { return s != nil && s.Enabled != nil && s.Enabled() }
func (s *Service) deliveryEnabled() bool {
	return s.enabled() && s.DeliveryEnabled != nil && s.DeliveryEnabled()
}
func (s *Service) reportsEnabled() bool {
	return s.enabled() && s.ReportsEnabled != nil && s.ReportsEnabled()
}
func (s *Service) feedbackEnabled() bool {
	return s.enabled() && s.FeedbackEnabled != nil && s.FeedbackEnabled()
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) RegisterDomain(ctx context.Context, name string) (DomainResponse, error) {
	if !s.enabled() {
		return DomainResponse{}, ErrDisabled
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if !validDomain(name) {
		return DomainResponse{}, ErrInvalid
	}
	if d, err := s.Store.DomainByName(ctx, name); err == nil {
		return s.domainResponse(d), nil
	} else if !errors.Is(err, ErrNotFound) {
		return DomainResponse{}, err
	}
	d, err := s.newDomain(name)
	if err != nil {
		return DomainResponse{}, err
	}
	if !s.enabled() {
		return DomainResponse{}, ErrDisabled
	}
	d, err = s.Store.CreateDomain(ctx, d)
	if err != nil {
		return DomainResponse{}, err
	}
	return s.domainResponse(d), nil
}

func (s *Service) newDomain(name string) (Domain, error) {
	id := uuid.NewString()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Domain{}, err
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return Domain{}, err
	}
	private := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	sealed, err := s.Codec.Seal("domain:"+id, private)
	if err != nil {
		return Domain{}, err
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Domain{}, err
	}
	return Domain{ID: id, Name: name, Status: "pending", Selector: "gregale" + strings.ReplaceAll(id[:8], "-", ""), VerificationToken: hex.EncodeToString(token[:]), PublicKey: base64.StdEncoding.EncodeToString(public), SealedKey: sealed}, nil
}

func (s *Service) domainResponse(d Domain) DomainResponse {
	ipMechanism := "ip4:"
	if s.SendingIP.Is6() {
		ipMechanism = "ip6:"
	}
	return DomainResponse{Domain: d, Records: []DNSRecord{
		{Name: "_gregale-mail." + d.Name, Type: "TXT", Value: "gregale-mail-verification=" + d.VerificationToken},
		{Name: d.Selector + "._domainkey." + d.Name, Type: "TXT", Value: "v=DKIM1; k=rsa; p=" + d.PublicKey},
		{Name: d.Name, Type: "TXT", Value: "v=spf1 " + ipMechanism + s.SendingIP.String() + " -all"},
		{Name: "_dmarc." + d.Name, Type: "TXT", Value: "v=DMARC1; p=none"},
	}}
}

func (s *Service) VerifyDomain(ctx context.Context, id string) (DomainResponse, error) {
	if !s.enabled() {
		return DomainResponse{}, ErrDisabled
	}
	d, err := s.Store.Domain(ctx, id)
	if err != nil {
		return DomainResponse{}, err
	}
	if d.Status == "disabled" {
		return DomainResponse{}, ErrConflict
	}
	response := s.domainResponse(d)
	if err := s.verifyCurrentDomain(ctx, d, response); err != nil {
		return DomainResponse{}, err
	}
	if !s.enabled() {
		return DomainResponse{}, ErrDisabled
	}
	if err := s.confirmVerifiedDomain(ctx, d); err != nil {
		return DomainResponse{}, err
	}
	d, err = s.Store.Domain(ctx, id)
	if err != nil {
		return DomainResponse{}, err
	}
	return s.domainResponse(d), nil
}

func (s *Service) verifyRecords(ctx context.Context, d DomainResponse) error {
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailDNSVerifyTimeout)
	defer cancel()
	for i, record := range d.Records {
		values, err := s.Resolver.LookupTXT(ctx, record.Name)
		if err != nil {
			return ErrUnverified
		}
		if !s.recordMatches(d.Domain, record, i, values) {
			return ErrUnverified
		}
	}
	return nil
}

func (s *Service) recordMatches(d Domain, record DNSRecord, index int, values []string) bool {
	if index == 1 && len(values) != 1 {
		return false
	}
	if index == 3 {
		count := 0
		for _, value := range values {
			if strings.HasPrefix(value, "v=DMARC1") {
				count++
			}
		}
		if count != 1 {
			return false
		}
	}
	for _, value := range values {
		fields := dnsTags(value)
		if index == 1 || index == 3 {
			if !uniqueDNSTags(value) {
				continue
			}
		}
		switch index {
		case 0:
			if value == record.Value {
				return true
			}
		case 1:
			if fields["v"] == "DKIM1" && fields["k"] == "rsa" && strings.Join(strings.Fields(fields["p"]), "") == d.PublicKey {
				return true
			}
		case 2:
			return validSPF(values, s.SendingIP)
		case 3:
			policy := fields["p"]
			if fields["v"] == "DMARC1" && (policy == "none" || policy == "quarantine" || policy == "reject") {
				return true
			}
		}
	}
	return false
}

func uniqueDNSTags(value string) bool {
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ";") {
		key, _, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func dnsTags(value string) map[string]string {
	result := map[string]string{}
	for _, part := range strings.Split(value, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			result[k] = strings.TrimSpace(v)
		}
	}
	return result
}

// The internal pilot uses one explicit sending IP. SPF include/redirect policy
// evaluation is intentionally not guessed: publish the direct IP mechanism.
func validSPF(values []string, ip netip.Addr) bool {
	count := 0
	valid := false
	mechanism := "ip4:"
	if ip.Is6() {
		mechanism = "ip6:"
	}
	for _, value := range values {
		fields := strings.Fields(value)
		if len(fields) == 0 || fields[0] != "v=spf1" {
			continue
		}
		count++
		for _, field := range fields[1:] {
			if field == "all" || field == "+all" || field == "-all" || field == "~all" || field == "?all" {
				break
			}
			if field == mechanism+ip.String() || field == "+"+mechanism+ip.String() {
				valid = true
				break
			}
		}
	}
	return count == 1 && valid
}

func (s *Service) Submit(ctx context.Context, key string, request SendRequest) (MessageResponse, error) {
	return s.submitMessage(ctx, key, request, nil)
}

func (s *Service) submitMessage(ctx context.Context, key string, request SendRequest, producer *Producer) (MessageResponse, error) {
	if !s.enabled() {
		return MessageResponse{}, ErrDisabled
	}
	if err := s.checkRecovery(ctx); err != nil {
		return MessageResponse{}, err
	}
	if !request.IsZero() && !s.schedulingEnabled() {
		return MessageResponse{}, ErrDisabled
	}
	request, domain, err := normalizeRequest(request)
	if err != nil {
		return MessageResponse{}, err
	}
	var producerID, senderAddress string
	if producer != nil {
		address, err := parseAddress(request.From)
		if err != nil || !producer.allows(address.Address) {
			return MessageResponse{}, ErrProducerSender
		}
		producerID, senderAddress = producer.ID, address.Address
	}
	for _, address := range request.To {
		if s.RecipientAllowed == nil || !s.RecipientAllowed(address) {
			return MessageResponse{}, ErrNotAllowed
		}
	}
	if len(key) == 0 || len(key) > api.InternalMailMaxIdempotencyBytes || strings.ContainsAny(key, "\r\n\x00") {
		return MessageResponse{}, ErrInvalid
	}
	d, err := s.Store.DomainByName(ctx, domain)
	if err != nil {
		return MessageResponse{}, err
	}
	id := uuid.NewString()
	raw, err := json.Marshal(request)
	if err != nil {
		return MessageResponse{}, err
	}
	fingerprint, err := s.Codec.Fingerprint("message:"+id, raw)
	if err != nil {
		return MessageResponse{}, err
	}
	sealed, err := s.Codec.Seal("message:"+id, raw)
	if err != nil {
		return MessageResponse{}, err
	}
	recipients := make([]Recipient, 0, len(request.To))
	for _, to := range request.To {
		recipients = append(recipients, Recipient{ID: uuid.NewString(), MessageID: id, Address: to})
	}
	if !s.enabled() {
		return MessageResponse{}, ErrDisabled
	}
	m, created, err := s.enqueueMail(ctx, Message{ID: id, DomainID: d.ID, ProducerID: producerID, SenderAddress: senderAddress, IdempotencyKey: key, SealedPayload: sealed, PayloadFingerprint: fingerprint, MessageTiming: request.MessageTiming}, recipients)
	if err != nil {
		return MessageResponse{}, err
	}
	if !created {
		if err := s.checkReplay(m, raw); err != nil {
			return MessageResponse{}, err
		}
	}
	return s.Store.Message(ctx, m.ID)
}

func (s *Service) checkReplay(m Message, raw []byte) error {
	if len(m.PayloadFingerprint) != 0 {
		return verifyFingerprint(s.Codec, "message:"+m.ID, raw, m.PayloadFingerprint)
	}
	// Legacy messages remain replayable before the retention pass atomically
	// backfills their verifier and removes the encrypted payload.
	original, err := s.Codec.Open("message:"+m.ID, m.SealedPayload)
	if err != nil {
		return err
	}
	defer clear(original)
	if !bytes.Equal(original, raw) {
		return ErrConflict
	}
	return nil
}

func validateReport(r Report) error {
	if _, err := uuid.Parse(r.RecipientID); err != nil {
		return ErrInvalid
	}
	if len(r.EventID) == 0 || len(r.EventID) > api.InternalMailMaxIdempotencyBytes || strings.ContainsAny(r.EventID, "\r\n\x00") || len(r.Code) > api.InternalMailMaxDiagnosticBytes || strings.ContainsAny(r.Code, "\r\n\x00") {
		return ErrInvalid
	}
	switch r.Kind {
	case "accepted", "bounced", "complained", "deferred":
		return nil
	case "retry":
		if r.Code == "confirmed_not_accepted" {
			return nil
		}
	}
	return ErrInvalid
}

// DeliverOne owns one bounded attempt. A disabled flag means no claim, no
// message decryption and no SMTP connection. Re-check immediately before DATA.
func (s *Service) DeliverOne(ctx context.Context) error {
	if !s.enabled() {
		return nil
	}
	if err := s.checkRecovery(ctx); err != nil {
		if errors.Is(err, ErrRecoveryHeld) {
			return nil
		}
		return err
	}
	if !s.deliveryEnabled() {
		if s.reputationEnabled() {
			store, ok := s.Store.(reputationStore)
			if !ok {
				return ErrInvalid
			}
			if err := store.refreshReputation(ctx); err != nil {
				return err
			}
		}
		if s.backpressureEnabled() {
			store, ok := s.Store.(backpressureStore)
			if !ok {
				return ErrInvalid
			}
			if _, err := store.refreshRelay(ctx); err != nil {
				return err
			}
		}
		if s.schedulingEnabled() || s.backpressureEnabled() || s.reputationEnabled() {
			store, ok := s.Store.(timingStore)
			if !ok {
				return ErrInvalid
			}
			return store.expireTimed(ctx)
		}
		return nil
	}
	if s.reputationEnabled() && !s.feedbackEnabled() {
		return ErrFeedbackUnavailable
	}
	if s.suppressionControlsEnabled() {
		if _, ok := s.Store.(suppressionSafetyStore); !ok {
			return ErrInvalid
		}
	}
	if codec, ok := s.Codec.(*KeyringCodec); ok {
		store, supported := s.Store.(encryptionStore)
		if !supported {
			return ErrInvalid
		}
		if err := store.refreshEncryptionSelection(ctx, codec); err != nil {
			return err
		}
	}
	r, err := s.claimMail(ctx)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	state, code := s.prepareAndSubmit(ctx, r)
	next := s.now()
	if state == "deferred" {
		next = next.Add(retryDelay(r.Attempts))
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.settleMail(settleCtx, r, state, code, next)
}

func (s *Service) prepareAndSubmit(ctx context.Context, r Recipient) (string, string) {
	if !s.deliveryEnabled() || ctx.Err() != nil {
		return "queued", "delivery_disabled"
	}
	allowed, controlErr := s.deliveryWorkPermitted(ctx, r)
	if controlErr != nil {
		return "deferred", "delivery_controls_unavailable"
	}
	if !allowed {
		return "queued", "delivery_paused"
	}
	if s.RecipientAllowed == nil || !s.RecipientAllowed(r.Address) {
		return "failed", "recipient_not_allowed"
	}
	suppressed, err := s.recipientSuppressed(ctx, r)
	if err != nil {
		return "deferred", "suppression_unavailable"
	}
	if suppressed {
		return "suppressed", "recipient_suppressed"
	}
	m, err := s.Store.Message(ctx, r.MessageID)
	if err != nil {
		return "deferred", "message_unavailable"
	}
	if err := s.checkMessageTiming(m.Message); err != nil {
		return timingSettlement(err)
	}
	if !s.producerMessagePermitted(m.Message) {
		return "queued", "producer_unavailable"
	}
	if m.ProducerID != "" {
		if store, ok := s.Store.(producerLifecycleStore); ok {
			if err := store.checkProducerAuthentication(ctx, producerAuthentication{ProducerID: m.ProducerID}); err != nil {
				if errors.Is(err, ErrProducerSuspended) {
					return "queued", producerSuspendedCode
				}
				return "deferred", "producer_access_unavailable"
			}
		} else if s.producerLifecycleEnabled() {
			return "deferred", "producer_access_unavailable"
		}
	}
	d, err := s.Store.Domain(ctx, m.DomainID)
	if err != nil {
		return "deferred", "domain_unavailable"
	}
	if d.Status != "verified" {
		return "failed", "domain_disabled"
	}
	if !authorizedDomain(d, s.authorizationRequirement(), s.now()) {
		return "deferred", "domain_authorization_pending"
	}
	data, err := s.render(m.Message, r, d)
	if err != nil {
		if errors.Is(err, ErrEncryptionKeyUnavailable) {
			return "queued", "encryption_key_unavailable"
		}
		return "failed", "render_failed"
	}
	if !s.deliveryEnabled() || ctx.Err() != nil {
		return "queued", "delivery_disabled"
	}
	if err := s.checkMessageTiming(m.Message); err != nil {
		return timingSettlement(err)
	}
	if !s.producerMessagePermitted(m.Message) {
		return "queued", "producer_unavailable"
	}
	submissionCtx, err := s.paymentFailedSubmissionContext(ctx, m.Message)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return "queued", "producer_unavailable"
		}
		return "deferred", "submission_not_started"
	}
	submissionCtx, err = s.deploymentFailedSubmissionContext(submissionCtx, m.Message)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return "queued", "producer_unavailable"
		}
		return "deferred", "submission_not_started"
	}
	submissionCtx, err = s.subscriptionEndedSubmissionContext(submissionCtx, m.Message)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return "queued", "producer_unavailable"
		}
		return "deferred", "submission_not_started"
	}
	submissionCtx, err = s.quotaWarningSubmissionContext(submissionCtx, m.Message)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return "queued", "producer_unavailable"
		}
		return "deferred", "submission_not_started"
	}
	submissionCtx, err = s.nonpaymentDeletionSubmissionContext(submissionCtx, m.Message)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return "queued", "producer_unavailable"
		}
		return "deferred", "submission_not_started"
	}
	submissionCtx, err = s.accountSuspendedSubmissionContext(submissionCtx, m.Message)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return "queued", "producer_unavailable"
		}
		return "deferred", "submission_not_started"
	}
	submissionCtx, err = s.managedPostgresCapSubmissionContext(submissionCtx, m.Message)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return "queued", "producer_unavailable"
		}
		return "deferred", "submission_not_started"
	}
	submissionCtx, err = s.accountRestoredSubmissionContext(submissionCtx, m.Message)
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			return "queued", "producer_unavailable"
		}
		return "deferred", "submission_not_started"
	}
	if err := s.beginMailSubmission(submissionCtx, r, d.Selector); err != nil {
		if errors.Is(err, errDeploymentFailedObsolete) {
			return "cancelled", "account_changed"
		}
		if errors.Is(err, errSubscriptionEndedObsolete) {
			return "cancelled", "account_changed"
		}
		if errors.Is(err, errQuotaWarningObsolete) {
			return "cancelled", "account_changed"
		}
		if errors.Is(err, errNonpaymentDeletionObsolete) {
			return "cancelled", "account_changed"
		}
		if errors.Is(err, errAccountSuspensionObsolete) {
			return "cancelled", "account_changed"
		}
		if errors.Is(err, errManagedPostgresCapObsolete) {
			return "cancelled", "account_changed"
		}
		if errors.Is(err, errAccountRestorationObsolete) {
			return "cancelled", "account_changed"
		}
		if errors.Is(err, errPaymentFailureObsolete) {
			return "cancelled", "account_changed"
		}

		if errors.Is(err, ErrRecoveryHeld) {
			return "queued", "recovery_held"
		}
		if errors.Is(err, ErrRecipientSuppressed) {
			return "suppressed", "recipient_suppressed"
		}
		if errors.Is(err, ErrProducerSuspended) {
			return "queued", producerSuspendedCode
		}
		if errors.Is(err, ErrProducerThrottled) {
			return "queued", producerThrottleCode
		}
		if errors.Is(err, ErrReputationHeld) {
			return "queued", "reputation_held"
		}
		if errors.Is(err, ErrRelayHeld) {
			return "queued", "relay_backpressure"
		}
		if errors.Is(err, ErrDomainPaced) {
			return "queued", pacingWaitCode
		}
		if errors.Is(err, ErrDeliveryPaused) {
			return "queued", "delivery_paused"
		}
		if errors.Is(err, ErrMessageExpired) || errors.Is(err, ErrMessageNotDue) || errors.Is(err, ErrDisabled) {
			return timingSettlement(err)
		}
		return "deferred", "submission_not_started"
	}
	if !s.deliveryEnabled() || ctx.Err() != nil {
		return "queued", "delivery_disabled"
	}
	if err := s.checkMessageTiming(m.Message); err != nil {
		return timingSettlement(err)
	}
	if !s.producerMessagePermitted(m.Message) {
		return "queued", "producer_unavailable"
	}
	submitCtx, cancel := context.WithTimeout(ctx, api.InternalMailSMTPTimeout)
	defer cancel()
	expires := m.submissionDeadline()
	if expires != nil {
		var expireCancel context.CancelFunc
		submitCtx, expireCancel = context.WithDeadline(submitCtx, *expires)
		defer expireCancel()
	}
	return s.Transport.Submit(submitCtx, Envelope{From: "bounce+" + r.ID + "@" + d.Name, To: r.Address, Data: data, RecipientID: r.ID, ExpiresAt: expires})
}

func retryDelay(attempt int32) time.Duration {
	delay := api.InternalMailRetryBase
	for i := int32(1); i < attempt && delay < api.InternalMailRetryMax; i++ {
		delay *= 2
	}
	if delay > api.InternalMailRetryMax {
		return api.InternalMailRetryMax
	}
	return delay
}

func (s *Service) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(api.InternalMailPollInterval)
	defer ticker.Stop()
	for {
		passCtx, cancel := context.WithTimeout(ctx, api.InternalMailWorkerPassTimeout)
		err := s.DeliverOne(passCtx)
		cancel()
		if s.OnTick != nil {
			s.OnTick()
		}
		if err != nil && ctx.Err() == nil && onError != nil {
			onError(fmt.Errorf("internal mail delivery: %w", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

package internalmail

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrKeyPropagation = errors.New("internal mail DKIM propagation wait required")
	ErrKeyOverlap     = errors.New("internal mail DKIM selector still required")
)

type domainAuthorization struct {
	RequireFresh bool
	SendingIP    string
}

func (s *Service) rotationEnabled() bool {
	return s.enabled() && s.RotationEnabled != nil && s.RotationEnabled()
}
func (s *Service) reverificationEnabled() bool {
	return s.enabled() && s.ReverificationEnabled != nil && s.ReverificationEnabled()
}
func (s *Service) authorizationRequirement() domainAuthorization {
	return domainAuthorization{s.reverificationEnabled(), s.SendingIP.String()}
}

func authorizedDomain(d Domain, a domainAuthorization, now time.Time) bool {
	if d.AuthorizationState == "paused" {
		return false
	}
	return !a.RequireFresh || d.LastVerifiedAt != nil && d.LastVerifiedAt.After(now.Add(-api.InternalMailDomainCheckMaxAge)) && d.VerifiedSendingIP == a.SendingIP
}

type domainSenderStore interface {
	enqueueAuthorized(context.Context, Message, []Recipient, domainAuthorization) (Message, bool, error)
	claimAuthorized(context.Context, domainAuthorization) (Recipient, error)
	beginSignedSubmission(context.Context, Recipient, string, domainAuthorization) error
	confirmDomain(context.Context, Domain, bool, string, func() bool) error
}

type DKIMKey struct {
	ID              string     `json:"id"`
	DomainID        string     `json:"domain_id"`
	Selector        string     `json:"selector"`
	State           string     `json:"state"`
	CheckResult     string     `json:"check_result"`
	CreatedAt       time.Time  `json:"created_at"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	FirstVerifiedAt *time.Time `json:"first_verified_at,omitempty"`
	ActivatedAt     *time.Time `json:"activated_at,omitempty"`
	DeactivatedAt   *time.Time `json:"deactivated_at,omitempty"`
	ActivateAfter   *time.Time `json:"activate_after,omitempty"`
	RetireAfter     *time.Time `json:"retire_after,omitempty"`
	Record          DNSRecord  `json:"record"`
	PublicKey       string     `json:"-"`
}

type domainLifecycleStore interface {
	operatorDKIMKeys(context.Context, string, OperatorList) (OperatorPage[DKIMKey], error)
	stageDKIM(context.Context, string, OperatorAuditInput, func() (DKIMKey, []byte, error), func() bool) (OperationRecord, error)
	verifyDKIM(context.Context, string, string, OperatorAuditInput, func(context.Context, DKIMKey) (string, error), func() bool) (OperationRecord, error)
	activateDKIM(context.Context, string, string, OperatorAuditInput, func(context.Context, Domain, []DKIMKey) (string, error), string, func() bool) (OperationRecord, error)
	retireDKIM(context.Context, string, string, OperatorAuditInput, func() bool) (OperationRecord, error)
	domainCheckCandidates(context.Context, string) ([]Domain, error)
	requiredDKIMRecords(context.Context, string) ([]DKIMKey, error)
	recordDomainObservation(context.Context, Domain, string, string, func() bool) (Domain, error)
	operatorReverify(context.Context, string, OperatorAuditInput, func(context.Context, Domain, []DKIMKey) (string, error), string, func() bool) (OperationRecord, error)
}

func (s *Service) lifecycleStore(rotation, operator bool) (domainLifecycleStore, error) {
	if rotation && !s.rotationEnabled() || !rotation && !s.reverificationEnabled() || operator && !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(domainLifecycleStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}

// A negative answer is distinct from resolver failure. Only real record
// mismatches and NXDOMAIN count toward confirmed authorization failure.
func (s *Service) observeRecords(parent context.Context, d Domain, records []DNSRecord, indices []int) (outcome string, err error) {
	ctx, cancel := context.WithTimeout(parent, api.InternalMailDNSVerifyTimeout)
	defer cancel()
	defer func() {
		if err != nil && parent.Err() == nil && ctx.Err() != nil {
			outcome, err = "unavailable", nil
		}
	}()
	result := "valid"
	for i, record := range records {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if s.Resolver == nil {
			return "unavailable", nil
		}
		values, err := s.Resolver.LookupTXT(ctx, record.Name)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			var dns *net.DNSError
			if errors.As(err, &dns) && dns.IsNotFound {
				result = "mismatch"
			} else if result != "mismatch" {
				result = "unavailable"
			}
			continue
		}
		checkDomain := d
		if indices[i] == 1 {
			checkDomain.PublicKey = dkimPublicValue(record.Value)
		}
		if !s.recordMatches(checkDomain, record, indices[i], values) {
			result = "mismatch"
		}
	}
	return result, nil
}

func (s *Service) enqueueMail(ctx context.Context, m Message, recipients []Recipient) (Message, bool, error) {
	if !m.IsZero() {
		return s.enqueueWithTiming(ctx, m, recipients)
	}
	if m.ProducerID != "" {
		p, err := s.producer(m.ProducerID)
		if err != nil {
			return Message{}, false, err
		}
		if !p.allows(m.SenderAddress) {
			return Message{}, false, ErrProducerSender
		}
		store, ok := s.Store.(producerStore)
		if !ok {
			return Message{}, false, ErrInvalid
		}
		return store.enqueueProduced(ctx, m, recipients, s.authorizationRequirement(), p, s.producersEnabled)
	}
	if store, ok := s.Store.(domainSenderStore); ok {
		return store.enqueueAuthorized(ctx, m, recipients, s.authorizationRequirement())
	}
	if s.reverificationEnabled() {
		return Message{}, false, ErrInvalid
	}
	return s.Store.Enqueue(ctx, m, recipients)
}
func (s *Service) claimMail(ctx context.Context) (Recipient, error) {
	if err := s.producerLifecycleSupported(); err != nil {
		return Recipient{}, err
	}
	policy, err := s.pacingPolicy()
	if err != nil {
		return Recipient{}, err
	}
	dispatch, err := s.dispatchPolicy()
	if err != nil {
		return Recipient{}, err
	}
	if dispatch.Enabled {
		store, ok := s.Store.(producerSchedulerStore)
		if !ok {
			return Recipient{}, ErrInvalid
		}
		return store.claimScheduled(ctx, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled(), policy, s.backpressureEnabled(), s.reputationEnabled(), dispatch)
	}
	if store, ok := s.Store.(reputationStore); ok {
		return store.claimProtected(ctx, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled(), policy, s.backpressureEnabled(), s.reputationEnabled())
	}
	if s.reputationEnabled() {
		return Recipient{}, ErrInvalid
	}
	if store, ok := s.Store.(backpressureStore); ok {
		return store.claimWithRelay(ctx, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled(), policy, s.backpressureEnabled())
	}
	if s.backpressureEnabled() {
		return Recipient{}, ErrInvalid
	}
	if store, ok := s.Store.(pacingStore); ok {
		return store.claimPaced(ctx, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled(), policy)
	}
	if s.pacingEnabled() {
		return Recipient{}, ErrInvalid
	}
	if store, ok := s.Store.(timingStore); ok {
		return store.claimTimed(ctx, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled())
	}
	if s.schedulingEnabled() {
		return Recipient{}, ErrInvalid
	}
	if store, ok := s.Store.(producerStore); ok {
		return store.claimWithProducers(ctx, s.authorizationRequirement(), s.allowedProducerSenders())
	}
	if s.producersEnabled() {
		return Recipient{}, ErrInvalid
	}
	if store, ok := s.Store.(domainSenderStore); ok {
		return store.claimAuthorized(ctx, s.authorizationRequirement())
	}
	if s.reverificationEnabled() {
		return Recipient{}, ErrInvalid
	}
	return s.Store.Claim(ctx)
}
func (s *Service) beginMailSubmission(ctx context.Context, r Recipient, selector string) error {
	if err := s.producerLifecycleSupported(); err != nil {
		return err
	}
	policy, err := s.pacingPolicy()
	if err != nil {
		return err
	}
	dispatch, err := s.dispatchPolicy()
	if err != nil {
		return err
	}
	if dispatch.Enabled {
		store, ok := s.Store.(producerSchedulerStore)
		if !ok {
			return ErrInvalid
		}
		return store.beginScheduled(ctx, r, selector, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled(), policy, s.backpressureEnabled(), s.reputationEnabled(), dispatch)
	}
	if store, ok := s.Store.(reputationStore); ok {
		return store.beginProtected(ctx, r, selector, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled(), policy, s.backpressureEnabled(), s.reputationEnabled())
	}
	if s.reputationEnabled() {
		return ErrInvalid
	}
	if store, ok := s.Store.(backpressureStore); ok {
		return store.beginWithRelay(ctx, r, selector, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled(), policy, s.backpressureEnabled())
	}
	if s.backpressureEnabled() {
		return ErrInvalid
	}
	if store, ok := s.Store.(pacingStore); ok {
		return store.beginPacedSubmission(ctx, r, selector, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled(), policy)
	}
	if s.pacingEnabled() {
		return ErrInvalid
	}
	if store, ok := s.Store.(timingStore); ok {
		return store.beginTimedSubmission(ctx, r, selector, s.authorizationRequirement(), s.allowedProducerSenders(), s.schedulingEnabled())
	}
	if s.schedulingEnabled() {
		return ErrInvalid
	}
	if store, ok := s.Store.(producerStore); ok {
		return store.beginProducerSubmission(ctx, r, selector, s.authorizationRequirement(), s.allowedProducerSenders())
	}
	if s.producersEnabled() {
		return ErrInvalid
	}
	if store, ok := s.Store.(domainSenderStore); ok {
		return store.beginSignedSubmission(ctx, r, selector, s.authorizationRequirement())
	}
	if s.reverificationEnabled() || s.rotationEnabled() {
		return ErrInvalid
	}
	return s.Store.BeginSubmission(ctx, r)
}
func (s *Service) confirmVerifiedDomain(ctx context.Context, d Domain) error {
	if store, ok := s.Store.(domainSenderStore); ok {
		return store.confirmDomain(ctx, d, s.reverificationEnabled(), s.SendingIP.String(), s.enabled)
	}
	if s.reverificationEnabled() || s.rotationEnabled() {
		return ErrInvalid
	}
	return s.Store.SetDomainStatus(ctx, d.ID, "verified")
}

func (s *Service) verifyCurrentDomain(ctx context.Context, d Domain, response DomainResponse) error {
	if !s.reverificationEnabled() {
		return s.verifyRecords(ctx, response)
	}
	store, ok := s.Store.(domainLifecycleStore)
	if !ok {
		return ErrInvalid
	}
	keys, err := store.requiredDKIMRecords(ctx, d.ID)
	if err != nil {
		return err
	}
	outcome, err := s.observeDomain(ctx, d, keys)
	if err != nil {
		return err
	}
	if outcome != "valid" {
		return ErrUnverified
	}
	return nil
}

func dkimPublicValue(value string) string { return dnsTags(value)["p"] }

func (s *Service) observeDomain(ctx context.Context, d Domain, keys []DKIMKey) (string, error) {
	records := s.domainResponse(d).Records
	indices := []int{0, 1, 2, 3}
	for _, key := range keys {
		records = append(records, key.Record)
		indices = append(indices, 1)
	}
	return s.observeRecords(ctx, d, records, indices)
}

func (s *Service) ReverifyDomains(ctx context.Context) (err error) {
	store, err := s.lifecycleStore(false, false)
	if err != nil {
		return err
	}
	defer func() {
		if s.OnDomainCheck != nil {
			s.OnDomainCheck(err)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailDomainCheckPassTimeout)
	defer cancel()
	domains, err := store.domainCheckCandidates(ctx, s.SendingIP.String())
	if err != nil {
		return err
	}
	for _, d := range domains {
		if !s.reverificationEnabled() {
			return ErrDisabled
		}
		keys, err := store.requiredDKIMRecords(ctx, d.ID)
		if err != nil {
			return err
		}
		outcome, err := s.observeDomain(ctx, d, keys)
		if err != nil {
			return err
		}
		if _, err := store.recordDomainObservation(ctx, d, outcome, s.SendingIP.String(), s.reverificationEnabled); err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}

func (s *Service) RunDomainChecks(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(api.InternalMailDomainCheckPoll)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.ReverifyDomains(ctx); err != nil && !errors.Is(err, ErrDisabled) && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

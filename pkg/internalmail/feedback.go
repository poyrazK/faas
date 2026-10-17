package internalmail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/onebox-faas/faas/pkg/api"
)

const FeedbackPath = APIPath + "/feedback"

var (
	ErrFeedbackUntrusted   = errors.New("internal mail feedback is not authenticated")
	ErrFeedbackUnavailable = errors.New("internal mail feedback verification unavailable")
)

// IngestFeedback admits only an enrolled source's DKIM-authenticated report.
// This private path cannot send mail, attest acceptance or request retries. A
// receiving adapter must preserve the original MIME bytes, not forward/re-sign
// or trust caller-supplied Authentication-Results headers.
func (s *Service) IngestFeedback(ctx context.Context, sourceID string, raw []byte) error {
	if !s.feedbackEnabled() {
		return ErrDisabled
	}
	source, ok := s.FeedbackSources[sourceID]
	if !ok {
		return ErrFeedbackUntrusted
	}
	parsed, err := parseFeedback(raw)
	if err != nil {
		return err
	}
	_, recipientDomain, _ := strings.Cut(parsed.report.RecipientAddress, "@")
	if !slices.Contains(source.Formats, parsed.format) || !slices.Contains(source.RecipientDomains, recipientDomain) {
		return ErrFeedbackUntrusted
	}
	if err := s.authenticateFeedback(ctx, source, parsed.from, raw); err != nil {
		return err
	}
	if !s.feedbackEnabled() {
		return ErrDisabled
	}
	// Message-ID is required to be signed. Re-delivery may add Received or
	// other unsigned headers; those changes cannot create another event.
	// Contradictory reuse of a provider identity is rejected by Store.Report.
	digest := sha256.Sum256([]byte(source.ID + "\x00" + parsed.identity))
	parsed.report.EventID = "feedback:" + source.ID + ":" + hex.EncodeToString(digest[:])
	parsed.report.suppressionSource = suppressionSource{kind: "provider", id: source.ID}
	if s.reputationEnabled() {
		store, ok := s.Store.(reputationStore)
		if !ok {
			return ErrInvalid
		}
		return store.reportFeedback(ctx, parsed.report, func() bool { return s.feedbackEnabled() && s.reputationEnabled() })
	}
	return s.Store.Report(ctx, parsed.report)
}

func (s *Service) authenticateFeedback(ctx context.Context, source FeedbackSource, from string, raw []byte) error {
	if !slices.Contains(source.ReportFrom, from) {
		return ErrFeedbackUntrusted
	}
	if s.Resolver == nil {
		return ErrFeedbackUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailDNSVerifyTimeout)
	defer cancel()
	var unavailable atomic.Bool
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{
		MaxVerifications: api.InternalMailMaxFeedbackSignatures,
		LookupTXT: func(name string) ([]string, error) {
			selector, domain, ok := strings.Cut(strings.ToLower(name), "._domainkey.")
			if !ok || !validDomain("s."+selector) || !slices.Contains(source.DKIMDomains, domain) {
				return nil, ErrFeedbackUntrusted
			}
			records, err := s.Resolver.LookupTXT(ctx, name)
			var networkError net.Error
			var dnsError *net.DNSError
			if ctx.Err() != nil || errors.As(err, &networkError) && networkError.Timeout() || errors.As(err, &dnsError) && dnsError.IsTemporary {
				unavailable.Store(true)
			}
			return records, err
		},
	})
	if err != nil {
		return ErrFeedbackUntrusted
	}
	_, fromDomain, _ := strings.Cut(from, "@")
	for _, verification := range verifications {
		if verification.Err == nil && strings.EqualFold(verification.Domain, fromDomain) && slices.Contains(source.DKIMDomains, strings.ToLower(verification.Domain)) && feedbackSignedHeaders(verification.HeaderKeys) {
			return nil
		}
	}
	if unavailable.Load() || ctx.Err() != nil {
		return ErrFeedbackUnavailable
	}
	return ErrFeedbackUntrusted
}

func feedbackSignedHeaders(keys []string) bool {
	for _, required := range []string{"from", "message-id", "content-type", "mime-version"} {
		if !slices.ContainsFunc(keys, func(key string) bool { return strings.EqualFold(key, required) }) {
			return false
		}
	}
	return true
}

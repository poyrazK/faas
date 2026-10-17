package internalmail

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Producer is an operator-enrolled internal caller. The private configuration
// contains credentials and must never be returned by an API or logged.
type Producer struct {
	ID                    string   `json:"id"`
	Token                 string   `json:"token"`
	ReplacementToken      string   `json:"replacement_token,omitempty"`
	From                  []string `json:"from"`
	DailyRecipientLimit   int      `json:"daily_recipient_limit"`
	PendingRecipientLimit int      `json:"pending_recipient_limit"`
	DispatchPerMinute     int      `json:"dispatch_per_minute,omitempty"`
}

func validProducerID(id string) bool {
	return len(id) <= api.InternalMailMaxProducerIDBytes && validNamespace(id)
}

func parseProducers(raw, serviceToken, reportToken, operationsToken string, feeds map[string]FeedbackSource) (map[string]Producer, error) {
	if len(raw) > api.InternalMailMaxProducerConfigBytes {
		return nil, errors.New("internal mail: invalid producer configuration")
	}
	var producers []struct {
		ID                    string   `json:"id"`
		Token                 string   `json:"token"`
		ReplacementToken      string   `json:"replacement_token"`
		From                  []string `json:"from"`
		DailyRecipientLimit   *int     `json:"daily_recipient_limit"`
		PendingRecipientLimit *int     `json:"pending_recipient_limit"`
		DispatchPerMinute     *int     `json:"dispatch_per_minute"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&producers) != nil {
		return nil, errors.New("internal mail: invalid producer configuration")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("internal mail: invalid producer configuration")
	}
	configured := make(map[string]Producer, len(producers))
	for _, enrollment := range producers {
		p := Producer{ID: enrollment.ID, Token: enrollment.Token, ReplacementToken: enrollment.ReplacementToken, From: enrollment.From,
			DailyRecipientLimit: api.InternalMailProducerDailyRecipients, PendingRecipientLimit: api.InternalMailProducerPending}
		if _, exists := configured[p.ID]; exists {
			return nil, errors.New("internal mail: duplicate producer identity")
		}
		if enrollment.DailyRecipientLimit != nil {
			p.DailyRecipientLimit = *enrollment.DailyRecipientLimit
		}
		if enrollment.PendingRecipientLimit != nil {
			p.PendingRecipientLimit = *enrollment.PendingRecipientLimit
		}
		if enrollment.DispatchPerMinute != nil {
			if *enrollment.DispatchPerMinute < 1 || *enrollment.DispatchPerMinute > api.InternalMailMaxProducerDispatch {
				return nil, errors.New("internal mail: producer dispatch limits must be within internal bounds")
			}
			p.DispatchPerMinute = *enrollment.DispatchPerMinute
		}
		configured[p.ID] = p
	}
	if err := validateProducers(configured, serviceToken, reportToken, operationsToken, feeds); err != nil {
		return nil, err
	}
	return configured, nil
}

func validateProducers(producers map[string]Producer, serviceToken, reportToken, operationsToken string, feeds map[string]FeedbackSource) error {
	if len(producers) == 0 || len(producers) > api.InternalMailMaxProducers {
		return errors.New("internal mail: producers require bounded enrolled callers")
	}
	tokens := map[string]bool{serviceToken: true, reportToken: true, operationsToken: true}
	for _, feed := range feeds {
		tokens[feed.Token] = true
	}
	for id, p := range producers {
		if !validProducerID(id) || p.ID != id || !validToken(p.Token) || tokens[p.Token] {
			return errors.New("internal mail: producers require distinct identities and credentials")
		}
		tokens[p.Token] = true
		if p.ReplacementToken != "" {
			if !validToken(p.ReplacementToken) || tokens[p.ReplacementToken] {
				return errors.New("internal mail: replacement credentials must be valid and distinct")
			}
			tokens[p.ReplacementToken] = true
		}
		if !producerSendersValid(p.From) {
			return errors.New("internal mail: producers require exact approved From mailboxes")
		}
		if p.DailyRecipientLimit < 1 || p.DailyRecipientLimit > api.InternalMailDailyRecipients || p.PendingRecipientLimit < 1 || p.PendingRecipientLimit > api.InternalMailMaxPending {
			return errors.New("internal mail: producer budgets must be positive and within internal limits")
		}
		if p.dispatchLimit() < 1 || p.dispatchLimit() > api.InternalMailMaxProducerDispatch {
			return errors.New("internal mail: producer dispatch limits must be within internal bounds")
		}
	}
	return nil
}

func producerSendersValid(senders []string) bool {
	if len(senders) == 0 || len(senders) > api.InternalMailMaxProducerSenders {
		return false
	}
	seen := map[string]bool{}
	for _, value := range senders {
		address, err := parseAddress(value)
		if err != nil || address.Name != "" || address.Address != value || seen[value] || strings.ContainsAny(value, "*?") {
			return false
		}
		seen[value] = true
	}
	return true
}

func (p Producer) allows(from string) bool {
	for _, approved := range p.From {
		if approved == from {
			return true
		}
	}
	return false
}

package internalmail

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// FeedbackSource is an explicitly enrolled provider feed. A private collector
// credential is insufficient by itself: the original report must also carry a
// valid DKIM signature from an exact approved domain. Recipient domains and
// formats bound each feed's authority. Configuration is private, never an API.
type FeedbackSource struct {
	ID               string   `json:"id"`
	Token            string   `json:"token"`
	DKIMDomains      []string `json:"dkim_domains"`
	ReportFrom       []string `json:"report_from"`
	RecipientDomains []string `json:"recipient_domains"`
	Formats          []string `json:"formats"`
}

func parseFeedbackSources(raw, serviceToken, reportToken string) (map[string]FeedbackSource, error) {
	if len(raw) > api.InternalMailMaxFeedbackConfigBytes {
		return nil, errors.New("internal mail: invalid feedback source configuration")
	}
	var sources []FeedbackSource
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sources); err != nil {
		return nil, errors.New("internal mail: invalid feedback source configuration")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("internal mail: invalid feedback source configuration")
	}
	configured := make(map[string]FeedbackSource, len(sources))
	for _, source := range sources {
		if _, exists := configured[source.ID]; exists {
			return nil, errors.New("internal mail: duplicate feedback source")
		}
		configured[source.ID] = source
	}
	if err := validateFeedbackSources(configured, serviceToken, reportToken); err != nil {
		return nil, err
	}
	return configured, nil
}

func validateFeedbackSources(sources map[string]FeedbackSource, serviceToken, reportToken string) error {
	if len(sources) == 0 || len(sources) > api.InternalMailMaxFeedbackSources {
		return errors.New("internal mail: feedback requires bounded enrolled sources")
	}
	tokens := map[string]bool{serviceToken: true, reportToken: true}
	for id, source := range sources {
		if !validFeedbackSourceID(id) || source.ID != id || !validToken(source.Token) || tokens[source.Token] {
			return errors.New("internal mail: feedback requires distinct source identities and credentials")
		}
		tokens[source.Token] = true
		if !feedbackDomainsValid(source.DKIMDomains) || !feedbackDomainsValid(source.RecipientDomains) {
			return errors.New("internal mail: feedback requires exact signing and recipient domains")
		}
		if !feedbackReportFromValid(source.ReportFrom, source.DKIMDomains) {
			return errors.New("internal mail: feedback requires exact aligned report sender mailboxes")
		}
		if len(source.Formats) == 0 || len(source.Formats) > 2 {
			return errors.New("internal mail: feedback requires dsn or arf formats")
		}
		seen := map[string]bool{}
		for _, format := range source.Formats {
			if (format != "dsn" && format != "arf") || seen[format] {
				return errors.New("internal mail: feedback requires distinct dsn or arf formats")
			}
			seen[format] = true
		}
	}
	return nil
}

func validFeedbackSourceID(id string) bool {
	return len(id) <= api.InternalMailMaxFeedbackSourceBytes && validNamespace(id)
}

func feedbackDomainsValid(domains []string) bool {
	if len(domains) == 0 || len(domains) > api.InternalMailMaxFeedbackDomains {
		return false
	}
	seen := map[string]bool{}
	for _, domain := range domains {
		if domain != strings.ToLower(domain) || !validDomain(domain) || seen[domain] {
			return false
		}
		seen[domain] = true
	}
	return true
}

func feedbackReportFromValid(addresses, signingDomains []string) bool {
	if len(addresses) == 0 || len(addresses) > api.InternalMailMaxFeedbackDomains {
		return false
	}
	seen := map[string]bool{}
	for _, value := range addresses {
		address, err := parseAddress(value)
		if err != nil || address.Name != "" || address.Address != value || seen[value] || strings.ContainsAny(value, "*?") {
			return false
		}
		_, domain, _ := strings.Cut(value, "@")
		aligned := false
		for _, signer := range signingDomains {
			aligned = aligned || signer == domain
		}
		if !aligned {
			return false
		}
		seen[value] = true
	}
	return true
}

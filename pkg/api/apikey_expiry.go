package api

import (
	"slices"
	"time"
)

// NewAPIKeyExpiry is the expiry stamped on a newly minted API key. Every
// key, full-access (admin) ones included, expires DefaultAPIKeyLifetimeDays
// after it is minted, so a leaked key stops working within a year even if
// nobody rotates it. Agent keys run unattended and expire sooner
// (AgentKeyLifetime, ADR-20261029085359). Keys minted before this rule keep
// whatever expiry they had (ADR-20261114120000).
func NewAPIKeyExpiry(scopes []string, now time.Time) *time.Time {
	t := now.UTC().Add(time.Duration(DefaultAPIKeyLifetimeDays) * 24 * time.Hour)
	if slices.Contains(scopes, ScopeAgent) {
		t = now.UTC().Add(AgentKeyLifetime)
	}
	return &t
}

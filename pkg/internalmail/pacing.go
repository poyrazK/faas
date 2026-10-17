package internalmail

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrDomainPaced = errors.New("internal mail recipient domain is paced")

const pacingWaitCode = "recipient_domain_paced"

// PacingPolicy permits at most one new submission reservation per interval for
// an exact recipient domain in a namespace. Zero disables pacing.
type PacingPolicy struct {
	Interval time.Duration
}

func (p PacingPolicy) valid() bool {
	return p.Interval == 0 || p.Interval >= api.InternalMailMinDomainPacingInterval &&
		p.Interval <= api.InternalMailMaxDomainPacingInterval && p.Interval%time.Second == 0
}

func loadPacingPolicy(getenv func(string) string) (PacingPolicy, error) {
	p := PacingPolicy{Interval: api.InternalMailDomainPacingInterval}
	if value := getenv("FAAS_INTERNAL_MAIL_DOMAIN_PACING_INTERVAL_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < int(api.InternalMailMinDomainPacingInterval/time.Second) || seconds > int(api.InternalMailMaxDomainPacingInterval/time.Second) {
			return PacingPolicy{}, errors.New("internal mail: domain pacing interval must be between 1 and 300 whole seconds")
		}
		p.Interval = time.Duration(seconds) * time.Second
	}
	return p, nil
}

func (s *Service) pacingEnabled() bool {
	return s.enabled() && s.PacingEnabled != nil && s.PacingEnabled()
}

func (s *Service) pacingPolicy() (PacingPolicy, error) {
	if !s.pacingEnabled() {
		return PacingPolicy{}, nil
	}
	p := s.PacingPolicy
	if p.Interval == 0 {
		p.Interval = api.InternalMailDomainPacingInterval
	}
	if !p.valid() {
		return PacingPolicy{}, ErrInvalid
	}
	return p, nil
}

type pacingStore interface {
	claimPaced(context.Context, domainAuthorization, []string, bool, PacingPolicy) (Recipient, error)
	beginPacedSubmission(context.Context, Recipient, string, domainAuthorization, []string, bool, PacingPolicy) error
}

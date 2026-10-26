package appevents

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state"
)

// accountProducer names whom an account-route publish speaks for
// (ADR-20261025211616): the company a key is bound to, else the personal
// domain. Fanout keeps a company's events inside it unless the account's owner
// may still work in the company, and a key bound to a company publishes there
// only while its holder may deploy to the company's apps.
func (s *Service) accountProducer(ctx context.Context, acct state.Account) (string, *api.Problem) {
	_, key, _ := authmw.AccountFromCtx(ctx)
	if key == nil || key.OrgID == "" {
		return eventcontract.ProducerPersonal, nil
	}
	org, err := s.Host.Store().OrgByID(ctx, key.OrgID)
	if err != nil {
		s.Host.Logger().ErrorContext(ctx, "read event publisher organization", "err", err)
		return "", api.ErrCapacity("failed to read the key's organization")
	}
	if org.Personal {
		return eventcontract.ProducerPersonal, nil
	}
	access, ok := s.Host.Store().(state.OrgAccessStore)
	if !ok {
		return "", api.ErrInternal("organization access store")
	}
	member, err := access.OrgAccessSnapshot(ctx, org.ID, acct.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		s.Host.Logger().ErrorContext(ctx, "read event publisher membership", "err", err)
		return "", api.ErrCapacity("failed to read organization membership")
	}
	if err != nil || org.DeletedPending || org.Status == state.OrgStatusDeletedPending || !eventProducerRole(member.Role) {
		return "", api.ErrOrgRoleForbidden("publishing events")
	}
	return eventcontract.OrgProducer(org.ID), nil
}

// eventProducerRole is the role that may route events into and out of a
// company. A developer can already deploy code that publishes for the
// company's apps and reads what they receive; a viewer or billing member
// cannot. enqueue_event_fanout applies the same set.
func eventProducerRole(role state.OrgRole) bool {
	return role == state.OrgRoleOwner || role == state.OrgRoleAdmin || role == state.OrgRoleDeveloper
}

// AppSourceReservedProblem refuses an `app.<app id>` source on a route that
// does not speak for that app. The namespace belongs to the app's producer
// route and its own runtime (ADR-929, as amended 2026-10-26): consumers such
// as the Auth app's workflow starts trust it as the app's word.
func AppSourceReservedProblem() *api.Problem {
	return api.ErrValidation("app.<app id> sources are reserved for that app's own events; publish them with POST /v1/apps/{slug}/events:publish or from the app's runtime")
}

// PublishRateProblem charges n events to the account's publish rate
// (ADR-20261026140000).
func (s *Service) PublishRateProblem(acct state.Account, n int) *api.Problem {
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return nil
	}
	return s.Host.PublishLimiter().PublishRateProblem(acct.ID, limits.EventPublish, n)
}

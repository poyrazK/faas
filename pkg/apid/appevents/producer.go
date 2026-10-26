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

package support

import "context"

// StaffRole returns the actor's support role ("agent" or "manager") after
// the same checks every staff route applies: an active, verified account,
// an MFA-enrolled session principal, and a current roster entry (or an
// operator bootstrapping as manager). Other pages that belong to the staff
// inbox, such as attention signals (ADR-20261113123000), call it before
// they render.
func (s *Service) StaffRole(ctx context.Context, a Actor) (string, error) {
	v, err := s.transact(ctx, a, "", true, false, func(x *session) (any, error) { return x.role, nil })
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

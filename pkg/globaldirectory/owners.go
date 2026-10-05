package globaldirectory

// adr: 609
import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	ErrOwnerInactive         = errors.New("global directory owner inactive")
	ErrOwnerForbidden        = errors.New("global directory owner role forbidden")
	ErrOwnerPlanChanged      = errors.New("global directory owner plan changed")
	ErrOrgTargetsUnavailable = errors.New("shared regional authorization unavailable")
)

// Owner is a stable tenant root, distinct from the authenticated actor.
// Personal roots retain the account UUID. Shared roots have an independent UUID.
type Owner struct{ ID, AccountID, OrgID string }

func (o Owner) Shared() bool { return o.OrgID != "" }

type OwnerStore interface {
	OwnerByID(context.Context, string) (Owner, error)
	ResolveOrgOwner(context.Context, string) (Owner, error)
	CreateOrgApp(context.Context, string, string, api.Plan, string, int) (App, error)
}

// OrgTargetStore rechecks live authority when committing verified shared bindings.
type OrgTargetStore interface {
	AttachOrgTarget(context.Context, string, string, string, Binding) (Target, error)
	DetachOrgTarget(context.Context, string, string, string, string) error
}

// OrgAuthority supplies live tenant state to the portable adapter. PostgreSQL
// checks these rows under locks in create_global_org_app instead.
type OrgAuthority interface {
	AccountByID(context.Context, string) (state.Account, error)
	OrgByID(context.Context, string) (state.Org, error)
	OrgMemberByAccount(context.Context, string, string) (state.OrgMembership, error)
}

func activeSharedOrg(ctx context.Context, authority OrgAuthority, id string) (state.Org, error) {
	if authority == nil {
		return state.Org{}, ErrOwnerInactive
	}
	org, err := authority.OrgByID(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		return state.Org{}, ErrOwnerInactive
	}
	if err != nil {
		return state.Org{}, err
	}
	if org.Personal || org.Status != state.OrgStatusActive || org.DeletedPending {
		return state.Org{}, ErrOwnerInactive
	}
	return org, nil
}

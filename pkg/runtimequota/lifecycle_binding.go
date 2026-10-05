package runtimequota

// adr: 621
import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// LifecycleBinding identifies the caller's actual operation. Comparing it
// with placement bookkeeping never grants execution authority.
type LifecycleBinding struct {
	InstanceID, AppID, AccountID, DeploymentID string
	Plan                                       api.Plan
	Purpose                                    string
}

func (p Placement) ValidateBinding(b LifecycleBinding) error {
	if p.Validate() != nil {
		return ErrInvalid
	}
	permit := p.Permit
	if !MatchesID(permit.InstanceID, b.InstanceID) || !MatchesID(permit.Grant.Binding.RegionalAppID, b.AppID) || !MatchesID(permit.Grant.Binding.RegionalAccountID, b.AccountID) || !MatchesID(permit.DeploymentID, b.DeploymentID) || permit.Grant.Policy.Plan != b.Plan {
		return ErrBinding
	}
	switch b.Purpose {
	case CapacityServing:
		if (permit.Kind != CapacityServing && permit.Kind != CapacityWarm) || p.Seat == nil {
			return ErrBinding
		}
	case CapacityWarm, CapacityPrime, CapacityAppTask:
		if permit.Kind != b.Purpose || p.Seat != nil {
			return ErrBinding
		}
	default:
		return ErrBinding
	}
	return nil
}

// MatchesID compares a canonical protocol UUID with an actual row/runtime key.
// Gregale also stores compact UUIDs. This never rewrites immutable claims or
// changes the key used to look up a resident; host launch uniqueness still binds
// one physical permit to one attempt regardless of a caller's UUID encoding.
func MatchesID(canonical, actual string) bool {
	u, err := uuid.Parse(actual)
	return validID(canonical) && err == nil && u.String() == canonical
}

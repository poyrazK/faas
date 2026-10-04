package runtimequota

// adr: 495
import "github.com/onebox-faas/faas/pkg/api"

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
	if permit.InstanceID != b.InstanceID || permit.Grant.Binding.RegionalAppID != b.AppID || permit.Grant.Binding.RegionalAccountID != b.AccountID || permit.DeploymentID != b.DeploymentID || permit.Grant.Policy.Plan != b.Plan {
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

package globaldirectory

// ADR-482: the directory retains only sealed purpose-scoped authority.
import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	DelegationPending  = "pending"
	DelegationActive   = "active"
	DelegationRevoking = "revoking"
	DelegationRevoked  = "revoked"
	DelegationExpired  = "expired"
)

var ErrDelegationUnavailable = errors.New("regional delegation unavailable")
var ErrDelegationSealed = errors.New("regional delegation seal invalid or unavailable")
var ErrDelegationOwnerInactive = errors.New("global release owner inactive")

type DelegationLimitError struct{ Limit, Observed int64 }

func (e *DelegationLimitError) Error() string { return "global delegation bound reached" }

type Delegation struct {
	ID          string
	GlobalAppID string
	OwnerID     string
	Slug        string
	Binding     Binding
	APIBase     string
	Hash        []byte `json:"-"`
	Sealed      []byte `json:"-"`
	ExpiresAt   time.Time
	CreatedAt   time.Time
	Status      string
}

func (g Delegation) usable(now time.Time) bool {
	return (g.Status == DelegationPending || g.Status == DelegationActive) && g.ExpiresAt.After(now)
}
func (g Delegation) identityValid() bool {
	return ValidID(g.ID) && ValidID(g.GlobalAppID) && validOwnerID(g.OwnerID) && ValidSlug(g.Slug) &&
		g.Binding.Validate() == nil && ValidAPIBase(g.APIBase) && len(g.Hash) == api.RegionalReleaseTokenRandomBytes && !g.ExpiresAt.IsZero()
}
func (g Delegation) matches(t Target) bool {
	return g.GlobalAppID == t.GlobalAppID && g.Binding == t.Binding && g.APIBase == t.APIBase && t.Resolvable()
}
func copyDelegation(g Delegation) Delegation {
	g.Hash = append([]byte(nil), g.Hash...)
	g.Sealed = append([]byte(nil), g.Sealed...)
	return g
}

type RecoveryWork struct {
	GlobalAppID, OwnerID, Slug, ReleaseID, ClaimID, WakeID, CursorID string
}

type DelegationStore interface {
	EnsureDelegation(context.Context, string, string, Delegation) (Delegation, error)
	CurrentDelegation(context.Context, string, string, string) (Delegation, error)
	ReadDelegation(context.Context, string, string, string) (Delegation, error)
	CheckDelegation(context.Context, string, string, string) error
	SetDelegationStatus(context.Context, string, string, string, string) error
	ListRecoveryDelegations(context.Context, string, string) ([]Delegation, error)
	ClaimRecovery(context.Context, string) ([]RecoveryWork, error)
	FinishRecovery(context.Context, RecoveryWork) error
}

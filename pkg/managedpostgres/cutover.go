package managedpostgres

import (
	"context"
	"time"
)

type CutoverState string

const (
	CutoverPreparing  CutoverState = "preparing"
	CutoverPrepared   CutoverState = "prepared"
	CutoverCancelling CutoverState = "cancelling"
	CutoverCancelled  CutoverState = "cancelled"
)

// CutoverPrepared means target credentials are sealed, not that workloads have
// moved or SQL connectivity has been verified. Activation requires writer drain.
type Cutover struct {
	ID, AccountID, AppID, Scope   string
	Source, Target                Database `json:"-"`
	State                         CutoverState
	LastErrorCode                 string
	LeaseToken                    string    `json:"-"`
	LeaseUntil                    time.Time `json:"-"`
	AttemptCount                  int32
	RetryAt, CreatedAt, UpdatedAt time.Time
	Credentials                   []CutoverCredential `json:"-"`
}
type CutoverCredential struct {
	ID, SourceBindingID, EnvironmentKey string
	SourceCredentialGeneration          int64
	Access                              CredentialAccess
	State                               string
	Sealed                              SealedCredential `json:"-"`
}

// SealedCredential is an unpublished age envelope. It never enters app_secrets
// during preparation and must never be rendered in a customer response.
type SealedCredential struct {
	ProviderIdentityID string `json:"-"`
	Ref                string `json:"-"`
	Ciphertext         []byte `json:"-"`
	Kid                string `json:"-"`
	ValueHash          string `json:"-"`
}
type CredentialSealer interface {
	SealCredential(context.Context, Binding, CredentialMaterial) (SealedCredential, error)
}
type PrepareCutoverRequest struct{ ID, AccountID, AppID, Scope, SourceDatabaseID, TargetDatabaseID string }

type CutoverStore interface {
	ReserveCutover(context.Context, PrepareCutoverRequest, time.Time) (Cutover, bool, error)
	GetCutover(context.Context, string, string) (Cutover, error)
	ClaimCutover(context.Context, string, string, string, time.Time, time.Time) (Cutover, error)
	SaveCutoverCredential(context.Context, Cutover, CutoverCredential, SealedCredential, time.Time) error
	RevokeCutoverCredential(context.Context, Cutover, CutoverCredential, time.Time) error
	ReleaseCutover(context.Context, Cutover, string, time.Time, time.Time) error
	CancelCutover(context.Context, string, string, time.Time) (Cutover, error)
	DueCutovers(context.Context, bool, int, time.Time) ([]Cutover, error)
}

func cutoverBinding(c Cutover, member CutoverCredential) Binding {
	return Binding{ID: member.ID, AccountID: c.AccountID, AppID: c.AppID, Scope: c.Scope,
		DatabaseID: c.Target.ID, EnvironmentKey: member.EnvironmentKey, Access: member.Access, CredentialGeneration: 1}
}
func validPrepareCutover(r PrepareCutoverRequest, now time.Time) bool {
	return r.ID != "" && r.AccountID != "" && r.AppID != "" && r.SourceDatabaseID != "" && r.TargetDatabaseID != "" && r.SourceDatabaseID != r.TargetDatabaseID && validBindingScope(r.Scope) && !now.IsZero()
}
func sameCutoverRequest(c Cutover, r PrepareCutoverRequest) bool {
	return c.AccountID == r.AccountID && c.AppID == r.AppID && c.Scope == r.Scope && c.Source.ID == r.SourceDatabaseID && c.Target.ID == r.TargetDatabaseID
}
func validSealedCredential(c SealedCredential) bool {
	return validOpaqueID(c.ProviderIdentityID) && validOpaqueID(c.Ref) && len(c.Ciphertext) > 0 && c.Kid != "" && c.ValueHash != ""
}

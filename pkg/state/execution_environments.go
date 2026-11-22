package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/runfile"
	"time"
)

var ErrExecutionEnvironmentUnavailable = errors.New("state: Run environment unavailable")
var ErrExecutionEnvironmentQuota = errors.New("state: Run environment quota exceeded")
var ErrExecutionEnvironmentLease = errors.New("state: Run environment lease lost")

type ExecutionEnvironment struct {
	ID, AccountID, PrincipalID                                         string
	Runtime                                                            api.ExecutionRuntime
	Recipe                                                             api.CreateExecutionEnvironmentRequest
	RecipeDigest, BaseRef, Status                                      string
	Lease                                                              string
	LeaseExpiresAt                                                     time.Time
	BuildAttempts, ImageAttempts                                       int
	ExportKey, ExportDigest, BaseKey, ImageDigest, GuestExecutorDigest string
	ArtifactKeys                                                       []string
	ArtifactBytes                                                      int64
	FailureCode                                                        string
	CreatedAt                                                          time.Time
	DiagnosticSequence                                                 int64
	Diagnostics                                                        []api.ExecutionEnvironmentEvent
}

type ExecutionEnvironmentStore interface {
	CreateExecutionEnvironment(context.Context, ExecutionEnvironment) (ExecutionEnvironment, error)
	ExecutionEnvironmentByID(context.Context, string, string) (ExecutionEnvironment, error)
	ListExecutionEnvironments(context.Context, string, string, bool) ([]ExecutionEnvironment, error)
	RetireExecutionEnvironment(context.Context, string, string, string, bool) error
	ClaimExecutionEnvironment(context.Context, string) (ExecutionEnvironment, error)
	PublishExecutionEnvironmentBuild(context.Context, ExecutionEnvironment, string) error
	PublishExecutionEnvironmentImage(context.Context, ExecutionEnvironment, string, string, int64) error
	FailExecutionEnvironment(context.Context, ExecutionEnvironment, string) error
	CollectableExecutionEnvironments(context.Context) ([]ExecutionEnvironment, error)
	DeleteExecutionEnvironmentArtifacts(context.Context, string) error
}

func validateExecutionEnvironment(row ExecutionEnvironment) error {
	if !runenv.ValidID(row.ID) || !runfile.ValidOwnerID(row.AccountID) || row.PrincipalID != "" && !runfile.ValidOwnerID(row.PrincipalID) || row.Runtime != row.Recipe.Runtime {
		return ErrExecutionInvalid
	}
	digest, err := runenv.Digest(row.Recipe, row.BaseRef)
	if err != nil || digest != row.RecipeDigest {
		return ErrExecutionInvalid
	}
	return nil
}
func environmentUsable(row ExecutionEnvironment, account, principal string, wide bool, runtime api.ExecutionRuntime) bool {
	return row.AccountID == account && (wide || row.PrincipalID == principal) && row.Status == "ready" && row.Runtime == runtime && row.ImageDigest != "" && row.BaseKey != ""
}
func cloneExecutionEnvironment(row ExecutionEnvironment) ExecutionEnvironment {
	row.Recipe = runenv.Canonical(row.Recipe)
	row.ArtifactKeys = append([]string(nil), row.ArtifactKeys...)
	row.Diagnostics = append([]api.ExecutionEnvironmentEvent(nil), row.Diagnostics...)
	return row
}
func environmentRecipeJSON(row ExecutionEnvironment) []byte {
	body, _ := json.Marshal(row.Recipe)
	return body
}

func validEnvironmentDigest(d string) bool {
	b, err := hex.DecodeString(d)
	return err == nil && len(b) == 32 && d == hex.EncodeToString(b)
}
func validEnvironmentFailure(code string) bool {
	switch code {
	case "build_failed", "image_failed", "account_inactive", "worker_recovery_exhausted":
		return true
	}
	return false
}

func validEnvironmentID(id string) bool { return runenv.ValidID(id) }
func pgUUIDStringOrEmpty(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return pgUUIDString(id)
}

// ExecutionEnvironmentLayerKey names the payload-free per-environment layer.
func ExecutionEnvironmentLayerKey(row ExecutionEnvironment) string {
	return runenv.ArtifactKey(row.AccountID, row.ID, row.Lease, "layer.ext4")
}

type ExecutionEnvironmentQuotaError struct{ Limit, Observed int }

func (e *ExecutionEnvironmentQuotaError) Error() string {
	return fmt.Sprintf("Run environment quota exceeded: %d > %d", e.Observed, e.Limit)
}
func (e *ExecutionEnvironmentQuotaError) Unwrap() error { return ErrExecutionEnvironmentQuota }

// Versioned pending/running states prevent older workers from dropping recipe
// fields they cannot interpret. Ready images use the ordinary read contract.
func environmentWorkerStatus(status string, recipe api.CreateExecutionEnvironmentRequest) string {
	if len(recipe.SystemPackages) > 0 {
		return status + "_system_v1"
	}
	return status
}

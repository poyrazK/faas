package sched

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ExecutionRuntimeArtifacts are the trusted, platform-owned inputs needed to
// construct an execution VM. They are deliberately separate from the claim:
// a caller can choose a runtime and machine shape, but never an artifact key,
// digest, or compute-node identity.
type ExecutionRuntimeArtifacts struct {
	Architecture        string
	KernelDigest        string
	GuestExecutorDigest string
	BaseImageDigest     string
	KernelKey           string
	BaseKey             string
	LayerKey            string
	FCVersion           string
}

// ExecutionRuntimeArtifactsResolver supplies the release-pinned artifacts for
// one runtime. Implementations must not derive any value from tenant payload.
type ExecutionRuntimeArtifactsResolver interface {
	ResolveExecutionArtifacts(context.Context, api.ExecutionRuntime, api.ExecutionSnapshotShape) (ExecutionRuntimeArtifacts, error)
}

// ExecutionRuntimeArtifactsFunc adapts a function to the resolver interface.
type ExecutionRuntimeArtifactsFunc func(context.Context, api.ExecutionRuntime, api.ExecutionSnapshotShape) (ExecutionRuntimeArtifacts, error)

func (f ExecutionRuntimeArtifactsFunc) ResolveExecutionArtifacts(ctx context.Context, runtime api.ExecutionRuntime, shape api.ExecutionSnapshotShape) (ExecutionRuntimeArtifacts, error) {
	if f == nil {
		return ExecutionRuntimeArtifacts{}, ErrExecutionClaimResolverUnwired
	}
	return f(ctx, runtime, shape)
}

// StaticExecutionRuntimeArtifacts is a small immutable resolver useful for
// startup wiring and tests. Publication of release metadata can build this map
// once, then hand it to schedd without exposing mutable catalog state.
type StaticExecutionRuntimeArtifacts map[api.ExecutionRuntime]ExecutionRuntimeArtifacts

func (s StaticExecutionRuntimeArtifacts) ResolveExecutionArtifacts(_ context.Context, runtime api.ExecutionRuntime, shape api.ExecutionSnapshotShape) (ExecutionRuntimeArtifacts, error) {
	artifacts, ok := s[runtime]
	if !ok {
		return ExecutionRuntimeArtifacts{}, fmt.Errorf("%w: runtime %q", ErrExecutionRuntimeArtifactsUnavailable, runtime)
	}
	if err := artifacts.validate(runtime, shape); err != nil {
		return ExecutionRuntimeArtifacts{}, err
	}
	return artifacts, nil
}

var (
	ErrExecutionClaimResolverUnwired        = errors.New("sched: execution claim resolver is not wired")
	ErrExecutionClaimInvalid                = errors.New("sched: execution claim is invalid")
	ErrExecutionRuntimeArtifactsUnavailable = errors.New("sched: execution runtime artifacts unavailable")
)

// ExecutionClaimResolver turns a durable claim into a payload-free restore
// envelope. Account plan and release artifacts come from trusted state; the
// runtime and resource shape come from the already-resolved claim.
type ExecutionClaimResolver struct {
	accounts interface {
		AccountByID(context.Context, string) (state.Account, error)
	}
	catalog   *RuntimeSnapshotCatalog
	artifacts ExecutionRuntimeArtifactsResolver
	nodeID    string
}

func NewExecutionClaimResolver(accounts interface {
	AccountByID(context.Context, string) (state.Account, error)
}, catalog *RuntimeSnapshotCatalog, artifacts ExecutionRuntimeArtifactsResolver, nodeID string) *ExecutionClaimResolver {
	return &ExecutionClaimResolver{accounts: accounts, catalog: catalog, artifacts: artifacts, nodeID: strings.TrimSpace(nodeID)}
}

// ResolveExecutionClaim returns only machine metadata. In particular, the
// sealed source/input payload is never inspected or copied by this resolver.
func (r *ExecutionClaimResolver) ResolveExecutionClaim(ctx context.Context, claim state.ExecutionClaim) (ExecutionRestoreRequest, error) {
	if r == nil || r.accounts == nil || r.artifacts == nil || r.nodeID == "" {
		return ExecutionRestoreRequest{}, ErrExecutionClaimResolverUnwired
	}
	if err := validateExecutionClaimEnvelope(claim); err != nil {
		return ExecutionRestoreRequest{}, err
	}
	account, err := r.accounts.AccountByID(ctx, claim.AccountID)
	if err != nil {
		return ExecutionRestoreRequest{}, fmt.Errorf("sched: resolve execution account: %w", err)
	}
	planLimits, ok := account.Plan.ExecutionLimits()
	if !ok || !planLimits.Allowed {
		return ExecutionRestoreRequest{}, fmt.Errorf("%w: account plan %q", ErrExecutionClaimInvalid, account.Plan)
	}
	if err := validateExecutionClaimLimits(claim.Limits, planLimits); err != nil {
		return ExecutionRestoreRequest{}, err
	}

	shape := api.ExecutionSnapshotShape{
		Runtime: claim.Runtime, MemoryMB: claim.Limits.MemoryMB, EphemeralDiskMB: claim.Limits.EphemeralDiskMB,
	}
	artifacts, err := r.artifacts.ResolveExecutionArtifacts(ctx, claim.Runtime, shape)
	if err != nil {
		return ExecutionRestoreRequest{}, fmt.Errorf("sched: resolve execution artifacts: %w", err)
	}
	if err := artifacts.validate(claim.Runtime, shape); err != nil {
		return ExecutionRestoreRequest{}, err
	}
	plan, err := r.catalog.Resolve(ctx, RuntimeSnapshotRequest{
		Shape: shape, Architecture: artifacts.Architecture,
		KernelDigest: artifacts.KernelDigest, GuestExecutorDigest: artifacts.GuestExecutorDigest,
		BaseImageDigest: artifacts.BaseImageDigest, FormatVersion: CurrentRuntimeSnapshotFormatVersion,
	})
	if err != nil {
		return ExecutionRestoreRequest{}, fmt.Errorf("sched: resolve execution snapshot: %w", err)
	}

	request := ExecutionRestoreRequest{
		ID: claim.ID, AccountID: claim.AccountID, NodeID: r.nodeID,
		Plan: account.Plan, Runtime: claim.Runtime, NetworkMode: claim.NetworkMode,
		Limits: claim.Limits, DeadlineAt: claim.DeadlineAt,
		KernelKey: artifacts.KernelKey, BaseKey: artifacts.BaseKey, LayerKey: artifacts.LayerKey,
		VcpuCount: api.VCPUPerPlan[account.Plan], MemSizeMiB: claim.Limits.MemoryMB,
		CPUMillicores: claim.Limits.CPUMillicores,
	}
	if plan.Mode == RuntimeSnapshotRestore && plan.Snapshot != nil {
		request.Snapshot = SnapshotRef{
			DeploymentID:      claim.ID,
			FCVersion:         artifacts.FCVersion,
			StorageKey:        plan.Snapshot.StorageKey,
			VMStateStorageKey: runtimeSnapshotVMStateStorageKey(plan.Snapshot.StorageKey),
			Networkless:       true,
		}
	}
	return request, nil
}

func validateExecutionClaimEnvelope(claim state.ExecutionClaim) error {
	if strings.TrimSpace(claim.ID) == "" || strings.TrimSpace(claim.AccountID) == "" || !claim.Runtime.Valid() {
		return fmt.Errorf("%w: identity or runtime is invalid", ErrExecutionClaimInvalid)
	}
	if claim.NetworkMode != api.ExecutionNetworkNone {
		return fmt.Errorf("%w: network mode %q is not allowed", ErrExecutionClaimInvalid, claim.NetworkMode)
	}
	if claim.DeadlineAt.IsZero() {
		return fmt.Errorf("%w: deadline is required", ErrExecutionClaimInvalid)
	}
	return nil
}

func validateExecutionClaimLimits(limits api.ResolvedExecutionLimits, plan api.ExecutionPlanLimits) error {
	if limits.TimeoutMS < api.ExecutionTimeoutMinMS || limits.TimeoutMS > api.ExecutionTimeoutHardMaxMS ||
		!api.ValidExecutionMemoryMB(limits.MemoryMB) || !api.ValidAppCPUMillicores(limits.CPUMillicores) ||
		!api.ValidExecutionEphemeralDiskMB(limits.EphemeralDiskMB) ||
		limits.MaxOutputBytes < api.ExecutionOutputMinBytes || limits.MaxOutputBytes > api.ExecutionOutputHardMaxBytes ||
		limits.PIDsMax != api.ExecutionPIDsMax {
		return fmt.Errorf("%w: resource envelope is outside hard bounds", ErrExecutionClaimInvalid)
	}
	if limits.TimeoutMS > plan.MaxTimeoutMS || limits.MemoryMB > plan.MaxMemoryMB ||
		limits.CPUMillicores > plan.MaxCPUMillicores || limits.EphemeralDiskMB > plan.MaxEphemeralDiskMB ||
		limits.MaxOutputBytes > plan.MaxOutputBytes {
		return fmt.Errorf("%w: resource envelope exceeds account plan", ErrExecutionClaimInvalid)
	}
	return nil
}

func (a ExecutionRuntimeArtifacts) validate(runtime api.ExecutionRuntime, shape api.ExecutionSnapshotShape) error {
	if !runtime.Valid() || shape.Runtime != runtime {
		return fmt.Errorf("%w: runtime identity mismatch", ErrExecutionClaimInvalid)
	}
	if a.Architecture != archAMD64 && a.Architecture != archARM64 {
		return fmt.Errorf("%w: unsupported execution architecture %q", ErrExecutionClaimInvalid, a.Architecture)
	}
	identity := RuntimeSnapshotRequest{
		Shape: shape, Architecture: a.Architecture, KernelDigest: a.KernelDigest,
		GuestExecutorDigest: a.GuestExecutorDigest, BaseImageDigest: a.BaseImageDigest,
		FormatVersion: CurrentRuntimeSnapshotFormatVersion,
	}.Identity()
	if err := identity.Validate(); err != nil {
		return fmt.Errorf("%w: artifact identity: %w", ErrExecutionClaimInvalid, err)
	}
	for name, key := range map[string]string{"kernel": a.KernelKey, "base": a.BaseKey, "layer": a.LayerKey} {
		if !validExecutionArtifactKey(key) {
			return fmt.Errorf("%w: %s artifact key is not canonical", ErrExecutionClaimInvalid, name)
		}
	}
	if strings.TrimSpace(a.FCVersion) == "" {
		return fmt.Errorf("%w: firecracker version is required", ErrExecutionClaimInvalid)
	}
	return nil
}

func validExecutionArtifactKey(value string) bool {
	if strings.TrimSpace(value) != value || value == "" || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// RuntimeSnapshotVMStateStorageKey derives the paired Firecracker vmstate
// object. Runtime snapshots publish a memory object key; the vmstate object
// follows the same sibling convention as ordinary snapshots.
func runtimeSnapshotVMStateStorageKey(storageKey string) string {
	if strings.HasSuffix(storageKey, "/mem") {
		return strings.TrimSuffix(storageKey, "/mem") + "/vmstate"
	}
	return storageKey + "/vmstate"
}

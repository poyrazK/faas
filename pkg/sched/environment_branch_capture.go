package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// The credential-free capture boot (ADR-20261031193635 slice 5a). An
// environment branch restores only from an init-tier capture of its parent
// deployment whose boot never held a real managed credential. The boot is the
// parent's prime boot with every managed binding secret replaced by a
// placeholder, no private network and no static egress IP.

// errCredentialFreeCaptureUnsupported fails a capture for good: the boot
// cannot be made credential-free, so its branches keep cold-booting.
var errCredentialFreeCaptureUnsupported = errors.New("sched: credential-free capture unsupported")

const (
	managedSecretPostgres      = "managed_postgres"
	managedSecretObjectStorage = "object_storage"
)

// managedSecretKinds maps each managed binding secret key to its kind.
func managedSecretKinds(rows []state.AppSecret) map[string]string {
	kinds := map[string]string{}
	for _, row := range rows {
		switch {
		case row.ManagedPostgresBindingID != "":
			kinds[row.Key] = managedSecretPostgres
		case row.ManagedObjectStorageCredentialID != "":
			kinds[row.Key] = managedSecretObjectStorage
		}
	}
	return kinds
}

// credentialFreePlaceholder is the plain value a capture boot gets in place
// of a managed secret. vmmd unseals secrets, so schedd never sees the real
// value: the placeholder's shape comes from the binding kind.
func credentialFreePlaceholder(kind, envKey string) string {
	switch {
	case kind == managedSecretPostgres:
		return state.CredentialFreeValue("postgres://placeholder@placeholder/placeholder")
	case kind == managedSecretObjectStorage && strings.HasSuffix(envKey, "_ENDPOINT"):
		return state.CredentialFreeValue("https://placeholder")
	default:
		return state.CredentialFreePlaceholder
	}
}

// credentialFreeCaptureEnv drops every managed secret from a boot's sealed
// env and delivery candidates and puts its placeholder in the plain env under
// the same key, replacing any plain value of that key.
func credentialFreeCaptureEnv(delivery sealedEnvDelivery, apiEnv []fcvm.APIEnvEntry, kinds map[string]string) (sealedEnvDelivery, []fcvm.APIEnvEntry) {
	placeholders := map[string]string{}
	entries := make([]fcvm.SealedEnvEntry, 0, len(delivery.Entries))
	for _, entry := range delivery.Entries {
		source := entry.SourceKey
		if source == "" {
			source = entry.Key
		}
		if kind, managed := kinds[source]; managed {
			placeholders[entry.Key] = credentialFreePlaceholder(kind, entry.Key)
			continue
		}
		entries = append(entries, entry)
	}
	candidates := make([]state.AppSecretDeliveryCandidate, 0, len(delivery.Candidates))
	for _, candidate := range delivery.Candidates {
		if _, managed := kinds[candidate.Key]; !managed {
			candidates = append(candidates, candidate)
		}
	}
	env := make([]fcvm.APIEnvEntry, 0, len(apiEnv)+len(placeholders))
	for _, entry := range apiEnv {
		if _, replaced := placeholders[entry.Key]; !replaced {
			env = append(env, entry)
		}
	}
	for _, entry := range delivery.Entries {
		if value, ok := placeholders[entry.Key]; ok {
			env = append(env, fcvm.APIEnvEntry{Key: entry.Key, Value: value})
			delete(placeholders, entry.Key)
		}
	}
	delivery.Entries, delivery.Candidates = entries, candidates
	return delivery, env
}

// sidecarsReceiveManagedSecrets reports whether a sidecar is granted a
// managed secret. Sidecars have no plain env for a placeholder, so such a
// capture cannot be made credential-free.
func sidecarsReceiveManagedSecrets(sidecars []fcvm.WorkloadSpec, kinds map[string]string) bool {
	for _, sidecar := range sidecars {
		for _, entry := range sidecar.SealedSecrets {
			source := entry.SourceKey
			if source == "" {
				source = entry.Key
			}
			if _, managed := kinds[source]; managed {
				return true
			}
		}
		for _, name := range sidecar.GrantedEnvNames {
			if _, managed := kinds[name]; managed {
				return true
			}
		}
	}
	return false
}

// prepareCredentialFreeCaptureBoot builds the parent deployment's capture
// boot: its prime boot with placeholders for managed secrets, no private
// network, no static egress IP, and the branch-capture environment.
func (e *Engine) prepareCredentialFreeCaptureBoot(ctx context.Context, app state.App, acct state.Account, limits api.Limits, dep state.Deployment, placement Placement, ins state.Instance) (deploymentPrimeBoot, error) {
	boot, err := e.prepareDeploymentPrimeBoot(ctx, app, acct, limits, dep, placement, ins)
	if err != nil {
		return boot, err
	}
	scopes := map[string]bool{}
	for _, candidate := range boot.SecretDeliveries {
		scopes[candidate.Scope] = true
	}
	var rows []state.AppSecret
	for scope := range scopes {
		scoped, err := e.store.ListAppSecretsInScope(ctx, acct.ID, app.ID, scope)
		if err != nil {
			return boot, fmt.Errorf("sched: credential-free capture: list secrets: %w", err)
		}
		rows = append(rows, scoped...)
	}
	kinds := managedSecretKinds(rows)
	if sidecarsReceiveManagedSecrets(boot.Spec.Sidecars, kinds) {
		return boot, fmt.Errorf("%w: a sidecar receives a managed binding secret", errCredentialFreeCaptureUnsupported)
	}
	delivery, apiEnv := credentialFreeCaptureEnv(sealedEnvDelivery{Entries: boot.Spec.SealedEnv, Candidates: boot.SecretDeliveries}, boot.Spec.APIEnv, kinds)
	boot.Spec.SealedEnv, boot.SecretDeliveries = delivery.Entries, delivery.Candidates
	boot.Spec.APIEnv = append(apiEnv, fcvm.APIEnvEntry{Key: "GREGALE_ENVIRONMENT_CAPTURE", Value: state.EnvironmentBranchCaptureEnvironment})
	boot.Spec.PrivateNetworkCIDRs, boot.Spec.PrivateNetworkAllowedCIDRs, boot.Spec.PrivateNetworkFirewallRules = nil, nil, nil
	boot.Spec.PrivateNetworkID, boot.Spec.PrivateNetworkAddress, boot.Spec.StaticEgressIP = "", "", ""
	return boot, nil
}

// requestEnvironmentBranchCapture queues the credential-free capture of a
// branch deployment's lineage parent when the app opts into branch restores
// and the branch is cold-booting. It is best effort: without the capture the
// branch keeps cold-booting.
func (e *Engine) requestEnvironmentBranchCapture(ctx context.Context, app state.App, dep state.Deployment) {
	if !app.Manifest.AfterRestore.ReloadsIdentity() {
		return
	}
	store, ok := e.store.(state.EnvironmentBranchCaptureStore)
	if !ok {
		return
	}
	parent, err := store.EnvironmentBranchLineageParent(ctx, dep.ID)
	if errors.Is(err, state.ErrNotFound) {
		return
	}
	if err == nil {
		_, err = store.RequestEnvironmentBranchCapture(ctx, parent)
	}
	if err != nil && e.log != nil {
		e.log.Warn("sched: credential-free capture request failed; the branch keeps cold-booting", "deployment_id", dep.ID, "err", err)
	}
}

// environmentBranchCaptureSnapshot returns the lineage parent's ready
// credential-free capture as the snapshot an opted-in branch wake restores
// (ADR-20261031193635 slice 5b). It never reads or marks a snapshots row: a
// capture that does not fit this wake is simply not used, and the branch
// cold-boots as on the default path.
func (e *Engine) environmentBranchCaptureSnapshot(ctx context.Context, app state.App, dep state.Deployment, expectedRAMMB int) (state.Snapshot, bool) {
	if !app.Manifest.AfterRestore.ReloadsIdentity() {
		return state.Snapshot{}, false
	}
	store, ok := e.store.(state.EnvironmentBranchCaptureStore)
	if !ok {
		return state.Snapshot{}, false
	}
	parent, err := store.EnvironmentBranchLineageParent(ctx, dep.ID)
	if err != nil {
		return state.Snapshot{}, false
	}
	capture, err := store.EnvironmentBranchCapture(ctx, parent)
	if err != nil || capture.State != state.EnvironmentBranchCaptureReady {
		return state.Snapshot{}, false
	}
	snap := state.Snapshot{
		ID: capture.ID, DeploymentID: parent, Tier: state.SnapshotTierInit,
		FCVersion: capture.FCVersion, StorageKey: capture.StorageKey, BaseImageVersion: capture.BaseImageVersion,
		CPUTemplate: capture.CPUTemplate, CPUModel: capture.CPUModel,
		MemBytes: capture.MemBytes, DiskBytes: capture.DiskBytes, StoredBytes: capture.StoredBytes,
	}
	if !environmentBranchCaptureFits(snap, e.fcVer, expectedRAMMB, app.AppProtocol) || state.SnapshotDriveKey(snap) == "" ||
		!e.snapshotRestorableSomewhere(ctx, snap) {
		return state.Snapshot{}, false
	}
	return snap, true
}

// environmentBranchCaptureFits applies the restore rules a capture can fail
// without any snapshots row: Firecracker version, guest RAM and, for
// HTTP/2 and gRPC apps, the base image.
func environmentBranchCaptureFits(snap state.Snapshot, fcVersion string, expectedRAMMB int, appProtocol string) bool {
	if snap.StorageKey == "" || snap.FCVersion != fcVersion {
		return false
	}
	if snap.MemBytes > 0 && expectedRAMMB > 0 && snap.MemBytes != int64(expectedRAMMB)<<20 {
		return false
	}
	if (appProtocol == api.AppProtocolHTTP2 || appProtocol == api.AppProtocolGRPC) && snap.BaseImageVersion != fcvm.FAAS_BASE_IMAGE_VERSION {
		return false
	}
	return true
}

// retireEnvironmentBranchCapture stops a capture serving restores once a
// restore from it fell back to cold boot: the app did not acknowledge its
// identity, and every later restore would pay for a second boot.
func (e *Engine) retireEnvironmentBranchCapture(ctx context.Context, captureID, reason string) {
	store, ok := e.store.(state.EnvironmentBranchCaptureStore)
	if !ok {
		return
	}
	if err := store.RetireEnvironmentBranchCapture(ctx, captureID, reason); err != nil && e.log != nil {
		e.log.Warn("sched: retire credential-free capture", "capture_id", captureID, "err", err)
	}
}

package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// Boot grants/receipts are private scheduler authority. Protocol 2 binds measured
// drive handoffs; neither version acknowledges delivered logs or live egress.
type InstanceApplicationStandardBootStore interface {
	IssueInstanceApplicationStandardBoot(context.Context, string, runtimeadmission.Binding) (runtimeadmission.Binding, error)
	PublishInstanceApplicationStandardRuntime(context.Context, string, State, runtimeadmission.Receipt) (Instance, error)
}

// Native admission and configuration evidence become visible with readiness
// in the same transaction. No consumer can observe only half the publication.
type InstanceApplicationStandardConfigPublisher interface {
	PublishInstanceApplicationStandardRuntimeWithConfig(context.Context, string, State, runtimeadmission.Receipt, string, RuntimeConfigInputs) (Instance, error)
}

type ComputeNodeRuntimeIdentityStore interface {
	RegisterComputeNodeRuntimeIdentity(context.Context, runtimeadmission.Identity) error
}

// Historical residency is read only to choose the versioned native capability.
// This reader cannot issue or renew capture authority.
type InstanceApplicationStandardRuntimeReceiptStore interface {
	GetInstanceApplicationStandardRuntimeReceipt(context.Context, string) (runtimeadmission.Receipt, error)
}

type instanceStandardBoot struct {
	Binding       runtimeadmission.Binding
	ExpectedState string
	Receipt       *runtimeadmission.Receipt
	ReceivedAt    time.Time
}

type nativeBootLockedInputs struct {
	Snapshot                  json.RawMessage `json:"input_snapshot"`
	CapturedInputHash         string          `json:"captured_input_hash"`
	NodeID                    string          `json:"node_id"`
	Incarnation               string          `json:"incarnation"`
	ProtocolVersion           uint32          `json:"protocol_version"`
	ClockUnixNano             int64           `json:"clock_unix_nano"`
	ArtifactExpiresAtUnixNano int64           `json:"artifact_expires_at_unix_nano"`
	capture                   InstanceApplicationStandardAdmission
}

func validateStandardBootBinding(binding runtimeadmission.Binding, capture InstanceApplicationStandardAdmission, incarnation string, protocol uint32, now time.Time) error {
	if err := binding.Validate(now); err != nil {
		if errors.Is(err, runtimeadmission.ErrExpired) {
			return ErrApplicationStandardRuntimeStale
		}
		return ErrInvalidArgument
	}
	if binding.ProtocolVersion > protocol || !capture.Managed || binding.InstanceID != capture.InstanceID || binding.AppID != capture.AppID || binding.AccountID != capture.AccountID || binding.DeploymentID != capture.DeploymentID || binding.NodeID != capture.NodeID || binding.Incarnation != incarnation || binding.DesiredRevision != capture.DesiredRevision || capture.PersistedRevision != capture.DesiredRevision || binding.EffectiveHash != capture.EffectiveHash || binding.CapturedInputHash != capture.NativeInputHash || binding.EgressRevision != capture.EgressRevision {
		return ErrApplicationStandardRuntimeStale
	}
	if standardSourceNativeCapture(capture) && binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion {
		return ErrApplicationStandardRuntimeStale
	}
	if binding.ProtocolVersion == runtimeadmission.ArtifactProtocolVersion {
		hash, err := standardCapturedArtifactSourceHash(capture)
		if err != nil || hash != binding.ArtifactSourcesHash {
			return ErrApplicationStandardRuntimeStale
		}
	}
	return nil
}

func standardCapturedArtifactSourceHash(capture InstanceApplicationStandardAdmission) (string, error) {
	sources := make([]runtimeadmission.ArtifactSource, 0, len(capture.RuntimeArtifacts))
	for _, a := range capture.RuntimeArtifacts {
		sources = append(sources, runtimeadmission.ArtifactSource{Kind: a.Kind, WorkloadName: a.WorkloadName, StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes})
	}
	return runtimeadmission.HashArtifactSources(sources)
}

func standardRuntimeReceiptTarget(next State, receipt runtimeadmission.Receipt) bool {
	return next == StateRunning && !receipt.Paused || next == StateWarm && receipt.Paused
}
